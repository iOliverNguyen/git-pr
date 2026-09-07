package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	regexpCommitHash = regexp.MustCompile(`^commit ([0-9a-f]{40})$`)
	regexpAuthor     = regexp.MustCompile(`^Author: (.*) <(.*)>$`)
	regexpDate       = regexp.MustCompile(`^Date:\s+(.*)$`)

	// "key: value"
	// - leading blanks are allowed: FullMessage right-aligns keys, so a footer
	//   with 2+ items indents the shorter ones
	regexpKeyVal = regexp.MustCompile(`^[ \t]*([a-zA-Z0-9-]+)\s*:\s*([^ ].+)$`)
	dateLayouts  = []string{"Mon Jan _2 15:04:05 2006 -0700", "2006-01-02 15:04:05 -0700"}
)

func gitLogs(size int, extra ...string) (string, error) {
	args := []string{"log", fmt.Sprintf("-%v", size)}
	args = append(args, extra...)
	return git(args...)
}

// mustCommitCount returns the number of commits in (from, to], i.e., reachable
// from `to` but not from `from`. Used pre-rewrite to capture depth-from-HEAD
// markers that survive a rewrite pass (which changes hashes but not positions).
func mustCommitCount(from, to string) int {
	out := must(git("rev-list", "--count", fmt.Sprintf("%v..%v", from, to)))
	n, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		exitf("ERROR: failed to parse commit count for %v..%v: %v", from, to, err)
	}
	return n
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
				return nil, errorf("failed to parse time from %q", m[1])
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
		return nil, errorf("failed to parse commit with log:\n%v", strings.Join(backup, "\n"))
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
	if len(attrs) > 0 && strings.TrimSpace(line) == "" {
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

// getStackedCommits returns the commits in (base, target] ordered oldest→newest,
// with empty/invalid commits filtered out. When includeJJWorkingCopy is true and
// jj is enabled, jj's working copy (if non-empty with a description) is appended
// as the newest commit. Range-mode callers should pass false to avoid pulling
// in the working copy when the user has explicitly named a tip.
func getStackedCommits(base, target string, includeJJWorkingCopy bool) ([]*Commit, error) {
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
	if includeJJWorkingCopy && config.jj.enabled {
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

// refSyncAction is what syncLocalRefsAfterPush should do with one local ref.
type refSyncAction int

const (
	refSyncNothing   refSyncAction = iota // already on the pushed commit
	refSyncMove                           // safe to move onto the pushed commit
	refSyncUnrelated                      // shares the name only; moving would drop commits
)

// decideRefSync decides whether a local ref that shares a name with a Remote-Ref
// may be moved onto the commit we just pushed. Sharing the name is not enough:
// a Remote-Ref can be hand-written to a name the user already uses for a branch
// of their own, and pushing a sub-range can put the pushed commit *behind* it.
// Moving is safe in exactly two shapes — a fast-forward, or the same jj change
// re-created by the rewrite phase (the stale-bookmark case this whole function
// exists for). Anything else is left alone and reported.
func decideRefSync(local, pushed string, fastForward, sameChange bool) refSyncAction {
	switch {
	case local == pushed:
		return refSyncNothing
	case fastForward, sameChange:
		return refSyncMove
	default:
		return refSyncUnrelated
	}
}

// isFastForward reports whether moving a ref from `from` to `to` only adds
// commits, i.e. `from` is an ancestor of `to`.
func isFastForward(from, to string) bool {
	_, err := git("merge-base", "--is-ancestor", from, to)
	return err == nil
}

// jjBookmarkTargets returns the commit ids a local jj bookmark points at: none
// if it does not exist, one normally, and several when the bookmark is in a
// conflicted state (jj shows those as `name??`).
func jjBookmarkTargets(name string) []string {
	out, err := jj("log", "-r", fmt.Sprintf("bookmarks(exact:%q)", name), "--no-graph", "-T", `commit_id ++ "\n"`)
	if err != nil {
		return nil
	}
	var ids []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); len(line) == 40 {
			ids = append(ids, line)
		}
	}
	return ids
}

// checkedOutBranches returns every branch checked out in any worktree of this
// repo. Rewinding one of those with `update-ref` is not refused the way
// `git branch -f` would be, and leaves that worktree looking entirely dirty.
func checkedOutBranches() map[string]bool {
	out, err := git("worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	branches := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if ref, ok := strings.CutPrefix(strings.TrimSpace(line), "branch refs/heads/"); ok {
			branches[ref] = true
		}
	}
	return branches
}

