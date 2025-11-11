package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	regexpCommitHash = regexp.MustCompile(`^commit ([0-9a-f]{40})$`)
	regexpAuthor     = regexp.MustCompile(`^Author: (.*) <(.*)>$`)
	regexpDate       = regexp.MustCompile(`^Date:\s+(.*)$`)

	// "key: value"  or  "key = value"
	// - must not start with space at the beginning of the line
	regexpKeyVal = regexp.MustCompile(`^([a-zA-Z0-9-]+)\s*:\s*([^ ].+)$`)
	dateLayouts  = []string{"Mon Jan _2 15:04:05 2006 -0700", "2006-01-02 15:04:05 -0700"}
)

func gitLogs(size int, extra ...string) (string, error) {
	args := []string{"log", fmt.Sprintf("-%v", size)}
	args = append(args, extra...)
	return git(args...)
}

func parseLogs(logs string) (out CommitList, _ error) {
	logs = strings.TrimSpace(logs)
	if logs == "" {
		return nil, nil
	}
	lines := strings.Split(logs, "\n")
	part := []string{}
	for _, line := range lines {
		if m := regexpCommitHash.FindStringSubmatch(line); m != nil {
			if len(part) > 0 {
				item, err := parseLogsCommit(part)
				if err != nil {
					return nil, err
				}
				out = append(out, item)
			}
			part = part[:0]
		}
		part = append(part, line)
	}
	item, err := parseLogsCommit(part)
	if err != nil {
		return nil, err
	}
	out = append(out, item)
	return out, err
}

func parseLogsCommit(lines []string) (*Commit, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	backup := lines
	out := &Commit{}
	// parse header
	bodyStart := len(lines) // default: no body
	for i, line := range lines {
		if line == "" {
			bodyStart = i + 1
			break
		}
		if m := regexpCommitHash.FindStringSubmatch(line); m != nil {
			out.Hash = m[1]
		}
		if m := regexpAuthor.FindStringSubmatch(line); m != nil {
			out.AuthorName = m[1]
			out.AuthorEmail = m[2]
		}
		if m := regexpDate.FindStringSubmatch(line); m != nil {
			var date time.Time
			var err error
			for _, layout := range dateLayouts {
				date, err = time.Parse(layout, m[1])
				if err == nil {
					break
				}
			}
			if err != nil {
				panicf(nil, "failed to parse time from %q", m[1])
			}
			out.Date = date.UTC()
		}
	}
	// parse title and body
	bodyLines := lines[bodyStart:]
	if len(bodyLines) > 0 {
		out.Title = strings.TrimSpace(bodyLines[0])
		bodyLines = bodyLines[1:]
		// trim 4 spaces prefix from body lines before parsing trailers
		for i := 0; i < len(bodyLines); i++ {
			bodyLines[i] = strings.TrimPrefix(bodyLines[i], "    ")
		}
		out.Message, out.Attrs = parseTrailers(bodyLines)
	}
	// validate (allow empty title for jujutsu commits like "jj new")
	if out.Hash == "" || out.AuthorName == "" || out.AuthorEmail == "" {
		panicf(nil, "failed to parse commit with log:\n%v", strings.Join(backup, "\n"))
	}
	return out, nil
}

func parseTrailers(lines []string) (message string, attrs []KeyVal) {
	// skip empty lines
	for i := 0; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) != "" {
			lines = lines[i:]
			break
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			lines = lines[:i+1]
			break
		}
	}

	// parse trailer from bottom up
	i, line := 0, ""
	for i = len(lines) - 1; i >= 0; i-- {
		if m := regexpKeyVal.FindStringSubmatch(lines[i]); m != nil {
			key, val := strings.ToLower(m[1]), strings.TrimSpace(m[2])
			attrs = append(attrs, KeyVal{key, val})
		} else {
			line = lines[i]
			break
		}
	}

	// require: trailers must be separated from body by a blank line
	// stop at first non-trailer line, then validate the blank line above
	if len(attrs) > 0 && line == "" {
		if i >= 0 {
			lines = lines[:i] // exclude the blank line
		} else {
			lines = nil
		}
	} else {
		attrs = nil // no valid trailers
	}

	return strings.TrimSpace(strings.Join(lines, "\n")), attrs
}

// jjGetChangeID returns the jj change ID for a git commit hash
func jjGetChangeID(gitHash string) (string, error) {
	if !config.jj.enabled {
		return "", nil
	}
	output, err := jj("log", "-r", gitHash, "--no-graph", "-T", "change_id")
	if err != nil {
		return "", err
	}
	// jj output may include status messages before the actual change ID
	// get the last non-empty line
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line, nil
		}
	}
	return "", errorf("failed to parse change ID from jj output: %s", output)
}

