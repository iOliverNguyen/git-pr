package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type NewPRBody struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}
type PR struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	Head   struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	// Stack is non-nil when the PR is part of a GitHub native stack. It is the
	// signal that base edits via `gh pr edit --base` are blocked, and it carries
	// the stack Number needed to dissolve the stack (`gh stack unstack <n>`).
	Stack *struct {
		Number   int `json:"number"`
		Size     int `json:"size"`
		Position int `json:"position"`
	} `json:"stack"`
	UpdatedAt *time.Time
}

func githubGetPRNumberForCommit(commit *Commit, base string) (int, error) {
	if commit.PRNumber != 0 {
		return commit.PRNumber, nil
	}
	ghURL := ghAPIURL("commits/%v/pulls?per_page=100", commit.Hash)
	jsonBody, err := httpGET(ghURL)
	switch {
	case err != nil && strings.Contains(err.Error(), "No commit found"):
		return githubSearchPRNumberForCommit(commit)
	case err != nil:
		return 0, err
	}

	var out []PR
	err = json.Unmarshal(jsonBody, &out)
	if err != nil {
		return 0, errorf("failed to parse request body: %v", err)
	}

	remoteRef := commit.GetRemoteRef()
	if remoteRef != "" {
		for _, pr := range out {
			if pr.Head.Ref == remoteRef {
				return pr.Number, nil
			}
		}
	}
	if commit.Skip {
		return githubSearchPRNumberForCommit(commit)
	}

	// the commit was pushed and got "Everything up-to-date", try creating new pr
	err = githubCreatePRForCommit(commit, base)
	if err != nil {
		return 0, err
	}
	return commit.PRNumber, nil
}

// githubLookupPRNumber returns the existing PR number for a commit by querying
// the GitHub API and matching against the commit's Remote-Ref branch. Returns
// 0 if no PR is found or the lookup fails. Unlike githubGetPRNumberForCommit,
// this never creates a PR — used to populate PRNumber for commits in fullStack
// that are outside the selected push range, so stack-info bullets render the
// real PR links even for non-selected commits.
func githubLookupPRNumber(commit *Commit) int {
	if commit.PRNumber != 0 {
		return commit.PRNumber
	}
	remoteRef := commit.GetRemoteRef()
	if remoteRef == "" {
		return 0
	}
	ghURL := ghAPIURL("commits/%v/pulls?per_page=100", commit.Hash)
	jsonBody, err := httpGET(ghURL)
	if err != nil {
		return 0
	}
	var out []PR
	if err := json.Unmarshal(jsonBody, &out); err != nil {
		return 0
	}
	for _, pr := range out {
		if pr.Head.Ref == remoteRef {
			return pr.Number
		}
	}
	return 0
}

func githubGetPRByNumber(number int) (*PR, error) {
	ghURL := ghAPIURL("pulls/%d", number)
	jsonBody, err := httpGET(ghURL)
	if err != nil {
		return nil, err
	}

	var out PR
	err = json.Unmarshal(jsonBody, &out)
	if err != nil {
		return nil, errorf("failed to parse request body: %v", err)
	}

	return &out, nil
}

var regexpPRURL = regexp.MustCompile(`/pull/(\d+)`)

func githubCreatePRForCommit(commit *Commit, base string) error {
	args := []string{"pr", "create", "--title", commit.Title, "--body", "", "--head", commit.GetRemoteRef(), "--base", base}
	if tags := commit.GetTags(config.tags...); len(tags) > 0 {
		args = append(args, "--label", strings.Join(tags, ","))
	}
	printf("create pull request for %q\n", commit.Title)
	out, err := gh(args...)
	if err != nil {
		return err
	}
	m := regexpPRURL.FindStringSubmatch(out)
	if m == nil {
		return errorf("failed to parse PR number from gh pr create output: %v", out)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		return errorf("failed to parse PR number %q: %v", m[1], err)
	}
	commit.PRNumber = n
	return nil
}

func githubPRUpdateBaseForCommit(commit *Commit, base string) error {
	prNumber, err := githubGetPRNumberForCommit(commit, base)
	if err != nil {
		return err
	}
	// Record the resolved number now (githubGetPRNumberForCommit doesn't set it
	// on branch-matched PRs until a later phase) so a deferred stack realign and
	// its warning can name the PR.
	if commit.PRNumber == 0 {
		commit.PRNumber = prNumber
	}
	// Skip the edit when the base already matches: this avoids a needless
	// updatePullRequest mutation, which GitHub blocks on natively-stacked PRs.
	// If the pre-check fetch fails, fall through and attempt the edit anyway.
	if pr, perr := githubGetPRByNumber(prNumber); perr == nil && pr != nil && pr.Base.Ref == base {
		debugf("PR #%d base already %q, skip base edit", prNumber, base)
		return nil
	}
	_, err = gh("pr", "edit", strconv.Itoa(prNumber), "--base", base)
	if isBaseChangeBlockedByStack(err) {
		// GitHub owns the base branch of a natively-stacked PR and rejects
		// updatePullRequest. Record it so main() can realign the whole stack
		// once, after the push barrier (with confirmation), instead of
		// crashing here inside a push goroutine.
		commit.BaseBlocked = true
		debugf("PR #%d base change to %q blocked by native stack; deferring realign", prNumber, base)
		return nil
	}
	return err
}

