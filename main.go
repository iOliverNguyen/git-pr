// git-pr submits the stack with each commit becomes a GitHub PR. It detects "Remote-Ref: <remote-branch>" from the
// commit message to know which remote branch to push to. It will attempt to create new "Remote-Ref" if not found.
//
// Usage: git pr -config=/path/to/config.json
package main

import (
	"fmt"
	"iter"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	KeyTags      = "tags"
	KeyRemoteRef = "remote-ref"
	head         = "HEAD"
)

const bodyTemplate = `
# Summary





<br><br><br><br>
`

var regexpDraft = regexp.MustCompile(`(?i)\[draft]`)

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
	stackedCommits := must(getStackedCommits(originMain, head))
	if len(stackedCommits) == 0 {
		exitf("no commits to submit")
	}
	for _, commit := range stackedCommits {
		printf("%s\n", commit)
	}
	printf("\n")

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

		time.Sleep(500 * time.Millisecond)
		stackedCommits = must(getStackedCommits(originMain, head))
	}

	// checkpoint: rewrite
	if config.stopAfter == "rewrite" {
		printf("stopped after: rewrite\n")
		return
	}

	prevCommit := func(commit *Commit) (prev *Commit) {
		for _, cm := range stackedCommits {
			if cm == commit {
				return prev
			}
			if cm.Skip {
				continue
			}
			prev = cm
		}
		panic("not found")
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
			if strings.Contains(out, "remote: Create a pull request") {
				must(0, githubCreatePRForCommit(commit, prevCommit(commit)))
			} else {
				must(0, githubPRUpdateBaseForCommit(commit, prevCommit(commit)))
			}
		}
	}
	// push commits, concurrently
	if config.dryRun {
		printf("[DRY-RUN] Would push commits:\n")
	}
	{
		var wg sync.WaitGroup
		for _, commit := range stackedCommits {
			// push my own commits
			// and include others' commits if "--include-other-authors" is set
			shouldPush := isMyOwnCommit(commit) || config.includeOtherAuthors
			if !shouldPush {
				commit.Skip = true
				author := coalesce(commit.AuthorEmail, "@unknown")
				printf("skip \"%v\" (%v)\n", shortenTitle(commit.Title), author)
				continue
			}
			wg.Add(1)
			logs, execFunc := pushCommit(commit)
			printf("%s\n", logs)
			if !config.dryRun {
				go func() {
					defer wg.Done()
					execFunc()
				}()
			} else {
				wg.Done()
			}
		}
		wg.Wait()
	}

	// checkpoint: push
	if config.stopAfter == "push" {
		printf("stopped after: push\n")
		return
	}

	// checkout the latest stacked commit
	if !config.dryRun {
		must(git("checkout", stackedCommits[len(stackedCommits)-1].Hash))
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
	{
		var wg sync.WaitGroup
		for i := len(stackedCommits) - 1; i >= 0; i-- {
			commit := stackedCommits[i]
			if commit.PRNumber == 0 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					var prev *Commit
					for j := 0; j < i; j++ {
						cm := stackedCommits[j]
						if !cm.Skip {
							prev = cm
							break
						}
					}
					commit.PRNumber = must(githubGetPRNumberForCommit(commit, prev))
				}()
			}
		}
		wg.Wait()
	}

	// checkpoint: pr-create
	if config.stopAfter == "pr-create" {
		printf("stopped after: pr-create\n")
		return
	}

	// update PRs with review link, concurrently
	{
		var wg sync.WaitGroup
		for _, commit := range stackedCommits {
			if commit.Skip {
				continue
			}
			wg.Add(1)
			commit := commit
			prURL := fmt.Sprintf("https://%v/%v/pull/%v", config.git.host, config.git.repo, commit.PRNumber)
			printf("update pull request %v\n", prURL)
			go func() {
				defer wg.Done()

				pr := must(githubGetPRByNumber(commit.PRNumber))
				pullURL := fmt.Sprintf("https://api.%v/repos/%v/pulls/%v", config.git.host, config.git.repo, commit.PRNumber)

				// generate the PR body with stack info
				stackInfo := generateStackInfo(stackedCommits, commit)
				body := generatePRBody(commit, pr.Body, stackInfo)

				// update the PR
				must(httpRequest("PATCH", pullURL, map[string]any{
					"title": commit.Title,
					"body":  body,
				}))
				isDraft := regexpDraft.MatchString(commit.Title)
				if isDraft {
					must(gh("pr", "ready", strconv.Itoa(commit.PRNumber), "--undo"))
				} else {
					must(gh("pr", "ready", strconv.Itoa(commit.PRNumber)))
				}
				if tags := commit.GetTags(config.tags...); len(tags) > 0 {
					must(gh("pr", "edit", strconv.Itoa(commit.PRNumber), "--add-label", strings.Join(tags, ",")))
				}
			}()
		}
		wg.Wait()
	}
}

func findCommitsWithoutRemoteRef(commits []*Commit) iter.Seq[*Commit] {
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
		cmURL := fmt.Sprintf("https://%v/%v/commit/%v", config.git.host, config.git.repo, cm.ShortHash())
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

func coalesce(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
