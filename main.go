// git-pr submits the stack with each commit becomes a GitHub PR. It detects "Remote-Ref: <remote-branch>" from the
// commit message to know which remote branch to push to. It will attempt to create new "Remote-Ref" if not found.
//
// Usage: git pr -config=/path/to/config.json
package main

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	KeyRemoteRef = "remote-ref"
	head         = "HEAD"
)

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
	lastPRNumber := must(githubGetLastPRNumber())

	// detect missing remote ref
	for commitWithoutRemoteRef := findCommitWithoutRemoteRef(stackedCommits); commitWithoutRemoteRef != nil; commitWithoutRemoteRef = findCommitWithoutRemoteRef(stackedCommits) {
		lastPRNumber++
		remoteRef := fmt.Sprintf("%v/pr%v", config.User, lastPRNumber)
		commitWithoutRemoteRef.SetAttr(KeyRemoteRef, remoteRef)
		debugf("creating remote ref %v for %v", remoteRef, commitWithoutRemoteRef.Title)
		must(execGit("reword", commitWithoutRemoteRef.Hash, "-m", commitWithoutRemoteRef.FullMessage()))

		time.Sleep(1 * time.Second)
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
		wg.Add(len(stackedCommits))
		for _, commit := range stackedCommits {
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
		wg.Add(len(stackedCommits))
		for _, commit := range stackedCommits {
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
					if cm.Hash == commit.Hash {
						fprintf(&bodyB, emojis1[commit.PRNumber%12])
						fprintf(&bodyB, " **[%v (#%v)](%v)**\n", cm.Title, cm.PRNumber, cmURL)
					} else {
						fprintf(&bodyB, "◻️")
						fprintf(&bodyB, " [%v (#%v)](%v)\n", cm.Title, cm.PRNumber, cmURL)
					}
				}
				must(httpRequest("PATCH", pullURL, map[string]string{
					"title": commit.Title,
					"body":  bodyB.String(),
				}))
			}()
		}
		wg.Wait()
	}
}

func findCommitWithoutRemoteRef(commits []*Commit) *Commit {
	for _, commit := range commits {
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