// isBaseChangeBlockedByStack reports whether err is GitHub rejecting a
// base-branch edit because the PR is part of a native stack, e.g.
// "GraphQL: Cannot change the base branch because the pull request is part of
// a stack. (updatePullRequest)".
func isBaseChangeBlockedByStack(err error) bool {
	return err != nil && strings.Contains(err.Error(), "part of a stack")
}

// isGhStackMissing reports whether a `gh stack ...` call failed because the
// gh-stack extension is not installed (`unknown command "stack" for "gh"`).
func isGhStackMissing(out string, err error) bool {
	return err != nil && strings.Contains(out+err.Error(), "unknown command")
}

// isStackNeedsRebuild reports whether `gh stack link` refused an incremental
// update because the existing stack cannot be grown into the requested shape.
// gh-stack only ever appends to the top and never drops a PR, so it refuses both
// when the local stack drops a PR ("✗ Cannot update stack: this would remove
// #22054 from the stack") and when a PR belongs below the top ("✗ Cannot update
// stack: new PRs must be added to the top of the existing stack"). Matching the
// shared "Cannot update stack" prefix covers both and any future sibling; either
// way the only scriptable remedy is dissolve + relink.
func isStackNeedsRebuild(out string, err error) bool {
	return err != nil && strings.Contains(out+err.Error(), "Cannot update stack")
}

// isStackPushRejected reports whether `gh stack link` failed because its own
// push of the local branches was *rejected* ("! [rejected] ... (non-fast-forward)").
// gh-stack pushes the *local* branch refs without --force, so this fires when a
// local ref no longer descends from what is on the remote — a local branch left
// behind by a rewrite (see syncLocalRefsAfterPush), or a remote branch someone
// else moved. It deliberately does not match gh-stack's generic "failed to push
// branches" wrapper on its own: that also covers auth failures, branch
// protection, rejecting hooks and network errors, none of which the stale-ref
// advice would help with. Those fall through to the default case, which prints
// gh's own output.
func isStackPushRejected(out string, err error) bool {
	if err == nil {
		return false
	}
	all := out + err.Error()
	return strings.Contains(all, "non-fast-forward") || strings.Contains(all, "[rejected]")
}

// isStackPartiallyUnstacked reports whether `gh stack unstack` left the stack in
// place. It exits 0 in that case, printing "Some pull requests are queued for
// merge or have auto-merge enabled and remain stacked on GitHub" — GitHub refuses
// to unstack a PR that is queued or has auto-merge on, and gh-stack then keeps
// the whole stack rather than partially dissolving it.
func isStackPartiallyUnstacked(out string) bool {
	return strings.Contains(out, "remain stacked")
}

// ghStackReason extracts gh-stack's own one-line explanation ("✗ Cannot update
// stack: ...") from its output, so git-pr can echo why the update was refused
// instead of paraphrasing it. Returns "" when no such line is present.
func ghStackReason(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.Contains(line, "Cannot update stack") {
			return strings.TrimSpace(strings.TrimPrefix(line, "✗"))
		}
	}
	return ""
}

// githubStackNumberForCommits returns the native stack number that the commits'
// PRs belong to (0 if none are in a stack). The number lives on each PR's stack
// field; the first non-nil one wins.
func githubStackNumberForCommits(commits []*Commit) int {
	for _, commit := range commits {
		if commit.PRNumber == 0 {
			continue
		}
		if pr, err := githubGetPRByNumber(commit.PRNumber); err == nil && pr != nil && pr.Stack != nil {
			return pr.Stack.Number
		}
	}
	return 0
}

// githubStackRealign rebuilds the native GitHub stack numbered stackNumber so it
// matches the local stack `branches` (ordered bottom→top). gh-stack's `link`
// never removes a PR from an existing stack and `modify` is interactive-only, so
// the only scriptable way to drop the orphaned PR(s) is to dissolve the whole
// stack (`gh stack unstack <n>`) and relink just the local branches into a fresh
// stack, which recreates it with correct base chaining. Any PR no longer in the
// chain is left as a standalone open PR.
func githubStackRealign(stackNumber int, branches []string) error {
	out, err := gh("stack", "unstack", strconv.Itoa(stackNumber))
	if err != nil {
		if strings.Contains(out+err.Error(), "unknown command") {
			return errorf("git-pr needs the gh-stack extension to fix a native GitHub stack:\n" +
				"  gh extension install github/gh-stack")
		}
		return errorf("gh stack unstack %d failed: %v", stackNumber, err)
	}
	// Relinking after a partial unstack would fail with the same opaque error, so
	// stop here and name the real cause.
	if isStackPartiallyUnstacked(out) {
		return errorf("stack #%d could not be dissolved: some PRs are queued for merge or have\n"+
			"auto-merge enabled, so GitHub kept them stacked. Let them land (or dequeue /\n"+
			"disable auto-merge), then re-run; or fix the stack with `gh stack modify`.", stackNumber)
	}
	if _, err := gh(append([]string{"stack", "link"}, branches...)...); err != nil {
		return errorf("gh stack link %s failed: %v", strings.Join(branches, " "), err)
	}
	return nil
}

var regexpNumber = regexp.MustCompile(`[0-9]+`)

func githubSearchPRNumberForCommit(commit *Commit) (int, error) {
	query := fmt.Sprintf("in:title %v", commit.Title)
	result, err := gh("pr", "list", "--limit=1", "--search", query)
	if err != nil {
		debugf("failed to search PR for commit (ignored) %q: %v\n", commit.Title, err)
		return 0, nil
	}
	s := regexpNumber.FindString(result)
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, errorf("failed to parse PR number %q: %v", s, err)
	}
	return n, nil
}
