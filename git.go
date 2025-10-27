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
	regexpDate       = regexp.MustCompile(`^Date:   (.*)$`)
	regexpKeyVal     = regexp.MustCompile(`^\s+([a-zA-Z0-9-]+):(.*)$`)
	dateLayouts      = []string{"Mon Jan _2 15:04:05 2006 -0700", "2006-01-02 15:04:05 -0700"}
)

func gitLogs(size int, extra ...string) (string, error) {
	args := []string{"log", fmt.Sprintf("-%v", size)}
	args = append(args, extra...)
	return git(args...)
}

func parseLogs(logs string) (out CommitList, _ error) {
	if strings.TrimSpace(logs) == "" {
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
	bodyStart := 0
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
	// truncate empty lines
	bodyEnd := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			bodyEnd = i + 1
			break
		}
	}
	bodyLines := lines[bodyStart:bodyEnd]
	// parse footer
	footerStart := len(bodyLines)
	for i := len(bodyLines) - 1; i >= 0; i-- {
		line := bodyLines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := regexpKeyVal.FindStringSubmatch(line); m != nil {
			key, val := strings.ToLower(m[1]), strings.TrimSpace(m[2])
			out.Attrs = append(out.Attrs, KeyVal{key, val})
		} else {
			footerStart = i + 1
			break
		}
	}
	// parse body
	out.Title, out.Message = parseBody(bodyLines[:footerStart])
	// validate
	if out.Hash == "" || out.AuthorName == "" || out.AuthorEmail == "" || out.Title == "" {
		panicf(nil, "failed to parse commit with log:\n%v", strings.Join(backup, "\n"))
	}
	return out, nil
}

func parseBody(lines []string) (string, string) {
	if len(lines) == 0 {
		return "", ""
	}
	title := strings.TrimSpace(lines[0])
	var b strings.Builder
	for _, line := range lines[1:] {
		b.WriteString(strings.TrimPrefix(line, "    "))
		b.WriteByte('\n')
	}
	return title, strings.TrimSpace(b.String())
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

	// skip if empty and --allow-empty is not set
	if isEmpty && !config.allowEmpty {
		return nil, nil
	}

	// include if: (non-empty) OR (empty with --allow-empty flag)

	// get full info including description body
	infoOutput, err := jj("log", "-r", "@", "--no-graph", "-T",
		"change_id ++ \"|\" ++ commit_id ++ \"|\" ++ description")
	if err != nil {
		return nil, err
	}

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
	title, message := parseBody(descLines)

	// create commit struct
	commit := &Commit{
		Hash:        commitID,
		ChangeID:    changeID,
		Title:       title,
		Message:     message,
		AuthorEmail: config.git.email,
		AuthorName:  config.git.user,
	}

	// extract attributes (Remote-Ref, etc.) from description lines
	// jj descriptions don't have leading whitespace, so use simpler regex
	regexpJjKeyVal := regexp.MustCompile(`^([a-zA-Z0-9-]+):\s*(.*)$`)
	for _, line := range descLines {
		line = strings.TrimSpace(line)
		if m := regexpJjKeyVal.FindStringSubmatch(line); m != nil {
			key, val := strings.ToLower(m[1]), strings.TrimSpace(m[2])
			commit.Attrs = append(commit.Attrs, KeyVal{key, val})
		}
	}

	return commit, nil
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

	return result, nil
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
