// git-pr submits the stack with each commit becomes a GitHub PR. It detects "Remote-Ref: <remote-branch>" from the
// commit message to know which remote branch to push to. It will attempt to create new "Remote-Ref" if not found.
//
// Usage: git pr -config=/path/to/config.json
package main

import (
	"fmt"
	"iter"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	KeyTags      = "tags"
	KeyRemoteRef = "remote-ref"
)

const bodyTemplate = `
# Summary





<br><br><br><br>
`

func main() {
	config = LoadConfig()

	// ensure no uncommitted changes
	if !validateGitStatusClean() {
		exitf(`ERROR: git status reports uncommitted changes

Hint: use "git add -A" and "git stash" to clean up the repository
`)
	}

	// checkpoint: validate
	if config.stopAfter == "validate" {
		printf("stopped after: validate\n")
		return
	}

	originMain := fmt.Sprintf("%v/%v", config.git.remote, config.git.remoteTrunk)
	originMainHash := must(resolveRef(originMain))

	// resolve the run's tip (top of the visible stack) and the user-selected
	// base (the lower exclusive endpoint of what we actually push).
	//
	// - no positional args: selectedBase == originMain, fullTip == HEAD/@-.
	// - range args:         selectedBase, fullTip come from config.commitRange.
	//
	// fullStack runs origin/<trunk>..fullTip and is used for stack-info
	// rendering and for resolving the bottom PR's base. stackedCommits is
	// what the rest of the pipeline operates on (validate → rewrite → push →
	// PR create/update).
	var selectedBase, fullTip string
	var rangeBaseDepth, rangeTipDepth int // depths from HEAD, used to recover after rewrite
	if config.commitRange.HasArg {
		selectedBase = config.commitRange.BaseRef
		fullTip = config.commitRange.TipRef
		preRewriteHead := resolveStackHead()
		rangeBaseDepth = mustCommitCount(selectedBase, preRewriteHead)
		rangeTipDepth = mustCommitCount(fullTip, preRewriteHead)
	} else {
		selectedBase = originMain
		fullTip = resolveStackHead()
	}

	fullStack := must(getStackedCommits(originMain, fullTip, !config.commitRange.HasArg))
	if len(fullStack) == 0 {
		exitf("no commits to submit")
	}

	stackedCommits := fullStack
	if config.commitRange.HasArg {
		stackedCommits = sliceFromBase(fullStack, selectedBase, originMainHash)
		if len(stackedCommits) == 0 {
			exitf("no commits to submit in range %v..%v (base %v not found on trunk..tip path)",
				config.commitRange.BaseRef[:8], config.commitRange.TipRef[:8], config.commitRange.BaseRef[:8])
		}
	}
	for _, commit := range stackedCommits {
		printf("%s\n", commit)
	}
	printf("\n")

	// filter draft commits based on configuration
	if shouldSkipDrafts() {
		for _, commit := range stackedCommits {
			if commit.Skip {
				continue // already skipped for other reasons
			}
			if matchAnyPattern(config.draftPatterns, commit.Title) {
				commit.Skip = true
				debugf("skipping draft commit %s: %s", commit.ShortHash(), shortenTitle(commit.Title))
				printf("skip draft \"%v\" (%v)\n", shortenTitle(commit.Title), commit.ShortHash())
			}
		}
	}

	// checkpoint: get-commits
	if config.stopAfter == "get-commits" {
		printf("stopped after: get-commits\n")
		return
	}

	// validate no duplicated remote ref
	mapRefs := map[string]*Commit{}
	for _, commit := range stackedCommits {
		remoteRef := commit.GetRemoteRef()
		if remoteRef == "" {
			continue
		}
		if last, ok := mapRefs[remoteRef]; ok {
			exitf("duplicated remote ref %q found for %q and %q", last.GetRemoteRef(), last.ShortHash(), commit.ShortHash())
		}
		mapRefs[remoteRef] = commit
	}

	// fill remote ref for each commit
	for commitWithoutRemoteRef := range findCommitsWithoutRemoteRef(stackedCommits) {
		remoteRef := fmt.Sprintf("%v/%v", config.gh.user, commitWithoutRemoteRef.ShortHash())
		commitWithoutRemoteRef.SetAttr(KeyRemoteRef, remoteRef)
		debugf("creating remote ref %v for %v", remoteRef, commitWithoutRemoteRef.Title)
		must(rewordCommit(commitWithoutRemoteRef, commitWithoutRemoteRef.FullMessage()))

		time.Sleep(time.Millisecond)
	}
	// rewords change commit_ids; in jj-workspace mode the captured `head` no
	// longer maps to the rewritten chain, so re-resolve before the re-read.
	// In range mode the user's baseRef/tipRef hashes also drift because the
	// rewritten chain has new hashes; recover via depth-from-HEAD captured
	// pre-rewrite (rewrite preserves count and ordering, only hashes change).
	postRewriteHead := resolveStackHead()
	if config.commitRange.HasArg {
		fullTip = must(resolveRef(fmt.Sprintf("%v~%v", postRewriteHead, rangeTipDepth)))
		selectedBase = must(resolveRef(fmt.Sprintf("%v~%v", postRewriteHead, rangeBaseDepth)))
	} else {
		fullTip = postRewriteHead
	}
	fullStack = must(getStackedCommits(originMain, fullTip, !config.commitRange.HasArg))
	stackedCommits = fullStack
	if config.commitRange.HasArg {
		stackedCommits = sliceFromBase(fullStack, selectedBase, originMainHash)
	}

	// checkpoint: rewrite
	if config.stopAfter == "rewrite" {
		printf("stopped after: rewrite\n")
		return
	}

	// resolveBase determines the PR base branch for a given selected commit:
	//   1. the predecessor's Remote-Ref if a non-skipped predecessor exists
	//      in stackedCommits (normal stack-linkage case);
	//   2. otherwise walk fullStack to find the commit just before this one
	//      and use that commit's Remote-Ref if present (range-mode
	//      bottom-of-selection: anchor onto an existing PR);
	//   3. otherwise the remote trunk.
	resolveBase := func(commit *Commit) string {
		if prev := findPrevNonSkipped(stackedCommits, commit); prev != nil {
			return prev.GetRemoteRef()
		}
		return resolveBaseForBottom(fullStack, commit, config.git.remoteTrunk)
	}

	pushCommit := func(commit *Commit) (logs string, execFunc func()) {
		args := fmt.Sprintf("%v:refs/heads/%v", commit.ShortHash(), commit.GetAttr(KeyRemoteRef))
		logs = fmt.Sprintf("push -f %v %v", config.git.remote, args)
		if config.dryRun {
			logs = "[DRY-RUN] " + logs
			return logs, func() {} // no-op for dry-run
		}
		return logs, func() {
			out := must(git("push", "-f", config.git.remote, args))
			time.Sleep(1 * time.Second)
			base := resolveBase(commit)
			if strings.Contains(out, "remote: Create a pull request") {
				mustE(githubCreatePRForCommit(commit, base))
			} else {
				mustE(githubPRUpdateBaseForCommit(commit, base))
			}
		}
	}
	// mark commits we won't push (other authors, unless --include-other-authors)
	for _, commit := range stackedCommits {
		shouldPush := isMyOwnCommit(commit) || config.includeOtherAuthors
		if !shouldPush {
			commit.Skip = true
			author := coalesce(commit.AuthorEmail, "@unknown")
			printf("skip \"%v\" (%v)\n", shortenTitle(commit.Title), author)
		}
	}

	// fail loudly if any pushable commit slipped through the rewrite phase
	// without a Remote-Ref. without this guard, git rejects the empty refspec
	// inside a goroutine and the panic stack does not name the offending commit.
	if missing := validateRemoteRefsBeforePush(stackedCommits); len(missing) > 0 {
		exitf(`ERROR: %d commit(s) have no Remote-Ref after rewrite phase: %v

This usually means the in-memory view of the stack drifted from what jj/git
actually wrote. Re-run git-pr; if it recurs, file an issue with the output of
"git log -10" and the contents of .jj/repo/op_heads (if jj is in use).`,
			len(missing), strings.Join(missing, ", "))
	}

	// push commits, concurrently
	if config.dryRun {
		printf("[DRY-RUN] Would push commits:\n")
	}
	var pushFns []func()
	for _, commit := range stackedCommits {
		if commit.Skip {
			continue
		}
		logs, execFn := pushCommit(commit)
		printf("%s\n", logs)
		if !config.dryRun {
			pushFns = append(pushFns, execFn)
		}
	}
	parallelForEach(pushFns, func(fn func()) { fn() })

	// checkpoint: push
	if config.stopAfter == "push" {
		printf("stopped after: push\n")
		return
	}

	// checkout the latest stacked commit
	if !config.dryRun {
		switch {
		case config.jj.enabled:
			debugf("skipping git checkout in jj repo (jj manages working copy)")
		case config.commitRange.HasArg:
			// the user asked for a specific range; leave HEAD where it was so
			// we don't silently move HEAD below the original tip when the
			// selected tip is not at HEAD.
			debugf("skipping git checkout in range mode (preserving HEAD)")
		default:
			must(git("checkout", stackedCommits[len(stackedCommits)-1].Hash))
		}
	}

	// wait for 5 seconds
	if !config.dryRun {
		printf("waiting a bit...\n")
		time.Sleep(5 * time.Second)
	}

	// update commits with PR numbers, concurrently
	if config.dryRun {
		printf("[DRY-RUN] Would update PR descriptions for:\n")
		for _, commit := range stackedCommits {
			if !commit.Skip {
				printf("  - %s: %s\n", commit.ShortHash(), commit.Title)
			}
		}
		return
	}
	var needsPRNumber []*Commit
	for _, commit := range stackedCommits {
		if commit.PRNumber == 0 {
			needsPRNumber = append(needsPRNumber, commit)
		}
	}
	parallelForEach(needsPRNumber, func(commit *Commit) {
		commit.PRNumber = must(githubGetPRNumberForCommit(commit, resolveBase(commit)))
	})

	if config.commitRange.HasArg {
		// in range mode, look up PR numbers for fullStack commits below the
		// selected range so stack-info bullets render existing PR links instead
		// of falling back to "no PR yet" formatting. lookup-only — never
		// creates a PR for a commit outside the selected range.
		var ancillary []*Commit
		for _, cm := range fullStack {
			if cm.PRNumber == 0 && cm.GetRemoteRef() != "" {
				ancillary = append(ancillary, cm)
			}
		}
		parallelForEach(ancillary, func(commit *Commit) {
			commit.PRNumber = githubLookupPRNumber(commit)
		})
	}

	// checkpoint: pr-create
	if config.stopAfter == "pr-create" {
		printf("stopped after: pr-create\n")
		return
	}

	// update PRs with review link, concurrently
	printf("\n")
	var prBodyTargets []*Commit
	for _, commit := range stackedCommits {
		if commit.Skip {
			continue
		}
		prURL := ghWebURL("pull/%v", commit.PRNumber)
		printf("%v\n", prURL)
		prBodyTargets = append(prBodyTargets, commit)
	}
	parallelForEach(prBodyTargets, func(commit *Commit) {
		pr := must(githubGetPRByNumber(commit.PRNumber))
		pullURL := ghAPIURL("pulls/%v", commit.PRNumber)

		// generate the PR body with stack info — render the full chain from
		// trunk up to the selected tip, so PR bodies of selected commits show
		// their position in the broader stack (not just the selected range).
		stackInfo := generateStackInfo(fullStack, commit)
		body := generatePRBody(commit, pr.Body, stackInfo)

		// update the PR
		must(httpRequest("PATCH", pullURL, map[string]any{
			"title": commit.Title,
			"body":  body,
		}))
		isDraft := matchAnyPattern(config.draftPatterns, commit.Title)
		if isDraft {
			must(gh("pr", "ready", strconv.Itoa(commit.PRNumber), "--undo"))
		} else {
			must(gh("pr", "ready", strconv.Itoa(commit.PRNumber)))
		}
		if tags := commit.GetTags(config.tags...); len(tags) > 0 {
			must(gh("pr", "edit", strconv.Itoa(commit.PRNumber), "--add-label", strings.Join(tags, ",")))
		}
	})
}

