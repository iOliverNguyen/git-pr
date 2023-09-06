// git-pr submits the stack with each commit becomes a GitHub PR. It detects "Remote-Ref: <remote-branch>" from the
// commit message to know which remote branch to push to. It will attempt to create new "Remote-Ref" if not found.
//
// Usage: git pr -config=/path/to/config.json
package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	KeyRemoteRef = "remote-ref"
	head         = "head"
)

var regexpDraft = regexp.MustCompile(`(?i)\[draft]`)

var emojis0 = []string{"♈️", "♉️", "♊️", "♋️", "♌️", "♍️", "♎️", "♏️", "♐️", "♑️", "♒️", "♓️"}
var emojis1 = []string{"🐹", "🐮", "🐯", "🦊", "🐲", "🐼", "🦁", "🐰", "🐵", "🐻", "🐶", "🐷"}

func main() {
	config = LoadConfig()

	// ensure no uncommitted changes
	if !validateGitStatusClean() {
		fmt.Println(`"git status reports uncommitted changes"`)
		fmt.Print(`
Hint: use "git add ." and "git stash" to clean up the repository
`)
		os.Exit(1)
	}

	originMain := fmt.Sprintf("%v/%v", config.Remote, config.MainBranch)
	stackedCommits := must(getStackedCommits(originMain, head))
	if len(stackedCommits) == 0 {
		exitf("no commits to submit")
	}
	for _, commit := range stackedCommits {
		fmt.Println(commit)
	}
	fmt.Println()

	// validate no duplicated remote ref
	mapRefs := map[string]*Commit{}
	for _, commit := range stackedCommits {
		remoteRef := commit.GetAttr(KeyRemoteRef)
		if remoteRef == "" {
			continue
		}
		if last, ok := mapRefs[remoteRef]; ok {
			exitf("duplicated remote ref %q found for %q and %q", last.GetAttr(KeyRemoteRef), last.ShortHash(), commit.ShortHash())
		}
		mapRefs[remoteRef] = commit
	}

	// create a PR for each commit with missing remote ref, one by one
	for commitWithoutRemoteRef := findCommitWithoutRemoteRef(stackedCommits); commitWithoutRemoteRef != nil; commitWithoutRemoteRef = findCommitWithoutRemoteRef(stackedCommits) {
		// NOT --include-other-authors: do not create pr for commits that are not my own
		if !config.IncludeOtherAuthors && !isMyOwnCommit(commitWithoutRemoteRef) {
			commitWithoutRemoteRef.Skip = true
			continue
		}
		remoteRef := fmt.Sprintf("%v/%v", config.User, commitWithoutRemoteRef.ShortHash())
		commitWithoutRemoteRef.SetAttr(KeyRemoteRef, remoteRef)
		debugf("creating remote ref %v for %v", remoteRef, commitWithoutRemoteRef.Title)
		must(execGit("reword", commitWithoutRemoteRef.Hash, "-m", commitWithoutRemoteRef.FullMessage()))

		time.Sleep(500 * time.Millisecond)
		stackedCommits = must(getStackedCommits(originMain, head))
	}

	pushCommit := func(commit *Commit) (logs string, execFunc func()) {
		args := fmt.Sprintf("%v:refs/heads/%v", commit.ShortHash(), commit.GetAttr(KeyRemoteRef))
		logs = fmt.Sprintf("push -f %v %v", config.Remote, args)
		return logs, func() {
			out := must(execGit("push", "-f", config.Remote, args))
			if strings.Contains(out, "remote: Create a pull request") {
				must(0, githubCreatePRForCommit(commit))
			}
		}
	}
	// push commits, concurrently
	{
		var wg sync.WaitGroup
		for _, commit := range stackedCommits {
			if commit.Skip {
				continue
			}
			wg.Add(1)
			logs, execFunc := pushCommit(commit)
			fmt.Println(logs)
			go func() {
				defer wg.Done()
				execFunc()
			}()
		}
		wg.Wait()
	}

	// checkout the latest stacked commit
	must(execGit("checkout", stackedCommits[len(stackedCommits)-1].Hash))

	// wait for 5 seconds
	fmt.Println("waiting a bit...")
	time.Sleep(5 * time.Second)

	// update commits with PR numbers, concurrently
	{
		var wg sync.WaitGroup
		for i := len(stackedCommits) - 1; i >= 0; i-- {
			commit := stackedCommits[i]
			if commit.PRNumber == 0 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					commit.PRNumber = must(githubGetPRNumberForCommit(commit))
				}()
			}
		}
		wg.Wait()
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
			prURL := fmt.Sprintf("https://%v/%v/pull/%v", config.Host, config.Repo, commit.PRNumber)
			fmt.Printf("update pull request %v\n", prURL)
			go func() {
				defer wg.Done()

				pullURL := fmt.Sprintf("https://api.%v/repos/%v/pulls/%v", config.Host, config.Repo, commit.PRNumber)
				reviewURL := fmt.Sprintf("https://%v/%v/pull/%v/commits/%v", config.Host, config.Repo, commit.PRNumber, commit.Hash)

				var bodyB strings.Builder
				fprintf(&bodyB, "# 👉 [REVIEW](%s) 👈\n\n", reviewURL)
				fprintf(&bodyB, "### %v\n---\n%v\n\n&nbsp;\n", commit.Title, commit.Message)
				for _, cm := range stackedCommits {
					cmURL := fmt.Sprintf("https://%v/%v/pull/%v", config.Host, config.Repo, cm.PRNumber)
					if cm.PRNumber == 0 {
						cmURL = fmt.Sprintf("https://%v/%v/commit/%v", config.Host, config.Repo, cm.ShortHash())
					}
					if cm.Hash == commit.Hash {
						fprintf(&bodyB, emojis1[commit.PRNumber%12])
						fprintf(&bodyB, " **[%v (#%v)](%v)**\n", cm.Title, coalesce(cm.PRNumber, cm.Hash), cmURL)
					} else {
						fprintf(&bodyB, "◻️")
						fprintf(&bodyB, " [%v (#%v)](%v)\n", cm.Title, coalesce(cm.PRNumber, cm.Hash), cmURL)
					}
				}
				must(httpRequest("PATCH", pullURL, map[string]any{
					"title": commit.Title,
					"body":  bodyB.String(),
				}))
				isDraft := regexpDraft.MatchString(commit.Title)
				if isDraft {
					must(execGh("pr", "ready", strconv.Itoa(commit.PRNumber), "--undo"))
				} else {
					must(execGh("pr", "ready", strconv.Itoa(commit.PRNumber)))
				}
			}()
		}
		wg.Wait()
	}
}

func findCommitWithoutRemoteRef(commits []*Commit) *Commit {
	for _, commit := range commits {
		if commit.Skip {
			continue
		}
		if commit.GetAttr(KeyRemoteRef) == "" {
			return commit
		}
	}
	return nil
}

func validateGitStatusClean() bool {
	output := must(execGit("status"))
	return strings.Contains(output, "nothing to commit, working tree clean")
}

func isMyOwnCommit(commit *Commit) bool {
	return commit.AuthorEmail == config.Email
}

func coalesce(a int, b string) string {
	if a != 0 {
		return fmt.Sprint(a)
	}
	return b
}
