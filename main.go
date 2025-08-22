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
	KeyTags      = "tags"
	KeyRemoteRef = "remote-ref"
	head         = "HEAD"
)

const bodyTemplate = `
# Summary

<br><br><br><br>
`

var regexpDraft = regexp.MustCompile(`(?i)\[draft]`)

// select emojis

func main() {
	config = LoadConfig()

	// ensure no uncommitted changes
	if !validateGitStatusClean() {
		fmt.Println(`"git status reports uncommitted changes"`)
		fmt.Print(`
Hint: use "git add -A" and "git stash" to clean up the repository
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
	for commitWithoutRemoteRef := findCommitWithoutRemoteRef(stackedCommits); commitWithoutRemoteRef != nil; commitWithoutRemoteRef = findCommitWithoutRemoteRef(stackedCommits) {
		remoteRef := fmt.Sprintf("%v/%v", config.User, commitWithoutRemoteRef.ShortHash())
		commitWithoutRemoteRef.SetAttr(KeyRemoteRef, remoteRef)
		debugf("creating remote ref %v for %v", remoteRef, commitWithoutRemoteRef.Title)
		must(execGit("reword", commitWithoutRemoteRef.Hash, "-m", commitWithoutRemoteRef.FullMessage()))

		time.Sleep(500 * time.Millisecond)
		stackedCommits = must(getStackedCommits(originMain, head))
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
		logs = fmt.Sprintf("push -f %v %v", config.Remote, args)
		return logs, func() {
			out := must(execGit("push", "-f", config.Remote, args))
			time.Sleep(1 * time.Second)
			if strings.Contains(out, "remote: Create a pull request") {
				must(0, githubCreatePRForCommit(commit, prevCommit(commit)))
			} else {
				must(0, githubPRUpdateBaseForCommit(commit, prevCommit(commit)))
			}
		}
	}
	// push commits, concurrently
	{
		var wg sync.WaitGroup
		for _, commit := range stackedCommits {
			// push my own commits
			// and include others' commits if "--include-other-authors" is set
			shouldPush := isMyOwnCommit(commit) || config.IncludeOtherAuthors
			if !shouldPush {
				commit.Skip = true
				author := coalesce(commit.AuthorEmail, "@unknown")
				fmt.Printf("skip \"%v\" (%v)\n", shortenTitle(commit.Title), author)
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
	fmt.Printf("waiting a bit...\n")
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

				pr := must(githubGetPRByNumber(commit.PRNumber))
				pullURL := fmt.Sprintf("https://api.%v/repos/%v/pulls/%v", config.Host, config.Repo, commit.PRNumber)

				parsedBody := func() string {
					footerIndex := prDelimiterRegexp.FindStringIndex(pr.Body)
					if len(footerIndex) > 0 {
						startIdx := footerIndex[0]
						return strings.TrimSpace(pr.Body[:startIdx])
					}
					return pr.Body
				}()

				// generate the PR's body:
				// - if the user edited the body on github, keep the body (+ commit message)
				// - if the user didn't edit the body, but set the commit message, keep the commit message
				// - if the user didn't edit the body and didn't set the commit message, use the default template
				var bodyB strings.Builder
				prf := func(msg string, args ...any) { fprintf(&bodyB, msg, args...) }
				prLine := func() { prf("---\n\n") }
				prDelim := func() { prf("%v\n\n", prDelimiterToGenerated) }
				prMessage := func() { prf("%v\n\n", commit.Message) }
				if parsedBody != "" {
					prf("%v\n\n\n\n\n\n\n\n", parsedBody)
					prDelim()
					prLine()
					prMessage()
				} else if commit.Message == "" {

					prf("%v\n\n\n\n\n\n\n\n", bodyTemplate) // TODO: config template
					prDelim()
					prLine()
					prMessage()
				} else {
					prDelim()
					prMessage()
					prLine()
				}

				// generate list of PRs:
				// - for the current PR with an emoji, mark with an emoji and point to the commit
				// - for other PRs, if it's from the author, use the PR number
				// - otherwise, use the commit title and hash
				for _, cm := range stackedCommits {
					var cmRef string
					cmURL := fmt.Sprintf("https://%v/%v/commit/%v", config.Host, config.Repo, cm.ShortHash())
					switch {
					case cm.PRNumber != 0 && cm.Hash == commit.Hash:
						cmRef = fmt.Sprintf("#%v (👉[%v](%v))", cm.PRNumber, cm.ShortHash(), cmURL)
					case cm.PRNumber != 0:
						cmRef = fmt.Sprintf("#%v", cm.PRNumber)
					default:
						first, last := splitEmail(cm.AuthorEmail)
						formattedEmail := first + "&#x200B;" + last // zero-width space to prevent creating email link
						cmRef = fmt.Sprintf(`&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;<b>[%v (%v)](%v)</b>&nbsp;&nbsp; ${\textsf{\color{lightblue}· %v}}$`, cm.Title, cm.ShortHash(), cmURL, formattedEmail)
					}
					if cm.Hash == commit.Hash {
						prf("* " + emojisx[commit.PRNumber%len(emojisx)])
					} else {
						prf("* ◻️")
					}
					prf(" %v\n", cmRef)
				}

				// update the PR
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
				if tags := commit.GetTags(config.Tags...); len(tags) > 0 {
					must(execGh("pr", "edit", strconv.Itoa(commit.PRNumber), "--add-label", strings.Join(tags, ",")))
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
		if commit.GetRemoteRef() == "" {
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