func findCommitsWithoutRemoteRef(commits []*Commit) iter.Seq[*Commit] {
	commits = slices.Clone(commits)
	slices.Reverse(commits)
	return func(yield func(*Commit) bool) {
		for _, commit := range commits {
			if commit.Skip {
				continue
			}
			if commit.GetRemoteRef() == "" {
				yield(commit)
			}
		}
	}
}

// findPrevNonSkipped returns the most-recent non-skipped commit strictly before
// target in commits (ordered oldest→newest). Returns nil if no eligible
// predecessor exists, including the case where target is not in commits.
func findPrevNonSkipped(commits []*Commit, target *Commit) (prev *Commit) {
	for _, cm := range commits {
		if cm == target {
			return prev
		}
		if cm.Skip {
			continue
		}
		prev = cm
	}
	return nil
}

// sliceFromBase returns the suffix of fullStack strictly after the commit with
// hash == baseHash. When baseHash equals originMainHash, the entire fullStack
// is returned (origin/<trunk> is exclusive and thus never appears in
// fullStack). Returns nil if baseHash is neither originMainHash nor present in
// fullStack — that case signals "user-supplied base is not on the trunk..tip
// path".
func sliceFromBase(fullStack []*Commit, baseHash, originMainHash string) []*Commit {
	if baseHash == originMainHash {
		return fullStack
	}
	for i, cm := range fullStack {
		if cm.Hash == baseHash {
			return fullStack[i+1:]
		}
	}
	return nil
}