// parseJJWorkingCopy parses jujutsu working copy output into a Commit.
// checkOutput format: "EMPTY|HAS-DESC" or "NONEMPTY|NO-DESC"
// infoOutput format: "changeID|commitID|description"
func parseJJWorkingCopy(checkOutput, infoOutput string) (*Commit, error) {
	lines := strings.Split(strings.TrimSpace(checkOutput), "\n")
	lastLine := lines[len(lines)-1]
	parts := strings.Split(lastLine, "|")

	if len(parts) != 2 {
		return nil, nil
	}

	isEmpty := parts[0] == "EMPTY"
	hasDesc := parts[1] == "HAS-DESC"

	// skip if no description at all
	if !hasDesc {
		return nil, nil
	}

	// skip empty commits (no changes)
	if isEmpty {
		return nil, nil
	}

	// include only non-empty commits with description

	// parse info output
	lines = strings.Split(strings.TrimSpace(infoOutput), "\n")
	firstLine := lines[0]
	parts = strings.Split(firstLine, "|")
	if len(parts) < 3 {
		return nil, errorf("unexpected jj @ output: %s", firstLine)
	}

	changeID := parts[0]
	commitID := parts[1]
	// full description starts after "changeID|commitID|"
	descriptionBody := strings.TrimPrefix(firstLine, changeID+"|"+commitID+"|")
	if len(lines) > 1 {
		// description spans multiple lines
		descriptionBody = descriptionBody + "\n" + strings.Join(lines[1:], "\n")
	}

	// parse description like a commit body
	descLines := strings.Split(descriptionBody, "\n")
	title := ""
	if len(descLines) > 0 {
		title = strings.TrimSpace(descLines[0])
	}
	message, attrs := parseTrailers(descLines[1:])

	// create commit struct
	commit := &Commit{
		Hash:        commitID,
		ChangeID:    changeID,
		Title:       title,
		Message:     message,
		Attrs:       attrs,
		AuthorEmail: config.git.email,
		AuthorName:  config.git.user,
	}
	return commit, nil
}

// jjGetWorkingCopy returns the working copy commit if it's non-empty with description
func jjGetWorkingCopy() (*Commit, error) {
	if !config.jj.enabled {
		return nil, nil
	}

	// check if @ is non-empty with description
	checkOutput, err := jj("log", "-r", "@", "--no-graph", "-T",
		"if(empty, \"EMPTY\", \"NONEMPTY\") ++ \"|\" ++ if(description, \"HAS-DESC\", \"NO-DESC\")")
	if err != nil {
		return nil, err
	}

	// get full info including description body
	infoOutput, err := jj("log", "-r", "@", "--no-graph", "-T",
		"change_id ++ \"|\" ++ commit_id ++ \"|\" ++ description")
	if err != nil {
		return nil, err
	}

	return parseJJWorkingCopy(checkOutput, infoOutput)
}

func getStackedCommits(base, target string) ([]*Commit, error) {
	logs, err := gitLogs(100, fmt.Sprintf("%v..%v", base, target))
	if err != nil {
		return nil, wrapf(err, "failed to find common ancestor for %v and %v", base, target)
	}
	list, err := parseLogs(logs)
	if err != nil {
		return nil, err
	}

	// filter out empty commits (no title and no message)
	filtered := make([]*Commit, 0, len(list))
	for _, commit := range list {
		if commit.Title != "" || commit.Message != "" {
			filtered = append(filtered, commit)
		}
	}
	list = filtered

	// populate jj change IDs if in jj repo
	if config.jj.enabled {
		for _, commit := range list {
			changeID, err := jjGetChangeID(commit.Hash)
			if err != nil {
				debugf("warning: failed to get change ID for %s: %v", commit.ShortHash(), err)
			} else {
				commit.ChangeID = changeID
			}
		}
	}

	// sort from oldest to newest
	result := revert(list)

	// append jj working copy at the end (newest) if applicable
	if config.jj.enabled {
		workingCopy, err := jjGetWorkingCopy()
		if err != nil {
			debugf("warning: failed to get jj working copy: %v", err)
		} else if workingCopy != nil {
			debugf("including jj working copy in stack: %s", workingCopy.Title)
			result = append(result, workingCopy)
		}
	}

	// validate commits and collect warnings/errors
	var warnings []string
	var errors []string
	filtered = result[:0] // reuse filtered slice for non-skipped commits

	for _, commit := range result {
		isEmpty := isEmptyCommit(commit)
		hasEmptyTitle := commit.Title == ""

		if hasEmptyTitle && isEmpty {
			// warn: empty title + no file changes
			warnings = append(warnings, fmt.Sprintf("⚠️  commit %s has empty title and no file changes, skipping", commit.ShortHash()))
			commit.Skip = true
			continue
		} else if hasEmptyTitle {
			// error: empty title + has file changes
			errors = append(errors, fmt.Sprintf("❌ commit %s has empty title but contains file changes (fix required)", commit.ShortHash()))
			commit.Skip = true
			continue
		} else if isEmpty {
			// warn: no file changes
			warnings = append(warnings, fmt.Sprintf("⚠️  commit %s %q has no file changes, skipping", commit.ShortHash(), shortenTitle(commit.Title)))
			commit.Skip = true
			continue
		}

		filtered = append(filtered, commit)
	}
	result = filtered

	// print warnings and errors
	for _, msg := range warnings {
		printf("%s\n", msg)
	}
	for _, msg := range errors {
		printf("%s\n", msg)
	}

	// return error if any validation errors
	if len(errors) > 0 {
		return nil, errorf("validation failed, please fix the commits above")
	}

	return result, nil
}

// isEmptyCommit checks if a commit has no file changes
func isEmptyCommit(commit *Commit) bool {
	// use git to check if commit has file changes
	output, err := git("diff-tree", "--no-commit-id", "--name-only", "-r", commit.Hash)
	if err != nil {
		debugf("warning: failed to check if commit is empty: %v", err)
		return false // assume not empty on error
	}

	return strings.TrimSpace(output) == ""
}

func shortenTitle(title string) string {
	const Max = 36
	if len(title) <= Max {
		return title
	}
	title = title[:Max]
	idx := strings.LastIndexByte(title, ' ')
	if idx == -1 {
		return title + "..."
	} else {
		return title[:idx] + " ..."
	}
}

func deleteBranch(branch string) error {
	branches, err := git("branch")
	if err != nil {
		return err
	}
	if strings.Contains(branches, branch+"\n") {
		_, err = git("branch", "-D", branch) // delete branch
	}
	return err
}