// syncLocalRefsAfterPush points any *pre-existing* local ref that shares a name
// with a Remote-Ref we just pushed at the commit we pushed. git-pr pushes
// `<hash>:refs/heads/<ref>` and otherwise never touches local refs, so a local
// branch (or jj bookmark) of that name keeps pointing at the pre-rewrite commit.
// `gh stack link` then pushes that stale local branch *without* --force and git
// rejects the whole atomic push as a non-fast-forward — which reads as a
// mysterious "tip is behind its remote counterpart" for a stack whose remote is
// in fact exactly what we wanted.
//
// Only refs that already exist are moved, and only when decideRefSync says the
// move cannot drop commits: git-pr does not own the Remote-Ref namespace
// locally, must not leave a branch per PR behind in a repo that had none, and
// must not rewind a branch that merely shares a name. Every failure is a
// warning, never fatal — the push itself already succeeded and nothing
// downstream reads the local ref.
func syncLocalRefsAfterPush(commits []*Commit) {
	var moved bool
	var checkedOut map[string]bool
	if !config.jj.enabled {
		checkedOut = checkedOutBranches()
	}
	for _, commit := range commits {
		if commit.Skip {
			continue
		}
		name := commit.GetRemoteRef()
		if name == "" {
			continue
		}
		if config.jj.enabled {
			syncJJBookmarkAfterPush(name, commit, &moved)
			continue
		}
		local, err := resolveRef("refs/heads/" + name)
		if err != nil || local == "" {
			continue // no local branch by that name, nothing to sync
		}
		sameChange := false // git mode has no stable identity across a rewrite
		switch decideRefSync(local, commit.Hash, isFastForward(local, commit.Hash), sameChange) {
		case refSyncNothing:
			continue
		case refSyncUnrelated:
			warnf("local branch %v points at %v, which is not an ancestor of the pushed %v;\n"+
				"  leaving it alone (a later non-forced push of it will be rejected)",
				name, shortHash(local), commit.ShortHash())
			continue
		}
		if checkedOut[name] {
			// moving a checked-out branch would leave that worktree looking
			// like it had uncommitted changes
			warnf("local branch %v is checked out at %v, not the pushed %v;\n"+
				"  leaving it alone", name, shortHash(local), commit.ShortHash())
			continue
		}
		debugf("moving local branch %v to %v (was %v)", name, commit.ShortHash(), shortHash(local))
		if _, err := git("update-ref", "refs/heads/"+name, commit.Hash, local); err != nil {
			warnf("could not move local branch %v onto the pushed commit %v: %v\n"+
				"  the push succeeded; the local branch is stale", name, commit.ShortHash(), err)
		}
	}
	if moved {
		// jj auto-exports after each operation, so this is normally a no-op; it
		// is here to surface the case where an export could *not* happen (a git
		// ref moved behind jj's back), which would leave gh-stack reading the
		// stale ref and reproduce the very bug this function fixes
		if out, err := jj("git", "export"); err != nil || strings.Contains(out, "Failed to export") {
			warnf("jj could not export every bookmark to a git ref: %v\n%v", err, out)
		}
	}
}

// syncJJBookmarkAfterPush is the jj half of syncLocalRefsAfterPush: it moves the
// bookmark through jj rather than writing refs/heads, so jj's view stays
// authoritative instead of being reverted by its next export. It sets *moved
// whenever an export is worth running afterwards.
func syncJJBookmarkAfterPush(name string, commit *Commit, moved *bool) {
	targets := jjBookmarkTargets(name)
	if len(targets) == 0 {
		return // no local bookmark by that name, nothing to sync
	}
	if len(targets) > 1 {
		// a conflicted bookmark has two sides; `bookmark set` would silently
		// collapse it onto ours and drop the other
		short := make([]string, len(targets))
		for i, t := range targets {
			short[i] = shortHash(t)
		}
		warnf("jj bookmark %v is conflicted (%v);\n"+
			"  leaving it alone — resolve it with `jj bookmark set %v -r <rev>`",
			name, strings.Join(short, ", "), name)
		return
	}
	local := targets[0]
	if local == commit.Hash {
		// the bookmark is right; the git ref it exports to may still be stale
		if ref, err := resolveRef("refs/heads/" + name); err == nil && ref != commit.Hash {
			*moved = true
		}
		return
	}
	sameChange := false
	if id, err := jjGetChangeID(local); err == nil && id != "" && commit.ChangeID != "" {
		sameChange = id == commit.ChangeID
	}
	if decideRefSync(local, commit.Hash, isFastForward(local, commit.Hash), sameChange) == refSyncUnrelated {
		warnf("jj bookmark %v points at %v, a different change than the pushed %v;\n"+
			"  leaving it alone (a later non-forced push of it will be rejected)",
			name, shortHash(local), commit.ShortHash())
		return
	}
	debugf("moving jj bookmark %v to %v (was %v)", name, commit.ShortHash(), shortHash(local))
	// --allow-backwards: a rewritten commit is rarely a descendant of the old one
	if _, err := jj("bookmark", "set", name, "-r", commit.Hash, "--allow-backwards"); err != nil {
		warnf("could not move jj bookmark %v onto the pushed commit %v: %v\n"+
			"  the push succeeded; the local bookmark is stale", name, commit.ShortHash(), err)
		return
	}
	*moved = true
}