// resolveBaseForBottom returns the branch the bottommost selected PR should
// base on. It walks fullStack to find the commit immediately before bottom and
// returns that commit's Remote-Ref if it has one (i.e., the parent is already a
// PR), otherwise the remote trunk. Used to preserve stack relationships when
// pushing a partial range over an existing stack.
func resolveBaseForBottom(fullStack []*Commit, bottom *Commit, trunk string) string {
	var prev *Commit
	for _, cm := range fullStack {
		if cm.Hash == bottom.Hash {
			break
		}
		prev = cm
	}
	if ref := prev.GetRemoteRef(); ref != "" {
		return ref
	}
	return trunk
}

// parallelForEach runs fn concurrently for each item, waiting for all goroutines
// to finish before returning. An empty slice is a no-op.
func parallelForEach[T any](items []T, fn func(T)) {
	var wg sync.WaitGroup
	for _, item := range items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn(item)
		}()
	}
	wg.Wait()
}

// resolveStackHead returns the commit_id git-log should walk from when computing
// the stack. In jj mode it queries jj's @- each time so the value reflects any
// rewords/rebases performed since the previous call — in jj-workspace setups the
// shared backing .git/HEAD does not follow the workspace, and the @- commit_id
// captured before a reword pass becomes stale once jj rewrites the chain. In
// plain git / git-branchless mode "HEAD" is sufficient because reword updates
// HEAD to track the rewritten descendants.
func resolveStackHead() string {
	if !config.jj.enabled {
		return "HEAD"
	}
	out, err := jj("log", "-r", "@-", "--no-graph", "-T", "commit_id")
	if err != nil {
		exitf("ERROR: failed to resolve jj @-: %v", err)
	}
	head := strings.TrimSpace(out)
	debugf("resolved jj @- to %v", head)
	return head
}

