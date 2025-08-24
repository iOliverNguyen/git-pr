// git-pr submits the stack with each commit becomes a GitHub PR. It detects "Remote-Ref: <remote-branch>" from the
// commit message to know which remote branch to push to. It will attempt to create new "Remote-Ref" if not found.
//
// Usage: git pr -config=/path/to/config.json
package main

import (
	"fmt"
	"iter"
	"os"
	"os/exec"
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
		fmt.Println("stopped after: validate")
		return
	}

	originMain := fmt.Sprintf("%v/%v", config.git.remote, config.git.remoteTrunk)
	stackedCommits := must(getStackedCommits(originMain, head))
	if len(stackedCommits) == 0 {
		exitf("no commits to submit")
	}
	for _, commit := range stackedCommits {
		fmt.Println(commit)
	}
	fmt.Println()

	// checkpoint: get-commits
	if config.stopAfter == "get-commits" {
		fmt.Println("stopped after: get-commits")
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
	commitsToUpdate := make(map[string]*Commit)
	for commitWithoutRemoteRef := range findCommitsWithoutRemoteRef(stackedCommits) {
		suffix := commitWithoutRemoteRef.ShortHash()
		remoteRef := fmt.Sprintf("%v/%v", config.gh.user, suffix)
		commitWithoutRemoteRef.SetAttr(KeyRemoteRef, remoteRef)
		debugf("creating remote ref %v for %v", remoteRef, commitWithoutRemoteRef.Title)
		commitsToUpdate[commitWithoutRemoteRef.Hash] = commitWithoutRemoteRef
	}

	// if there are commits to update, rewrite them using git plumbing
	if len(commitsToUpdate) > 0 {
		if config.dryRun {
			fmt.Println("[DRY-RUN] Would rewrite commits:")
			for hash, commit := range commitsToUpdate {
				fmt.Printf("  - %s: add Remote-Ref: %s\n", hash[:8], commit.GetAttr(KeyRemoteRef))
			}
		} else {
			stackedCommits = gitRewriteCommits(originMain, stackedCommits, commitsToUpdate)
		}
	}

	// checkpoint: rewrite
	if config.stopAfter == "rewrite" {
		fmt.Println("stopped after: rewrite")
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
			if strings.Contains(out, "remote: Create a pull request") {
				must(0, githubCreatePRForCommit(commit, prevCommit(commit)))
			} else {
				must(0, githubPRUpdateBaseForCommit(commit, prevCommit(commit)))
			}
		}
	}
	// push commits, concurrently
	if config.dryRun {
		fmt.Println("[DRY-RUN] Would push commits:")
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
				fmt.Printf("skip \"%v\" (%v)\n", shortenTitle(commit.Title), author)
				continue
			}
			wg.Add(1)
			logs, execFunc := pushCommit(commit)
			fmt.Println(logs)
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
		fmt.Println("stopped after: push")
		return
	}

	// checkout the latest stacked commit
	if !config.dryRun {
		must(git("checkout", stackedCommits[len(stackedCommits)-1].Hash))
	}

	// wait for 5 seconds
	if !config.dryRun {
		fmt.Printf("waiting a bit...\n")
		time.Sleep(5 * time.Second)
	}

	// update commits with PR numbers, concurrently
	if config.dryRun {
		fmt.Println("[DRY-RUN] Would update PR descriptions for:")
		for _, commit := range stackedCommits {
			if !commit.Skip {
				fmt.Printf("  - %s: %s\n", commit.ShortHash(), commit.Title)
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
		fmt.Println("stopped after: pr-create")
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
			fmt.Printf("update pull request %v\n", prURL)
			go func() {
				defer wg.Done()

				pr := must(githubGetPRByNumber(commit.PRNumber))
				pullURL := fmt.Sprintf("https://api.%v/repos/%v/pulls/%v", config.git.host, config.git.repo, commit.PRNumber)

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
					cmURL := fmt.Sprintf("https://%v/%v/commit/%v", config.git.host, config.git.repo, cm.ShortHash())
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

func gitRewriteCommits(originMain string, stackedCommits []*Commit, commitsToUpdate map[string]*Commit) []*Commit {
	// helper function to recreate a commit with new parent and/or message
	recreateCommit := func(commit *Commit, newParent string, newMessage string) string {
		// get the tree object from the original commit
		tree := must(git("rev-parse", commit.Hash+"^{tree}"))

		// get author and committer info from original commit
		authorInfo := must(git("log", "-1", "--format=%an <%ae>", commit.Hash))
		authorDate := must(git("log", "-1", "--format=%aI", commit.Hash))
		committerInfo := must(git("log", "-1", "--format=%cn <%ce>", commit.Hash))
		committerDate := must(git("log", "-1", "--format=%cI", commit.Hash))

		// if no new message provided, get the original
		if newMessage == "" {
			newMessage = must(git("log", "-1", "--format=%B", commit.Hash))
		}

		// create environment for preserving commit metadata
		env := []string{
			"GIT_AUTHOR_NAME=" + strings.Split(authorInfo, " <")[0],
			"GIT_AUTHOR_EMAIL=" + strings.Trim(strings.Split(authorInfo, " <")[1], ">"),
			"GIT_AUTHOR_DATE=" + authorDate,
			"GIT_COMMITTER_NAME=" + strings.Split(committerInfo, " <")[0],
			"GIT_COMMITTER_EMAIL=" + strings.Trim(strings.Split(committerInfo, " <")[1], ">"),
			"GIT_COMMITTER_DATE=" + committerDate,
		}

		// create new commit object
		cmd := exec.Command("git", "commit-tree", tree, "-p", newParent)
		cmd.Stdin = strings.NewReader(newMessage)
		cmd.Env = append(os.Environ(), env...)
		output, err := cmd.Output()
		if err != nil {
			panic(fmt.Sprintf("failed to recreate commit %s: %v", commit.Hash[:8], err))
		}
		return strings.TrimSpace(string(output))
	}

	// create a mapping of old commit -> new commit
	replacements := make(map[string]string)

	// process commits from oldest to newest
	for i, commit := range stackedCommits {
		// determine the parent for this commit
		var parent string
		if i == 0 {
			// first commit - use original parent
			parent = must(git("rev-parse", commit.Hash+"^"))
		} else {
			// use previous commit (possibly rewritten)
			prevCommit := stackedCommits[i-1]
			if newHash, replaced := replacements[prevCommit.Hash]; replaced {
				parent = newHash
			} else {
				parent = prevCommit.Hash
			}
		}

		// check if this commit needs updating
		commitToUpdate, needsRemoteRef := commitsToUpdate[commit.Hash]
		parentChanged := i > 0 && replacements[stackedCommits[i-1].Hash] != ""

		if needsRemoteRef || parentChanged {
			// determine the message to use
			var message string
			if needsRemoteRef {
				message = commitToUpdate.FullMessage()
			}
			// recreate commit with new parent and/or message
			newCommit := recreateCommit(commit, parent, message)
			replacements[commit.Hash] = newCommit

			if needsRemoteRef {
				debugf("created new commit %s to replace %s (added Remote-Ref)", newCommit[:8], commit.Hash[:8])
			} else {
				debugf("recreated commit %s to replace %s (parent changed)", newCommit[:8], commit.Hash[:8])
			}
		}
	}

	// update the branch to point to the new HEAD
	if len(replacements) > 0 {
		lastCommit := stackedCommits[len(stackedCommits)-1]
		if newHead, replaced := replacements[lastCommit.Hash]; replaced {
			must(git("update-ref", "HEAD", newHead))
			debugf("updated HEAD to %s", newHead[:8])
		}
	}

	// reload the commits after rewriting
	return must(getStackedCommits(originMain, head))
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