// validateRemoteRefsBeforePush returns the ShortHash of each non-skipped commit
// that has no Remote-Ref attribute. An empty result means every pushable commit
// has a ref and the push phase is safe to start.
func validateRemoteRefsBeforePush(commits []*Commit) []string {
	var missing []string
	for _, c := range commits {
		if c.Skip {
			continue
		}
		if c.GetAttr(KeyRemoteRef) == "" {
			missing = append(missing, c.ShortHash())
		}
	}
	return missing
}

// rewordCommit updates a commit's message using jj describe or git reword
func rewordCommit(commit *Commit, message string) (string, error) {
	if config.jj.enabled {
		// use jj change ID to avoid creating divergent commits
		if commit.ChangeID == "" {
			return "", errorf("commit %s has no change ID", commit.ShortHash())
		}
		debugf("using jj describe with change ID %s", commit.ChangeID[:12])
		return jj("describe", "-r", commit.ChangeID, "-m", message)
	}
	if config.bl.enabled {
		debugf("using git branchless reword to reword commit")
		return git("reword", commit.Hash, "-m", message)
	}

	exitf(`ERROR: neither jj nor git-branchless is available

This tool requires either:
  1. Jujutsu (jj) - install from https://martinvonz.github.io/jj/
     OR
  2. git-branchless - install from https://github.com/arxanas/git-branchless
     Then run: git branchless init

After installation, try again.`)
	return "", nil // unreachable
}

// generateStackInfo generates the stack info section showing all PRs in the stack
func generateStackInfo(stackedCommits []*Commit, currentCommit *Commit) string {
	var stackB strings.Builder
	sprf := func(msg string, args ...any) { fprintf(&stackB, msg, args...) }

	for _, cm := range stackedCommits {
		var cmRef string
		cmURL := ghWebURL("commit/%v", cm.ShortHash())
		switch {
		case cm.PRNumber != 0 && cm.Hash == currentCommit.Hash:
			cmRef = fmt.Sprintf("#%v (👉[%v](%v))", cm.PRNumber, cm.ShortHash(), cmURL)
		case cm.PRNumber != 0:
			cmRef = fmt.Sprintf("#%v", cm.PRNumber)
		default:
			first, last := splitEmail(cm.AuthorEmail)
			formattedEmail := first + "&#x200B;" + last // zero-width space to prevent creating email link
			cmRef = fmt.Sprintf(`&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;<b>[%v (%v)](%v)</b>&nbsp;&nbsp; ${\textsf{\color{lightblue}· %v}}$`, cm.Title, cm.ShortHash(), cmURL, formattedEmail)
		}
		if cm.Hash == currentCommit.Hash {
			sprf("* " + emojisx[currentCommit.PRNumber%len(emojisx)])
		} else {
			sprf("* ◻️")
		}
		sprf(" %v\n", cmRef)
	}

	return stackB.String()
}

// generatePRBody generates the PR body based on commit message and existing PR body
// If commit has a message, it overrides the entire PR body
// If commit has no message (GitHub UI user), it preserves existing content and only updates stack info
func generatePRBody(commit *Commit, existingBody string, stackInfo string) string {
	// normalize line endings from GitHub (may have \r\n)
	existingBody = strings.ReplaceAll(existingBody, "\r\n", "\n")

	if commit.Message != "" {
		// user manages via git commits - override entire PR body
		return fmt.Sprintf("%s\n\n---\n%s", commit.Message, stackInfo)
	}

	// user manages via GitHub UI - preserve their edits, only update stack info
	parts := strings.Split(existingBody, "\n---\n")

	if len(parts) > 1 {
		lastSection := parts[len(parts)-1]
		// check if last section is stack info (has bullets with PR numbers)
		stackInfoPattern := regexp.MustCompile(`(?m)^\* .* #\d+`)
		if stackInfoPattern.MatchString(lastSection) {
			// replace the stack info section
			parts[len(parts)-1] = stackInfo
			return strings.Join(parts, "\n---\n")
		}
		// no stack info found in last section, append it
		return existingBody + "\n\n---\n" + stackInfo
	}

	// no separator found
	if existingBody == "" || existingBody == bodyTemplate {
		// empty or template only, use template
		return bodyTemplate + "\n---\n" + stackInfo
	}
	// has content but no separator, append stack info
	return existingBody + "\n\n---\n" + stackInfo
}

func validateGitStatusClean() bool {
	if config.jj.enabled {
		// check jj working copy status: empty|nonempty + description
		output, err := jj("log", "-r", "@", "--no-graph", "-T",
			"if(empty, \"EMPTY\", \"NONEMPTY\") ++ \"|\" ++ if(description, description.first_line(), \"NO-DESC\")")
		if err != nil {
			debugf("warning: failed to check jj status: %v", err)
			// fallback to git status check
		} else {
			// parse output: "EMPTY|desc" or "NONEMPTY|NO-DESC" or "NONEMPTY|desc"
			lines := strings.Split(strings.TrimSpace(output), "\n")
			lastLine := lines[len(lines)-1] // get last line (actual output)
			parts := strings.Split(lastLine, "|")
			if len(parts) == 2 {
				isEmpty := parts[0] == "EMPTY"
				hasDesc := parts[1] != "NO-DESC"

				if isEmpty {
					debugf("jj working copy is empty, proceeding normally")
					return true
				}
				if !isEmpty && hasDesc {
					debugf("jj working copy has changes with description, will include in stack")
					return true
				}
				// not empty and no description - error
				return false
			}
		}
	}

	// for git repos or jj fallback
	if config.jj.enabled {
		// in jj mode the jj-path above returned earlier on success; getting here
		// means the jj template failed. don't silently swap to `git status` —
		// in a jj workspace GIT_DIR points outside the worktree and the output
		// would be meaningless. surface the failure instead.
		return false
	}
	output := must(git("status"))
	return strings.Contains(output, "nothing to commit, working tree clean")
}

func isMyOwnCommit(commit *Commit) bool {
	return commit.AuthorEmail == config.git.email
}

func splitEmail(email string) (string, string) {
	if idx := strings.Index(email, "@"); idx >= 0 {
		return email[:idx], email[idx:]
	}
	return email, ""
}

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// shouldSkipDrafts determines if draft commits should be skipped
// based on flags and config with proper precedence
func shouldSkipDrafts() bool {
	// --include-draft flag overrides everything (highest precedence)
	if config.includeDraft {
		return false
	}
	// --skip-draft flag or config setting
	return config.skipDraft
}
