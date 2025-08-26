package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// landConfig holds configuration for the land operation
type landConfig struct {
	timeout       time.Duration
	pollInterval  time.Duration
	deleteBranch  bool
	requireChecks bool
	autoMode      bool
	dryRun        bool
	interactive   bool          // enable interactive dashboard
	mergeStrategy MergeStrategy // merge strategy for interactive mode
	autoRetry     bool          // auto-retry failed checks
	pauseOnFail   bool          // pause on failures for manual intervention
	stopAtLast    bool          // stop at last PR if it has failures
}

// MergeStrategy defines when to merge PRs
type MergeStrategy int

const (
	MergeRequiredOnly MergeStrategy = iota + 1 // wait for required checks only
	MergeAllChecks                             // wait for all checks to pass
	MergeCustom                                // wait for custom important checks
	MergeManual                                // manual confirmation for each PR
)

// checkStatus represents the status of a CI check
type checkStatus struct {
	Name        string `json:"name"`
	State       string `json:"state"`
	Bucket      string `json:"bucket"`
	Workflow    string `json:"workflow"`
	Description string `json:"description"`
}

// prInfo holds information about a PR for landing
type prInfo struct {
	Number     int
	Title      string
	URL        string
	HeadSHA    string
	HeadBranch string // branch name to delete after merge
	BaseBranch string
	Commit     *Commit
	// dashboard fields
	Mergeable      string        // MERGEABLE, CONFLICTING, UNKNOWN (from mergeable field)
	MergeStatus    string        // CLEAN, UNSTABLE, BLOCKED, etc. (from mergeStateStatus)
	ChecksStatus   string        // PENDING, PASSING, FAILING
	Checks         []checkStatus // detailed check info
	State          string        // OPEN, MERGED, CLOSED
	ReviewDecision string        // APPROVED, CHANGES_REQUESTED, REVIEW_REQUIRED
	ReviewStatus   string        // summary of review states
	LastUpdated    time.Time     // when status was last fetched
}

// dashboardState holds the state of the interactive dashboard
type dashboardState struct {
	prs           []prInfo
	currentPR     int
	mergeStrategy MergeStrategy
	autoRetry     bool
	pauseOnFail   bool
	stopAtLast    bool
	lastUpdate    time.Time
	updateError   error
}

// landStack orchestrates the landing of a stack of PRs
func landStack(cfg landConfig) error {
	// ensure clean working directory
	if !validateGitStatusClean() {
		return fmt.Errorf("working directory has uncommitted changes")
	}

	// get current stack
	originMain := fmt.Sprintf("%v/%v", config.git.remote, config.git.remoteTrunk)
	debugf("getting stacked commits from %s to %s", originMain, head)
	stackedCommits := must(getStackedCommits(originMain, head))

	if len(stackedCommits) == 0 {
		fmt.Println("no commits to land")
		return nil
	}

	debugf("found %d commits to land", len(stackedCommits))

	// check if local commits differ from remote (for the first commit)
	if len(stackedCommits) > 0 {
		firstCommit := stackedCommits[0]
		if err := checkAndConfirmLocalChanges(firstCommit, stackedCommits); err != nil {
			return err
		}
	}

	// build PR info for each commit
	prs := []prInfo{}
	for _, commit := range stackedCommits {
		if commit.PRNumber == 0 {
			// try to find PR number
			debugf("searching for PR for commit %s", commit.ShortHash())
			commit.PRNumber = must(githubSearchPRNumberForCommit(commit))
			if commit.PRNumber == 0 {
				return fmt.Errorf("no PR found for commit %s", commit.ShortHash())
			}
		}

		debugf("found PR #%d for commit %s: %s", commit.PRNumber, commit.ShortHash(), commit.Title)

		// get PR details
		pr := must(githubGetPRByNumber(commit.PRNumber))
		// construct PR URL
		prURL := fmt.Sprintf("https://%s/%s/pull/%d", config.git.host, config.git.repo, commit.PRNumber)
		prs = append(prs, prInfo{
			Number:     commit.PRNumber,
			Title:      commit.Title,
			URL:        prURL,
			HeadSHA:    commit.Hash,
			HeadBranch: pr.Head.Ref, // store branch name for later deletion
			BaseBranch: config.git.remoteTrunk,
			Commit:     commit,
		})
	}

	// if interactive mode, show dashboard
	if cfg.interactive {
		return landStackInteractive(prs, cfg)
	}

	// land PRs from bottom to top (reverse order)
	for i := 0; i < len(prs); i++ {
		pr := prs[i]
		fmt.Printf("\n[%d/%d] Landing PR #%d: %s\n", i+1, len(prs), pr.Number, pr.Title)
		fmt.Printf("  URL: %s\n", pr.URL)

		if cfg.dryRun {
			fmt.Printf("  [DRY-RUN] Would merge PR #%d\n", pr.Number)
			continue
		}

		// check if PR can be merged
		fmt.Printf("  ⠼ Checking merge status...\n")
		mergeStatus, reason, err := checkPRMergeability(pr.Number)
		if err != nil {
			return fmt.Errorf("failed to check PR #%d mergeability: %w", pr.Number, err)
		}

		// handle different merge states
		switch mergeStatus {
		case "CONFLICTING":
			// conflicts must be resolved - abort
			return fmt.Errorf("PR #%d %s\n  Please resolve conflicts at: %s", pr.Number, reason, pr.URL)
		case "UNKNOWN":
			// retry a few times for unknown status
			for retry := 0; retry < 3 && mergeStatus == "UNKNOWN"; retry++ {
				fmt.Printf("  ⠼ Merge status unknown, retrying...\n")
				time.Sleep(2 * time.Second)
				mergeStatus, reason, err = checkPRMergeability(pr.Number)
				if err != nil {
					return fmt.Errorf("failed to check PR #%d mergeability: %w", pr.Number, err)
				}
			}
			if mergeStatus == "CONFLICTING" {
				return fmt.Errorf("PR #%d %s\n  Please resolve conflicts at: %s", pr.Number, reason, pr.URL)
			}
		case "BLOCKED", "UNSTABLE", "BEHIND":
			// these can potentially be handled by --auto flag
			fmt.Printf("  ⚠ PR %s\n", reason)
			fmt.Printf("  ⚠ Check status at: %s\n", pr.URL)
			fmt.Printf("  ⚠ Will attempt merge with --auto\n")
		case "MERGEABLE", "CLEAN":
			fmt.Printf("  ✓ PR is mergeable\n")
		default:
			debugf("proceeding with unexpected status: %s", mergeStatus)
		}

		// wait for checks if required
		if cfg.requireChecks {
			fmt.Printf("  ⠼ Waiting for checks...")
			if err := waitForChecks(pr.Number, cfg); err != nil {
				fmt.Printf("\r  ❌ Checks failed for PR #%d\n", pr.Number)
				return fmt.Errorf("checks failed for PR #%d: %w", pr.Number, err)
			}
			fmt.Printf("\r  ✓ All checks passed     \n")
		} else {
			debugf("skipping CI checks (requireChecks=false)")
		}

		// detect auto-generated commits
		debugf("checking for auto-generated commits on PR #%d", pr.Number)
		currentHeadSHA, hasAutoCommits := detectAutoGeneratedCommits(pr.Number)
		if hasAutoCommits {
			fmt.Printf("  ⚠ CI added commits, head SHA changed: %s -> %s\n", pr.HeadSHA[:8], currentHeadSHA[:8])
			pr.HeadSHA = currentHeadSHA
		} else {
			debugf("no auto-generated commits detected")
		}

		// merge the PR
		if cfg.dryRun {
			fmt.Printf("  [DRY-RUN] Would merge PR\n")
		} else {
			fmt.Printf("  ⠼ Merging PR...")
			output, err := mergePR(pr.Number, pr.Title, pr.HeadSHA, cfg)

			// check if auto-merge failed due to not being configured
			if err != nil && strings.Contains(output, "enablePullRequestAutoMerge") {
				debugf("auto-merge not enabled for repo, falling back to immediate merge")
				// retry without --auto flag
				cfg.autoMode = false
				output, err = mergePR(pr.Number, pr.Title, pr.HeadSHA, cfg)
				cfg.autoMode = true // restore for next PR
			}

			if err != nil {
				fmt.Printf("\r  ❌ Failed to merge PR #%d\n", pr.Number)
				return fmt.Errorf("failed to merge PR #%d: %w", pr.Number, err)
			}

			// if we used auto mode, wait for merge to complete
			if cfg.autoMode {
				fmt.Printf("\r  ✓ Merge queued with --auto\n")
				fmt.Printf("  ⠼ Waiting for merge to complete...")
				if err := waitForMerge(pr.Number, pr.URL, cfg); err != nil {
					fmt.Printf("\r  ❌ Failed waiting for merge\n")
					return fmt.Errorf("failed waiting for PR #%d to merge: %w", pr.Number, err)
				}
				fmt.Printf("\r  ✓ Merged to %s                    \n", config.git.remoteTrunk)
			} else {
				fmt.Printf("\r  ✓ Merged to %s\n", config.git.remoteTrunk)
			}

			// update next PR's base AFTER merge completes
			if i < len(prs)-1 {
				nextPR := prs[i+1]
				fmt.Printf("  ⠼ Updating next PR #%d base to %s...", nextPR.Number, config.git.remoteTrunk)
				if err := updatePRBase(nextPR.Number, config.git.remoteTrunk); err != nil {
					// check if PR was closed
					if strings.Contains(err.Error(), "closed") {
						fmt.Printf("\r  ❌ PR #%d was closed, cannot update base\n", nextPR.Number)
						return fmt.Errorf("PR #%d was closed, cannot update base: %w", nextPR.Number, err)
					}
					// other errors might be recoverable
					fmt.Printf("\r  ⚠ Could not update PR #%d base: %v\n", nextPR.Number, err)
				} else {
					fmt.Printf("\r  ✓ Updated PR #%d base                      \n", nextPR.Number)
					// wait for GitHub to process the base change
					time.Sleep(2 * time.Second)
				}
			}

			// delete the merged branch after updating dependent PRs
			if cfg.deleteBranch && pr.HeadBranch != "" {
				fmt.Printf("  ⠼ Deleting branch %s...", pr.HeadBranch)
				if err := deleteRemoteBranch(pr.HeadBranch); err != nil {
					// not fatal, just warn
					fmt.Printf("\r  ⚠ Could not delete branch: %v\n", err)
				} else {
					fmt.Printf("\r  ✓ Deleted branch %s                    \n", pr.HeadBranch)
				}
			}
		}

		// pull latest main
		if !cfg.dryRun {
			fmt.Printf("  ⠼ Pulling latest %s...", config.git.remoteTrunk)
			must(git("fetch", config.git.remote, config.git.remoteTrunk))
			must(git("checkout", config.git.remoteTrunk))
			must(git("pull", config.git.remote, config.git.remoteTrunk))
			fmt.Printf("\r  ✓ Pulled latest %s     \n", config.git.remoteTrunk)
		}

		if !cfg.dryRun {
			fmt.Printf("  ✓ PR #%d successfully landed\n", pr.Number)
		}
	}

	if cfg.dryRun {
		fmt.Printf("\n[DRY-RUN] Would land %d PRs\n", len(prs))
	} else {
		fmt.Printf("\n✓ Successfully landed %d PRs\n", len(prs))
	}
	return nil
}

// landStackInteractive shows an interactive dashboard for landing PRs
func landStackInteractive(prs []prInfo, cfg landConfig) error {
	state := &dashboardState{
		prs:           prs,
		currentPR:     0,
		mergeStrategy: cfg.mergeStrategy,
		autoRetry:     cfg.autoRetry,
		pauseOnFail:   cfg.pauseOnFail,
		stopAtLast:    cfg.stopAtLast,
	}

	// fetch initial status for all PRs
	fmt.Print("\033[2J\033[H") // clear screen first
	fmt.Print("⠼ Fetching PR status...")
	updateAllPRStatus(state)

	// main interactive loop
	for {
		// display the dashboard
		showDashboard(state)

		// check if all PRs are merged
		if allPRsMerged(state) {
			fmt.Printf("\n✓ Successfully landed %d PRs\n", len(prs))
			return nil
		}

		// prompt for action
		fmt.Print("\nAction ([y]es to land, [r]efresh, [q]uit): ")
		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		action := strings.TrimSpace(strings.ToLower(input))

		switch action {
		case "y", "yes":
			// start landing
			return landStackFromDashboard(state, cfg)

		case "r", "refresh":
			// refresh status
			fmt.Print("\033[2J\033[H") // clear entire screen and move cursor to top
			fmt.Print("⠼ Refreshing status...")
			updateAllPRStatus(state)
			fmt.Print("\r                      \r") // clear the refreshing message

		case "q", "quit":
			// quit
			fmt.Println("\n⚠️ Landing cancelled")
			return fmt.Errorf("cancelled by user")

		default:
			fmt.Println("Unknown action. Use [y]es, [r]efresh, or [q]uit")
		}
	}
}

// showDashboard displays the interactive dashboard
func showDashboard(state *dashboardState) {
	// clear screen for clean display
	fmt.Print("\033[2J\033[H")

	fmt.Println("================== Stack Landing Status ==================")
	fmt.Printf("Stack: %d PRs\n\n", len(state.prs))

	// show each PR with its status
	for i, pr := range state.prs {
		statusIcon := getPRStatusIcon(pr)

		// show PR number and title (at least 80 chars)
		title := pr.Title
		if len(title) > 80 {
			title = title[:77] + "..."
		}
		fmt.Printf("%2d. PR #%-4d %s %s\n", i+1, pr.Number, statusIcon, title)
		fmt.Printf("    %s\n", pr.URL)

		// always show merge status
		statusText := ""
		// don't show merge status for already merged or closed PRs
		if pr.State == "MERGED" {
			statusText = "✅ Already merged"
		} else if pr.State == "CLOSED" {
			statusText = "❌ Closed (not merged)"
		} else if pr.Mergeable == "CONFLICTING" {
			statusText = "⚠️ Has conflicts - must be resolved"
		} else if pr.Mergeable == "MERGEABLE" && pr.MergeStatus == "UNSTABLE" {
			statusText = "🟡 Mergeable but checks unstable (non-required checks failing)"
		} else if pr.MergeStatus != "" {
			switch pr.MergeStatus {
			case "CLEAN", "MERGEABLE", "HAS_HOOKS":
				statusText = "🟢 Ready to merge"
			case "CONFLICTING", "DIRTY":
				statusText = "⚠️ Has conflicts - must be resolved"
			case "BLOCKED":
				statusText = "🔒 Blocked by branch protection"
			case "BEHIND":
				statusText = "↓ Behind base branch"
			case "UNSTABLE":
				statusText = "⏳ Checks pending or failing"
			case "UNKNOWN":
				statusText = "❓ Status unknown - computing..."
			case "DRAFT":
				statusText = "📝 Draft PR - not ready to merge"
			default:
				statusText = pr.MergeStatus
			}
		}
		if statusText != "" {
			fmt.Printf("    %s\n", statusText)
		}

		// show review status
		if pr.ReviewStatus != "" {
			fmt.Printf("    %s\n", pr.ReviewStatus)
		}

		// show individual CI checks
		if len(pr.Checks) > 0 {
			fmt.Printf("    Checks:\n")
			for _, check := range pr.Checks {
				checkIcon := "⏳"
				switch check.Bucket {
				case "pass", "success":
					checkIcon = "✅"
				case "fail", "failure", "cancel":
					checkIcon = "❌"
				case "skipping", "neutral":
					checkIcon = "◻️"
				case "pending":
					checkIcon = "⏳"
				}
				fmt.Printf("      %s %s\n", checkIcon, check.Name)
			}
		} else if pr.ChecksStatus != "NONE" && pr.ChecksStatus != "" {
			// fallback to summary if no detailed checks
			if pr.ChecksStatus == "FAILING" {
				fmt.Printf("    ❌ Checks failing\n")
			} else if pr.ChecksStatus == "PENDING" {
				fmt.Printf("    ⏳ Checks pending\n")
			} else if pr.ChecksStatus == "PASSING" {
				fmt.Printf("    ✅ All checks passed\n")
			}
		}

		fmt.Println()
	}

	// show summary
	fmt.Println("-----------------------------------------------------------")
	readyCount := 0
	blockedCount := 0
	mergedCount := 0
	for _, pr := range state.prs {
		if pr.State == "MERGED" {
			mergedCount++
		} else if pr.State == "OPEN" && pr.Mergeable == "MERGEABLE" {
			readyCount++
		} else if pr.State == "OPEN" {
			blockedCount++
		}
	}

	if mergedCount > 0 {
		fmt.Printf("Status: %d merged, %d ready, %d blocked\n", mergedCount, readyCount, blockedCount)
	} else {
		fmt.Printf("Status: %d ready to merge, %d blocked\n", readyCount, blockedCount)
	}

	if state.updateError != nil {
		fmt.Printf("⚠ Error updating status: %v\n", state.updateError)
	}
}

// updateAllPRStatus fetches the latest status for all PRs using GraphQL
func updateAllPRStatus(state *dashboardState) {
	state.lastUpdate = time.Now()
	state.updateError = nil

	// build PR numbers list for GraphQL query
	prNumbers := make([]int, len(state.prs))
	for i, pr := range state.prs {
		prNumbers[i] = pr.Number
	}

	// fetch all PR statuses in one GraphQL query
	if err := updatePRStatusBatch(state.prs); err != nil {
		state.updateError = err
		debugf("failed to batch update PR statuses: %v", err)

		// fallback to individual updates
		for i := range state.prs {
			if err := updatePRStatus(&state.prs[i]); err != nil {
				debugf("failed to update PR #%d status: %v", state.prs[i].Number, err)
			}
		}
	}
}

// updatePRStatusBatch fetches status for multiple PRs using GraphQL
func updatePRStatusBatch(prs []prInfo) error {
	if len(prs) == 0 {
		return nil
	}

	// build GraphQL query for all PRs
	query := `query {
		repository(owner: "%s", name: "%s") {`

	// parse repo from config
	parts := strings.Split(config.git.repo, "/")
	if len(parts) != 2 {
		return fmt.Errorf("invalid repo format: %s", config.git.repo)
	}
	owner, repo := parts[0], parts[1]

	query = fmt.Sprintf(query, owner, repo)

	// add each PR to query
	for i, pr := range prs {
		query += fmt.Sprintf(`
			pr%d: pullRequest(number: %d) {
				number
				state
				mergeable
				mergeStateStatus
				reviewDecision
				reviews(last: 10) {
					nodes {
						state
						author {
							login
						}
					}
				}
				statusCheckRollup {
					contexts(first: 100) {
						nodes {
							__typename
							... on CheckRun {
								name
								status
								conclusion
							}
							... on StatusContext {
								context
								state
							}
						}
					}
				}
			}`, i, pr.Number)
	}

	query += `
		}
	}`

	// execute GraphQL query
	output, err := gh("api", "graphql", "-f", fmt.Sprintf("query=%s", query))
	if err != nil {
		return err
	}

	// parse response
	var response struct {
		Data struct {
			Repository map[string]struct {
				Number           int    `json:"number"`
				State            string `json:"state"`
				Mergeable        string `json:"mergeable"`
				MergeStateStatus string `json:"mergeStateStatus"`
				ReviewDecision   string `json:"reviewDecision"`
				Reviews          struct {
					Nodes []struct {
						State  string `json:"state"`
						Author struct {
							Login string `json:"login"`
						} `json:"author"`
					} `json:"nodes"`
				} `json:"reviews"`
				StatusCheckRollup struct {
					Contexts struct {
						Nodes []struct {
							TypeName   string `json:"__typename"`
							Name       string `json:"name"`
							Context    string `json:"context"`
							Status     string `json:"status"`
							State      string `json:"state"`
							Conclusion string `json:"conclusion"`
						} `json:"nodes"`
					} `json:"contexts"`
				} `json:"statusCheckRollup"`
			} `json:",inline"`
		} `json:"data"`
	}

	if err := json.Unmarshal([]byte(output), &response); err != nil {
		return err
	}

	// update each PR with fetched data
	for i := range prs {
		key := fmt.Sprintf("pr%d", i)
		if prData, ok := response.Data.Repository[key]; ok {
			prs[i].State = prData.State
			prs[i].Mergeable = prData.Mergeable
			prs[i].MergeStatus = prData.MergeStateStatus
			prs[i].ReviewDecision = prData.ReviewDecision
			prs[i].LastUpdated = time.Now()

			// process reviews to get review status
			approved := 0
			changesRequested := 0
			commented := 0
			for _, review := range prData.Reviews.Nodes {
				switch review.State {
				case "APPROVED":
					approved++
				case "CHANGES_REQUESTED":
					changesRequested++
				case "COMMENTED":
					commented++
				}
			}
			if changesRequested > 0 {
				prs[i].ReviewStatus = fmt.Sprintf("❌ %d changes requested", changesRequested)
			} else if approved > 0 {
				prs[i].ReviewStatus = fmt.Sprintf("✅ %d approved", approved)
			} else if prData.ReviewDecision == "REVIEW_REQUIRED" {
				prs[i].ReviewStatus = "⏳ Review required"
			} else if commented > 0 {
				prs[i].ReviewStatus = fmt.Sprintf("💬 %d comments", commented)
			} else {
				prs[i].ReviewStatus = ""
			}

			// process checks
			prs[i].Checks = nil
			passing := 0
			failing := 0
			pending := 0

			for _, check := range prData.StatusCheckRollup.Contexts.Nodes {
				// convert to checkStatus
				cs := checkStatus{}

				if check.TypeName == "CheckRun" {
					cs.Name = check.Name
					// determine bucket based on conclusion/status
					switch check.Conclusion {
					case "SUCCESS":
						cs.Bucket = "pass"
						passing++
					case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
						cs.Bucket = "fail"
						failing++
					default:
						if check.Status == "COMPLETED" {
							cs.Bucket = "pass"
							passing++
						} else {
							cs.Bucket = "pending"
							pending++
						}
					}
				} else if check.TypeName == "StatusContext" {
					cs.Name = check.Context
					switch check.State {
					case "SUCCESS":
						cs.Bucket = "pass"
						passing++
					case "FAILURE", "ERROR":
						cs.Bucket = "fail"
						failing++
					default:
						cs.Bucket = "pending"
						pending++
					}
				}

				prs[i].Checks = append(prs[i].Checks, cs)
			}

			// set overall check status
			if failing > 0 {
				prs[i].ChecksStatus = "FAILING"
			} else if pending > 0 {
				prs[i].ChecksStatus = "PENDING"
			} else if passing > 0 {
				prs[i].ChecksStatus = "PASSING"
			} else {
				prs[i].ChecksStatus = "NONE"
			}
		}
	}

	return nil
}

// updatePRStatus fetches the latest status for a single PR
func updatePRStatus(pr *prInfo) error {
	// get PR details
	output, err := gh("pr", "view", strconv.Itoa(pr.Number),
		"--json", "state,mergeable,mergeStateStatus,statusCheckRollup")
	if err != nil {
		return err
	}

	var data struct {
		State             string `json:"state"`
		Mergeable         string `json:"mergeable"`
		MergeStateStatus  string `json:"mergeStateStatus"`
		StatusCheckRollup []struct {
			Name       string `json:"name"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"statusCheckRollup"`
	}

	if err := json.Unmarshal([]byte(output), &data); err != nil {
		return err
	}

	pr.State = data.State
	pr.Mergeable = data.Mergeable
	pr.MergeStatus = data.MergeStateStatus
	pr.LastUpdated = time.Now()

	// determine checks status
	if len(data.StatusCheckRollup) == 0 {
		pr.ChecksStatus = "NONE"
	} else {
		passing := 0
		failing := 0
		pending := 0

		for _, check := range data.StatusCheckRollup {
			switch check.Conclusion {
			case "SUCCESS", "NEUTRAL", "SKIPPED":
				passing++
			case "FAILURE", "CANCELLED", "TIMED_OUT", "ACTION_REQUIRED":
				failing++
			default:
				pending++
			}
		}

		if failing > 0 {
			pr.ChecksStatus = "FAILING"
		} else if pending > 0 {
			pr.ChecksStatus = "PENDING"
		} else {
			pr.ChecksStatus = "PASSING"
		}
	}

	// fetch detailed checks
	pr.Checks = nil // clear old checks
	checksOutput, err := gh("pr", "checks", strconv.Itoa(pr.Number), "--json", "name,state,bucket,workflow,description")
	if err == nil {
		var checks []checkStatus
		if err := json.Unmarshal([]byte(checksOutput), &checks); err == nil {
			pr.Checks = checks
		}
	}

	return nil
}

// helper functions

func getPRStatusIcon(pr prInfo) string {
	switch pr.State {
	case "MERGED":
		return "✅"
	case "CLOSED":
		return "❌"
	default:
		// first check mergeable field for conflicts
		if pr.Mergeable == "CONFLICTING" {
			return "⚠️"
		}

		// check if it's mergeable despite unstable status
		if pr.Mergeable == "MERGEABLE" {
			// even if unstable, it's still mergeable
			if pr.MergeStatus == "UNSTABLE" {
				return "🟡" // yellow circle for mergeable but unstable
			}
			return "🟢" // green for clean mergeable
		}

		// fallback to mergeStateStatus
		switch pr.MergeStatus {
		case "CONFLICTING", "DIRTY":
			return "⚠️"
		case "BLOCKED":
			return "🔒"
		case "BEHIND":
			return "⬇️"
		case "UNSTABLE":
			return "⏳"
		case "UNKNOWN":
			return "❓"
		case "DRAFT":
			return "📝"
		case "HAS_HOOKS", "MERGEABLE", "CLEAN":
			return "🟢"
		default:
			return "◻️"
		}
	}
}

func getChecksIcon(pr prInfo) string {
	switch pr.ChecksStatus {
	case "PASSING":
		return "✅"
	case "FAILING":
		return "❌"
	case "PENDING":
		return "⏳"
	default:
		return "  "
	}
}

func truncateTitle(title string, maxLen int) string {
	if len(title) <= maxLen {
		return title
	}
	return title[:maxLen-3] + "..."
}

func allPRsMerged(state *dashboardState) bool {
	for _, pr := range state.prs {
		if pr.State != "MERGED" {
			return false
		}
	}
	return true
}

// landStackFromDashboard starts the landing process from the dashboard
func landStackFromDashboard(state *dashboardState, cfg landConfig) error {
	fmt.Println("\n🚀 Starting landing process...")

	// use the existing landing logic but with the PR info we already have
	for i, pr := range state.prs {
		// check if already merged
		if pr.State == "MERGED" {
			fmt.Printf("\n[%d/%d] PR #%d already merged\n", i+1, len(state.prs), pr.Number)
			continue
		}

		fmt.Printf("\n[%d/%d] Landing PR #%d: %s\n", i+1, len(state.prs), pr.Number, pr.Title)
		fmt.Printf("  URL: %s\n", pr.URL)

		// verify commit matches remote before landing
		needsRebase, err := verifyAndSyncCommit(&pr, i == 0)
		if err != nil {
			return fmt.Errorf("failed to verify commit for PR #%d: %w", pr.Number, err)
		}
		if needsRebase {
			// commits were out of sync and have been rebased/pushed
			// refresh PR info after push
			fmt.Printf("  ⠼ Refreshing PR info after sync...")
			if err := updatePRStatus(&pr); err != nil {
				fmt.Printf("\r  ⚠ Could not refresh PR status: %v\n", err)
			} else {
				fmt.Printf("\r  ✓ PR info refreshed                \n")
				state.prs[i] = pr // update state with refreshed info
			}
		}

		// check merge status
		if pr.MergeStatus == "CONFLICTING" {
			return fmt.Errorf("PR #%d has conflicts that must be resolved\n  Please resolve at: %s",
				pr.Number, pr.URL)
		}

		// wait for checks if configured
		if cfg.requireChecks {
			fmt.Printf("  ⠼ Waiting for checks...")
			if err := waitForChecks(pr.Number, cfg); err != nil {
				fmt.Printf("\r  ❌ Checks failed for PR #%d: %v\n", pr.Number, err)
				return fmt.Errorf("checks failed for PR #%d: %w", pr.Number, err)
			}
			fmt.Printf("\r  ✓ All checks passed     \n")
		}

		// detect auto-generated commits
		debugf("checking for auto-generated commits on PR #%d", pr.Number)
		currentHeadSHA, hasAutoCommits := detectAutoGeneratedCommits(pr.Number)
		if hasAutoCommits {
			fmt.Printf("  ⚠ CI added commits, head SHA changed: %s -> %s\n", pr.HeadSHA[:8], currentHeadSHA[:8])
			pr.HeadSHA = currentHeadSHA
		}

		// merge the PR
		fmt.Printf("  ⠼ Merging PR...")
		output, err := mergePR(pr.Number, pr.Title, pr.HeadSHA, cfg)

		// check if auto-merge failed due to not being configured
		if err != nil && strings.Contains(output, "enablePullRequestAutoMerge") {
			debugf("auto-merge not enabled for repo, falling back to immediate merge")
			// retry without --auto flag
			cfg.autoMode = false
			output, err = mergePR(pr.Number, pr.Title, pr.HeadSHA, cfg)
			cfg.autoMode = true // restore for next PR
		}

		if err != nil {
			fmt.Printf("\r  ❌ Failed to merge PR #%d: %v\n", pr.Number, err)
			return fmt.Errorf("failed to merge PR #%d: %w", pr.Number, err)
		}

		// if we used auto mode, wait for merge to complete
		if cfg.autoMode {
			fmt.Printf("\r  ✓ Merge queued with --auto\n")
			fmt.Printf("  ⠼ Waiting for merge to complete...")
			if err := waitForMerge(pr.Number, pr.URL, cfg); err != nil {
				fmt.Printf("\r  ❌ Failed waiting for PR #%d to merge: %v\n", pr.Number, err)
				return fmt.Errorf("failed waiting for PR #%d to merge: %w", pr.Number, err)
			}
			fmt.Printf("\r  ✓ Merged to %s                    \n", config.git.remoteTrunk)
		} else {
			fmt.Printf("\r  ✓ Merged to %s\n", config.git.remoteTrunk)
		}

		// update next PR's base AFTER merge completes
		if i < len(state.prs)-1 {
			nextPR := state.prs[i+1]
			fmt.Printf("  ⠼ Updating next PR #%d base to %s...", nextPR.Number, config.git.remoteTrunk)
			if err := updatePRBase(nextPR.Number, config.git.remoteTrunk); err != nil {
				// check if PR was closed
				if strings.Contains(err.Error(), "closed") {
					fmt.Printf("\r  ❌ PR #%d was closed, cannot update base\n", nextPR.Number)
					return fmt.Errorf("PR #%d was closed, cannot update base: %w", nextPR.Number, err)
				}
				// other errors might be recoverable
				fmt.Printf("\r  ⚠ Could not update PR #%d base: %v\n", nextPR.Number, err)
			} else {
				fmt.Printf("\r  ✓ Updated PR #%d base                      \n", nextPR.Number)
				// wait for GitHub to process the base change
				time.Sleep(2 * time.Second)

				// check if the PR now has conflicts after base update
				hasConflicts, err := checkPRConflicts(nextPR.Number)
				if err != nil {
					fmt.Printf("  ⚠ Could not check conflicts for PR #%d: %v\n", nextPR.Number, err)
				} else if hasConflicts {
					fmt.Printf("  ⚠ PR #%d has conflicts after base update\n", nextPR.Number)

					// get all remaining PRs that need rebasing
					remainingPRs := state.prs[i+1:]

					// attempt to rebase all remaining PRs
					if err := rebaseRemainingPRs(remainingPRs); err != nil {
						fmt.Printf("  ❌ Failed to rebase remaining PRs: %v\n", err)
						fmt.Printf("  💡 Manual intervention required. Please resolve conflicts at:\n")
						for _, rpr := range remainingPRs {
							fmt.Printf("     - PR #%d: %s\n", rpr.Number, rpr.URL)
						}
						return fmt.Errorf("conflicts detected after base update, manual resolution required")
					}

					// verify conflicts are resolved
					hasConflicts, err = checkPRConflicts(nextPR.Number)
					if err != nil {
						fmt.Printf("  ⚠ Could not verify conflict resolution: %v\n", err)
					} else if hasConflicts {
						fmt.Printf("  ❌ PR #%d still has conflicts after rebase\n", nextPR.Number)
						return fmt.Errorf("PR #%d still has conflicts after rebase", nextPR.Number)
					} else {
						fmt.Printf("  ✓ Conflicts resolved for remaining PRs\n")
					}
				}
			}
		}

		// delete the merged branch after updating dependent PRs
		if cfg.deleteBranch && pr.HeadBranch != "" {
			fmt.Printf("  ⠼ Deleting branch %s...", pr.HeadBranch)
			if err := deleteRemoteBranch(pr.HeadBranch); err != nil {
				// check if error is because branch doesn't exist (already deleted)
				errStr := err.Error()
				if strings.Contains(errStr, "remote ref does not exist") || strings.Contains(errStr, "unable to delete") {
					// branch already deleted, that's fine
					fmt.Printf("\r  ✓ Branch %s already deleted                    \n", pr.HeadBranch)
				} else {
					// other error, warn about it
					fmt.Printf("\r  ⚠ Could not delete branch: %v\n", err)
				}
			} else {
				fmt.Printf("\r  ✓ Deleted branch %s                    \n", pr.HeadBranch)
			}
		}

		// pull latest main
		fmt.Printf("  ⠼ Pulling latest %s...", config.git.remoteTrunk)
		must(git("fetch", config.git.remote, config.git.remoteTrunk))
		must(git("checkout", config.git.remoteTrunk))
		must(git("pull", config.git.remote, config.git.remoteTrunk))
		fmt.Printf("\r  ✓ Pulled latest %s     \n", config.git.remoteTrunk)

		// rebase remaining commits onto updated main and checkout the last commit
		if i < len(state.prs)-1 {
			// there are more PRs to land
			remainingPRs := state.prs[i+1:]
			lastPR := remainingPRs[len(remainingPRs)-1]

			fmt.Printf("  ⠼ Rebasing remaining commits onto %s...", config.git.remoteTrunk)

			// checkout the branch of the last PR in the stack
			if _, err := git("checkout", lastPR.HeadBranch); err != nil {
				fmt.Printf("\r  ⚠ Could not checkout branch %s: %v\n", lastPR.HeadBranch, err)
			} else {
				// rebase onto the updated main
				if _, err := git("rebase", config.git.remoteTrunk); err != nil {
					fmt.Printf("\r  ❌ Failed to rebase: %v\n", err)
					fmt.Printf("  💡 Please resolve conflicts manually and run 'git-pr land' again\n")
					return fmt.Errorf("failed to rebase remaining commits: %w", err)
				}
				fmt.Printf("\r  ✓ Rebased remaining commits onto %s\n", config.git.remoteTrunk)

				// force push the rebased commits
				fmt.Printf("  ⠼ Pushing rebased commits...")
				for _, rpr := range remainingPRs {
					if _, err := git("push", "--force-with-lease", config.git.remote, rpr.HeadBranch); err != nil {
						fmt.Printf("\r  ⚠ Failed to push branch %s: %v\n", rpr.HeadBranch, err)
					}
				}
				fmt.Printf("\r  ✓ Pushed rebased commits        \n")

				// stay on the last branch for the next iteration
				fmt.Printf("  ✓ Checked out %s\n", lastPR.HeadBranch)
			}
		}

		fmt.Printf("  ✓ PR #%d successfully landed\n", pr.Number)
	}

	fmt.Printf("\n✓ Successfully landed %d PRs\n", len(state.prs))
	return nil
}

// waitForChecks waits for required CI checks to pass
func waitForChecks(prNumber int, cfg landConfig) error {
	startTime := time.Now()
	debugf("waiting for required checks on PR #%d (timeout: %v)", prNumber, cfg.timeout)

	for {
		// check if timeout exceeded
		if time.Since(startTime) > cfg.timeout {
			return fmt.Errorf("timeout waiting for checks after %v", cfg.timeout)
		}

		// get check status
		debugf("polling check status for PR #%d", prNumber)
		output, err := gh("pr", "checks", strconv.Itoa(prNumber), "--required", "--json", "name,state,bucket")
		if err != nil {
			// no required checks configured, which is fine
			debugf("no required checks configured for PR #%d", prNumber)
			return nil
		}

		var checks []checkStatus
		if err := json.Unmarshal([]byte(output), &checks); err != nil {
			return fmt.Errorf("failed to parse check status: %w", err)
		}

		// check if all required checks passed
		allPassed := true
		failedChecks := []string{}
		pendingChecks := []string{}

		for _, check := range checks {
			switch check.Bucket {
			case "pass", "skipping":
				// these are OK
			case "fail", "cancel":
				failedChecks = append(failedChecks, check.Name)
				allPassed = false
			case "pending":
				pendingChecks = append(pendingChecks, check.Name)
				allPassed = false
			}
		}

		if len(failedChecks) > 0 {
			return fmt.Errorf("required checks failed: %s", strings.Join(failedChecks, ", "))
		}

		if allPassed {
			debugf("all required checks passed for PR #%d", prNumber)
			return nil
		}

		// show pending checks
		fmt.Printf("    Pending checks (%d): %s\n", len(pendingChecks), strings.Join(pendingChecks, ", "))
		debugf("waiting %v before next poll", cfg.pollInterval)

		// wait before next poll
		time.Sleep(cfg.pollInterval)
	}
}

// detectAutoGeneratedCommits checks if CI has added commits to the PR
func detectAutoGeneratedCommits(prNumber int) (string, bool) {
	// get current PR head SHA
	debugf("getting current head SHA for PR #%d", prNumber)
	output := must(gh("pr", "view", strconv.Itoa(prNumber), "--json", "headRefOid"))

	var prData struct {
		HeadRefOid string `json:"headRefOid"`
	}
	json.Unmarshal([]byte(output), &prData)

	debugf("current head SHA for PR #%d: %s", prNumber, prData.HeadRefOid[:8])

	// for now, just return the current SHA
	// future enhancement: compare with our tracked commit to detect auto-generated commits
	return prData.HeadRefOid, false
}

// mergePR merges a pull request
func mergePR(prNumber int, title, headSHA string, cfg landConfig) (string, error) {
	// get PR details to clean up the squash commit message
	debugf("getting PR #%d details for merge", prNumber)
	pr := must(githubGetPRByNumber(prNumber))

	// clean up the PR body for the squash commit
	body := cleanupPRBodyForMerge(pr.Body)
	debugf("cleaned PR body (removed footer/template): %d -> %d chars", len(pr.Body), len(body))

	args := []string{"pr", "merge", strconv.Itoa(prNumber)}

	// use squash merge
	args = append(args, "--squash")

	// set custom title and body for the squash commit
	// gh pr merge uses --subject for title and --body for body
	args = append(args, "--subject", title)
	if body != "" {
		args = append(args, "--body", body)
	} else {
		// provide empty body to override PR description
		args = append(args, "--body", "")
	}

	// match head commit to prevent race conditions
	if headSHA != "" {
		args = append(args, "--match-head-commit", headSHA)
	}

	// note: we don't use --delete-branch here, we delete after updating dependent PRs

	// use auto mode if configured
	if cfg.autoMode {
		args = append(args, "--auto")
	}

	debugf("executing: gh %s", strings.Join(args, " "))
	output, err := gh(args...)
	return output, err
}

// Regex patterns for PR body cleanup (compiled once for efficiency)
var (
	// HTML comments: <!-- comment --> or <!--- comment --->
	htmlCommentRegex = regexp.MustCompile(`(?s)<!--.*?-->`)

	// Markdown link reference comments: [//]: # (comment), []: # (comment), etc.
	markdownCommentRegex = regexp.MustCompile(`(?m)^\[[\w/]*]:\s*#\s*[("'].*[)"']?\s*$`)

	// PR reference in stack footer: * #123
	prReferenceRegex = regexp.MustCompile(`^\*.*#\d+`)

	// Multiple consecutive blank lines
	multipleBlankLinesRegex = regexp.MustCompile(`\n{3,}`)

	// Trailing <br> tags at end of body
	trailingBrRegex = regexp.MustCompile(`(?s)(\s*<br\s*\/?>)+\s*$`)

	// Empty template with just "# Summary" and whitespace/br tags
	emptyTemplateRegex = regexp.MustCompile(`(?s)^#\s*Summary\s*(\n|\s|<br\s*\/?>)*$`)

	// Body with only headers and no content
	onlyHeadersRegex = regexp.MustCompile(`(?s)^((#+\s*\w+\s*)|(\w+\s*\n\s*[-=]+\s*)|\s)*$`)
)

// cleanupPRBodyForMerge removes metadata while preserving actual content from PR body
func cleanupPRBodyForMerge(body string) string {
	if body == "" {
		return ""
	}

	// Step 1: Normalize line endings
	body = strings.ReplaceAll(body, "\r\n", "\n")

	// Step 2: Remove comments (HTML and Markdown)
	body = removeComments(body)

	// Step 3: Remove stack info footer
	body = removeStackFooter(body)

	// Step 4: Clean up formatting artifacts
	body = cleanupFormatting(body)

	// Step 5: Check if body is essentially empty
	if isEmptyBody(body) {
		return ""
	}

	return strings.TrimSpace(body)
}

// removeComments removes HTML and Markdown comments from the body
func removeComments(body string) string {
	// remove HTML comments: <!-- --> and <!--- --->
	body = htmlCommentRegex.ReplaceAllString(body, "")

	// remove markdown link reference comments: [//]: #, []: #, etc.
	body = markdownCommentRegex.ReplaceAllString(body, "")

	return body
}

// removeStackFooter removes the PR stack info footer if present
func removeStackFooter(body string) string {
	lines := strings.Split(body, "\n")
	footerStart := findStackFooterStart(lines)

	if footerStart >= 0 {
		lines = lines[:footerStart]
		return strings.Join(lines, "\n")
	}

	return body
}

// findStackFooterStart finds where the stack footer begins
// Returns -1 if no footer found
func findStackFooterStart(lines []string) int {
	for i := 0; i < len(lines); i++ {
		// look for "---" separator
		if strings.TrimSpace(lines[i]) != "---" {
			continue
		}

		// check if preceded by empty line (to distinguish from markdown headers)
		if !hasPrecedingEmptyLine(lines, i) {
			continue
		}

		// check if followed by PR references
		if hasStackInfoAfter(lines, i) {
			// find the first empty line before the separator
			return findFirstEmptyLineBefore(lines, i)
		}
	}

	return -1
}

// hasPrecedingEmptyLine checks if there's at least one empty line before index i
func hasPrecedingEmptyLine(lines []string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) != "" {
			// found non-empty line, stop looking
			return false
		}
		// found empty line
		return true
	}
	return false
}

// hasStackInfoAfter checks if there are PR references after index i
func hasStackInfoAfter(lines []string, i int) bool {
	for j := i + 1; j < len(lines); j++ {
		if prReferenceRegex.MatchString(strings.TrimSpace(lines[j])) {
			return true
		}
	}
	return false
}

// findFirstEmptyLineBefore finds the first empty line before index i
func findFirstEmptyLineBefore(lines []string, i int) int {
	for j := i - 1; j >= 0; j-- {
		if strings.TrimSpace(lines[j]) != "" {
			return j + 1
		}
		if j == 0 {
			return 0
		}
	}
	return i
}

// cleanupFormatting removes formatting artifacts like excessive blank lines and trailing br tags
func cleanupFormatting(body string) string {
	// collapse multiple consecutive blank lines to maximum of 2
	body = multipleBlankLinesRegex.ReplaceAllString(body, "\n\n")

	// remove trailing <br> tags
	body = trailingBrRegex.ReplaceAllString(body, "")

	return body
}

// isEmptyBody checks if the body is essentially empty (only template or headers)
func isEmptyBody(body string) bool {
	trimmed := strings.TrimSpace(body)

	// check for empty template (just "# Summary" with whitespace)
	if emptyTemplateRegex.MatchString(trimmed) {
		return true
	}

	// check if only contains headers without actual content
	if onlyHeadersRegex.MatchString(trimmed) {
		return true
	}

	return false
}

// waitForMerge waits for a PR to be merged after using --auto flag
func waitForMerge(prNumber int, prURL string, cfg landConfig) error {
	startTime := time.Now()
	debugf("waiting for PR #%d to be merged (timeout: %v)", prNumber, cfg.timeout)

	// spinner characters for animation
	spinners := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	spinnerIndex := 0

	for {
		// calculate elapsed time
		elapsed := time.Since(startTime)

		// check if timeout exceeded
		if elapsed > cfg.timeout {
			fmt.Printf("\n") // new line before error
			return fmt.Errorf("timeout waiting for PR #%d to merge after %v\n  Check PR at: %s", prNumber, cfg.timeout, prURL)
		}

		// get PR state with more details
		debugf("checking merge status for PR #%d", prNumber)
		output, err := gh("pr", "view", strconv.Itoa(prNumber), "--json", "state,mergeStateStatus")
		if err != nil {
			fmt.Printf("\n") // new line before error
			return fmt.Errorf("failed to check PR #%d status: %w", prNumber, err)
		}

		var prData struct {
			State            string `json:"state"`
			MergeStateStatus string `json:"mergeStateStatus"`
		}
		if err := json.Unmarshal([]byte(output), &prData); err != nil {
			fmt.Printf("\n") // new line before error
			return fmt.Errorf("failed to parse PR status: %w", err)
		}

		debugf("PR #%d state: %s, merge: %s", prNumber, prData.State, prData.MergeStateStatus)

		// check if merged
		if prData.State == "MERGED" {
			debugf("PR #%d successfully merged", prNumber)
			fmt.Printf("\r\033[K") // clear the status line
			return nil
		}

		// check if closed (not merged)
		if prData.State == "CLOSED" {
			fmt.Printf("\n") // new line before error
			return fmt.Errorf("PR #%d was closed without merging\n  Check PR at: %s", prNumber, prURL)
		}

		// format merge state for display
		mergeStateDisplay := prData.MergeStateStatus
		switch prData.MergeStateStatus {
		case "BLOCKED":
			mergeStateDisplay = "BLOCKED (waiting for approvals/checks)"
		case "UNSTABLE":
			mergeStateDisplay = "UNSTABLE (checks pending)"
		case "BEHIND":
			mergeStateDisplay = "BEHIND (needs update)"
		case "CONFLICTING":
			mergeStateDisplay = "CONFLICTING (has conflicts)"
		case "CLEAN":
			mergeStateDisplay = "CLEAN (ready to merge)"
		}

		// update status on same line
		spinner := spinners[spinnerIndex]
		fmt.Printf("\r\033[K  %s Waiting for merge... (%ds) - Status: %s, Merge: %s",
			spinner, int(elapsed.Seconds()), prData.State, mergeStateDisplay)

		// update spinner index
		spinnerIndex = (spinnerIndex + 1) % len(spinners)

		// still open, wait before next poll
		debugf("PR #%d still open, waiting %v before next poll", prNumber, cfg.pollInterval)
		time.Sleep(cfg.pollInterval)
	}
}

// checkPRMergeability checks if a PR can be merged and returns the reason if not
func checkPRMergeability(prNumber int) (string, string, error) {
	debugf("checking mergeability for PR #%d", prNumber)
	output, err := gh("pr", "view", strconv.Itoa(prNumber), "--json", "mergeable,mergeStateStatus")
	if err != nil {
		return "", "", fmt.Errorf("failed to check PR mergeability: %w", err)
	}

	var prData struct {
		Mergeable        string `json:"mergeable"`
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal([]byte(output), &prData); err != nil {
		return "", "", fmt.Errorf("failed to parse PR mergeability: %w", err)
	}

	debugf("PR #%d mergeability: mergeable=%s, status=%s", prNumber, prData.Mergeable, prData.MergeStateStatus)

	// interpret the merge state
	var reason string
	switch prData.MergeStateStatus {
	case "CONFLICTING":
		reason = "has merge conflicts that must be resolved"
	case "BLOCKED":
		reason = "is blocked by branch protection rules or missing required reviews"
	case "UNSTABLE":
		reason = "has failing or pending CI checks"
	case "BEHIND":
		reason = "needs to be updated with the base branch"
	case "UNKNOWN":
		reason = "merge status is being computed, please retry"
	case "MERGEABLE", "CLEAN":
		reason = ""
	default:
		// if we get an unexpected status, still try to proceed
		debugf("unexpected merge state status: %s", prData.MergeStateStatus)
		reason = ""
	}

	return prData.MergeStateStatus, reason, nil
}

// updatePRBase updates the base branch of a PR
func updatePRBase(prNumber int, newBase string) error {
	_, err := gh("pr", "edit", strconv.Itoa(prNumber), "--base", newBase)
	return err
}

// deleteRemoteBranch deletes a remote branch
func deleteRemoteBranch(branchName string) error {
	_, err := git("push", config.git.remote, "--delete", branchName)
	return err
}

// checkPRConflicts quickly checks if a PR has conflicts
func checkPRConflicts(prNumber int) (bool, error) {
	debugf("checking PR #%d for conflicts", prNumber)
	output, err := gh("pr", "view", strconv.Itoa(prNumber), "--json", "mergeable,mergeStateStatus")
	if err != nil {
		return false, err
	}

	var prData struct {
		Mergeable        string `json:"mergeable"`
		MergeStateStatus string `json:"mergeStateStatus"`
	}
	if err := json.Unmarshal([]byte(output), &prData); err != nil {
		return false, err
	}

	hasConflicts := prData.Mergeable == "CONFLICTING" ||
		prData.MergeStateStatus == "CONFLICTING" ||
		prData.MergeStateStatus == "DIRTY"

	debugf("PR #%d conflicts check: mergeable=%s, mergeState=%s, hasConflicts=%v",
		prNumber, prData.Mergeable, prData.MergeStateStatus, hasConflicts)

	return hasConflicts, nil
}

// verifyAndSyncCommit verifies that the local commit matches the remote PR and syncs if needed
// Returns true if a rebase/push was performed
func verifyAndSyncCommit(pr *prInfo, isFirst bool) (bool, error) {
	// get the PR's branch name (this is the Remote-Ref)
	debugf("verifying commit for PR #%d", pr.Number)
	prOutput, err := gh("pr", "view", strconv.Itoa(pr.Number), "--json", "headRefName")
	if err != nil {
		return false, fmt.Errorf("could not get PR #%d info: %w", pr.Number, err)
	}

	var prBranchData struct {
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(prOutput), &prBranchData); err != nil {
		return false, err
	}

	// the PR's branch name is the Remote-Ref
	remoteRef := prBranchData.HeadRefName
	debugf("PR #%d has Remote-Ref (branch): %s", pr.Number, remoteRef)

	// find the local commit that has this Remote-Ref
	// we need to search through all stacked commits to find the one with matching Remote-Ref
	originMain := fmt.Sprintf("%s/%s", config.git.remote, config.git.remoteTrunk)
	stackedCommits, err := getStackedCommits(originMain, "HEAD")
	if err != nil {
		debugf("could not get stacked commits: %v", err)
		return false, nil
	}

	var localCommit *Commit
	for _, commit := range stackedCommits {
		if commit.GetRemoteRef() == remoteRef {
			localCommit = commit
			debugf("found local commit %s with Remote-Ref: %s", commit.ShortHash(), remoteRef)
			break
		}
	}

	if localCommit == nil {
		debugf("no local commit found with Remote-Ref: %s", remoteRef)
		// this PR doesn't have a corresponding local commit
		fmt.Printf("\n  ⚠ No local commit found for PR #%d (Remote-Ref: %s)\n", pr.Number, remoteRef)
		fmt.Printf("    This PR may have been created elsewhere or your local stack is out of sync.\n")
		return false, nil
	}

	// fetch the remote branch to get latest state
	git("fetch", config.git.remote, remoteRef)

	// get the first commit on the remote PR branch (not HEAD, which might have CI commits)
	remoteBranch := fmt.Sprintf("%s/%s", config.git.remote, remoteRef)
	remoteCommits, err := git("rev-list", "--reverse", fmt.Sprintf("%s..%s", originMain, remoteBranch))
	if err != nil {
		debugf("could not get remote commits: %v", err)
		return false, nil
	}

	remoteSHAs := strings.Split(strings.TrimSpace(remoteCommits), "\n")
	if len(remoteSHAs) == 0 || remoteSHAs[0] == "" {
		debugf("no commits found on remote branch %s", remoteBranch)
		return false, nil
	}

	localSHA := localCommit.Hash

	// check if the local commit matches ANY commit in the PR (not just the first)
	// this handles cases where the PR has multiple commits (e.g., previously merged commits + new commit)
	commitFound := false
	for i, remoteSHA := range remoteSHAs {
		debugf("PR #%d - checking commit %d/%d: local %s vs remote %s",
			pr.Number, i+1, len(remoteSHAs), localSHA[:8], remoteSHA[:8])

		if strings.HasPrefix(remoteSHA, localSHA[:8]) || strings.HasPrefix(localSHA, remoteSHA[:8]) {
			commitFound = true
			debugf("found matching commit at position %d for PR #%d", i+1, pr.Number)
			break
		}
	}

	// check if commits match
	if commitFound {
		debugf("commits match for PR #%d", pr.Number)

		// update HeadSHA to the actual remote HEAD (in case there are CI commits)
		if len(remoteSHAs) > 0 {
			// get the actual HEAD of the remote branch
			remoteHead, _ := git("rev-parse", remoteBranch)
			remoteHead = strings.TrimSpace(remoteHead)
			if remoteHead != "" && remoteHead != pr.HeadSHA {
				debugf("updating PR #%d HeadSHA from %s to %s (CI commits detected)",
					pr.Number, pr.HeadSHA[:8], remoteHead[:8])
				pr.HeadSHA = remoteHead
			}
		}
		return false, nil
	}

	// commits don't match - need to sync
	// show all remote commits to help user understand the mismatch
	fmt.Printf("\n  ⚠ Commit mismatch detected for PR #%d\n", pr.Number)
	fmt.Printf("    Local commit with Remote-Ref '%s':\n", remoteRef)
	fmt.Printf("      %s %s\n", localSHA[:8], localCommit.Title)
	fmt.Printf("    Remote PR has %d commit(s):\n", len(remoteSHAs))

	// show first few remote commits to help identify the issue
	showCount := len(remoteSHAs)
	if showCount > 3 {
		showCount = 3
	}
	for i := 0; i < showCount; i++ {
		commitInfo, _ := git("log", "--format=%h %s", "-n", "1", remoteSHAs[i])
		commitInfo = strings.TrimSpace(commitInfo)
		fmt.Printf("      %d. %s\n", i+1, commitInfo)
	}
	if len(remoteSHAs) > 3 {
		fmt.Printf("      ... and %d more\n", len(remoteSHAs)-3)
	}

	fmt.Printf("\n  This PR needs to be synced with your local changes.\n")
	fmt.Printf("  Would you like to pull, rebase, and push? ([y]es/[n]o): ")

	reader := bufio.NewReader(os.Stdin)
	input, _ := reader.ReadString('\n')
	response := strings.TrimSpace(strings.ToLower(input))

	if response != "y" && response != "yes" {
		return false, fmt.Errorf("sync cancelled by user")
	}

	fmt.Printf("\n  🔄 Syncing PR #%d...\n", pr.Number)

	// save current branch
	currentBranch, _ := git("rev-parse", "--abbrev-ref", "HEAD")
	currentBranch = strings.TrimSpace(currentBranch)

	// pull latest main
	fmt.Printf("    ⠼ Fetching latest %s...", config.git.remoteTrunk)
	if _, err := git("fetch", config.git.remote, config.git.remoteTrunk); err != nil {
		fmt.Printf("\r    ❌ Failed to fetch\n")
		return false, err
	}
	fmt.Printf("\r    ✓ Fetched latest %s\n", config.git.remoteTrunk)

	// checkout main and pull
	if _, err := git("checkout", config.git.remoteTrunk); err != nil {
		return false, fmt.Errorf("failed to checkout %s: %w", config.git.remoteTrunk, err)
	}
	if _, err := git("pull", config.git.remote, config.git.remoteTrunk); err != nil {
		return false, fmt.Errorf("failed to pull %s: %w", config.git.remoteTrunk, err)
	}

	// checkout back to our working branch
	if currentBranch != "" && currentBranch != config.git.remoteTrunk {
		if _, err := git("checkout", currentBranch); err != nil {
			return false, fmt.Errorf("failed to checkout %s: %w", currentBranch, err)
		}
	}

	// rebase onto latest main
	fmt.Printf("    ⠼ Rebasing onto %s...", config.git.remoteTrunk)
	output, err := git("rebase", originMain)
	if err != nil {
		if strings.Contains(output, "CONFLICT") || strings.Contains(err.Error(), "conflict") {
			fmt.Printf("\r    ❌ Rebase conflicts\n")
			git("rebase", "--abort")
			return false, fmt.Errorf("rebase conflicts detected, please resolve manually")
		}
		fmt.Printf("\r    ❌ Rebase failed\n")
		return false, fmt.Errorf("rebase failed: %w", err)
	}
	fmt.Printf("\r    ✓ Rebased onto %s\n", config.git.remoteTrunk)

	// run git-pr to push all changes
	fmt.Printf("    ⠼ Pushing changes...")
	cmd := exec.Command(os.Args[0]) // run git-pr without 'land' subcommand
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	if err := cmd.Run(); err != nil {
		fmt.Printf("\r    ❌ Push failed\n")
		return false, fmt.Errorf("failed to push: %w", err)
	}
	fmt.Printf("\r    ✓ Changes pushed\n")

	// update the PR's HeadSHA after push
	output, err = gh("pr", "view", strconv.Itoa(pr.Number), "--json", "headRefOid")
	if err == nil {
		var prData struct {
			HeadRefOid string `json:"headRefOid"`
		}
		if err := json.Unmarshal([]byte(output), &prData); err == nil {
			pr.HeadSHA = prData.HeadRefOid
			debugf("updated PR #%d HeadSHA to %s after sync", pr.Number, pr.HeadSHA[:8])
		}
	}

	fmt.Printf("  ✓ PR #%d synced successfully\n", pr.Number)
	return true, nil
}

// checkAndConfirmLocalChanges checks if local commits differ from remote and prompts for confirmation
func checkAndConfirmLocalChanges(firstCommit *Commit, allCommits []*Commit) error {
	// find the PR for the first commit
	prNumber := firstCommit.PRNumber
	if prNumber == 0 {
		// try to find PR number
		debugf("searching for PR for commit %s", firstCommit.ShortHash())
		var err error
		prNumber, err = githubSearchPRNumberForCommit(firstCommit)
		if err != nil || prNumber == 0 {
			// no PR found, likely new commits that need to be pushed
			fmt.Printf("⚠️  No PR found for first commit %s\n", firstCommit.ShortHash())
			fmt.Printf("   This appears to be a new stack that hasn't been pushed yet.\n")
			fmt.Printf("\n   Local commits to push:\n")
			for i, commit := range allCommits {
				fmt.Printf("   %d. %s %s\n", i+1, commit.ShortHash(), commit.Title)
			}
			fmt.Printf("\n   Would you like to push these commits and create PRs? ([y]es/[n]o): ")

			reader := bufio.NewReader(os.Stdin)
			input, _ := reader.ReadString('\n')
			response := strings.TrimSpace(strings.ToLower(input))

			if response != "y" && response != "yes" {
				return fmt.Errorf("landing cancelled by user")
			}

			// run git-pr to push and create PRs
			fmt.Println("\n📤 Pushing commits and creating PRs...")
			cmd := exec.Command(os.Args[0]) // run git-pr without 'land' subcommand
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Stdin = os.Stdin
			if err := cmd.Run(); err != nil {
				return fmt.Errorf("failed to push commits: %w", err)
			}
			fmt.Println("\n✅ Commits pushed and PRs created. Please run 'git-pr land' again to continue.")
			os.Exit(0) // exit after pushing
		}
	}

	// get the PR's branch name
	debugf("getting PR #%d branch info", prNumber)
	prOutput, err := gh("pr", "view", strconv.Itoa(prNumber), "--json", "headRefName")
	if err != nil {
		debugf("could not get PR #%d branch: %v", prNumber, err)
		return nil
	}

	var prBranchData struct {
		HeadRefName string `json:"headRefName"`
	}
	if err := json.Unmarshal([]byte(prOutput), &prBranchData); err != nil {
		return nil
	}

	// get the first commit on the remote PR branch (not HEAD, which might have CI commits)
	remoteBranch := fmt.Sprintf("%s/%s", config.git.remote, prBranchData.HeadRefName)
	debugf("getting first commit from remote branch %s", remoteBranch)

	// fetch the remote branch
	git("fetch", config.git.remote, prBranchData.HeadRefName)

	// get commits on the remote branch (from base to branch tip)
	originMain := fmt.Sprintf("%s/%s", config.git.remote, config.git.remoteTrunk)
	remoteCommits, err := git("rev-list", "--reverse", fmt.Sprintf("%s..%s", originMain, remoteBranch))
	if err != nil {
		debugf("could not get remote commits: %v", err)
		return nil
	}

	// get the first commit SHA from the remote branch
	remoteSHAs := strings.Split(strings.TrimSpace(remoteCommits), "\n")
	if len(remoteSHAs) == 0 || remoteSHAs[0] == "" {
		debugf("no commits found on remote branch")
		return nil
	}

	firstRemoteSHA := remoteSHAs[0]
	localSHA := firstCommit.Hash

	debugf("comparing first commit - local: %s, remote: %s", localSHA[:8], firstRemoteSHA[:8])

	// if the first commits differ, we have local changes that need to be pushed
	if !strings.HasPrefix(firstRemoteSHA, localSHA[:8]) && !strings.HasPrefix(localSHA, firstRemoteSHA[:8]) {
		// get the commit message from remote to show the difference
		remoteCommitInfo, _ := git("log", "--format=%h %s", "-n", "1", firstRemoteSHA)
		remoteCommitInfo = strings.TrimSpace(remoteCommitInfo)

		fmt.Printf("⚠️  Local commits differ from remote\n")
		fmt.Printf("   First local commit:  %s %s\n", localSHA[:8], firstCommit.Title)
		fmt.Printf("   First remote commit: %s (PR #%d)\n", remoteCommitInfo, prNumber)

		// check if there are additional commits on remote (likely CI-generated)
		if len(remoteSHAs) > len(allCommits) {
			fmt.Printf("   Note: Remote has %d commits, local has %d commits\n", len(remoteSHAs), len(allCommits))
			fmt.Printf("         (Remote may have CI-generated commits)\n")
		}

		fmt.Printf("\n   This usually means you have local changes that haven't been pushed.\n")
		fmt.Printf("   Would you like to push all commits before landing? ([y]es/[n]o): ")

		reader := bufio.NewReader(os.Stdin)
		input, _ := reader.ReadString('\n')
		response := strings.TrimSpace(strings.ToLower(input))

		if response != "y" && response != "yes" {
			return fmt.Errorf("landing cancelled by user")
		}

		// run git-pr to push updates
		fmt.Println("\n📤 Pushing local changes...")
		cmd := exec.Command(os.Args[0]) // run git-pr without 'land' subcommand
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to push changes: %w", err)
		}
		fmt.Println("\n✅ Changes pushed. Continuing with landing...")
	} else {
		debugf("first commits match, no push needed")
	}

	return nil
}

// rebaseRemainingPRs rebases all remaining PRs onto the latest main branch
func rebaseRemainingPRs(remainingPRs []prInfo) error {
	fmt.Printf("\n  🔄 Rebasing remaining PRs onto %s...\n", config.git.remoteTrunk)

	// save current branch
	currentBranch, err := git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		debugf("could not get current branch: %v", err)
		currentBranch = ""
	} else {
		currentBranch = strings.TrimSpace(currentBranch)
	}

	// fetch latest main
	fmt.Printf("    ⠼ Fetching latest %s...", config.git.remoteTrunk)
	if _, err := git("fetch", config.git.remote, config.git.remoteTrunk); err != nil {
		fmt.Printf("\r    ❌ Failed to fetch %s\n", config.git.remoteTrunk)
		return fmt.Errorf("failed to fetch %s: %w", config.git.remoteTrunk, err)
	}
	fmt.Printf("\r    ✓ Fetched latest %s\n", config.git.remoteTrunk)

	// checkout and pull latest main
	fmt.Printf("    ⠼ Checking out %s...", config.git.remoteTrunk)
	if _, err := git("checkout", config.git.remoteTrunk); err != nil {
		fmt.Printf("\r    ❌ Failed to checkout %s\n", config.git.remoteTrunk)
		return fmt.Errorf("failed to checkout %s: %w", config.git.remoteTrunk, err)
	}

	if _, err := git("pull", config.git.remote, config.git.remoteTrunk); err != nil {
		fmt.Printf("\r    ❌ Failed to pull %s\n", config.git.remoteTrunk)
		return fmt.Errorf("failed to pull %s: %w", config.git.remoteTrunk, err)
	}
	fmt.Printf("\r    ✓ Checked out latest %s\n", config.git.remoteTrunk)

	// get the base for rebase
	originMain := fmt.Sprintf("%s/%s", config.git.remote, config.git.remoteTrunk)

	// for each remaining PR, fetch its branch and rebase
	for i, pr := range remainingPRs {
		fmt.Printf("    ⠼ Processing PR #%d (%s)...", pr.Number, pr.HeadBranch)

		// fetch the PR's remote branch
		if _, err := git("fetch", config.git.remote, pr.HeadBranch); err != nil {
			debugf("could not fetch branch %s: %v", pr.HeadBranch, err)
		}

		// check if local branch exists
		localBranches, _ := git("branch", "--list", pr.HeadBranch)
		branchExists := strings.Contains(localBranches, pr.HeadBranch)

		if branchExists {
			// checkout existing branch
			if _, err := git("checkout", pr.HeadBranch); err != nil {
				fmt.Printf("\r    ❌ Failed to checkout branch %s\n", pr.HeadBranch)
				return fmt.Errorf("failed to checkout branch %s: %w", pr.HeadBranch, err)
			}
		} else {
			// create and checkout branch from remote
			remoteBranch := fmt.Sprintf("%s/%s", config.git.remote, pr.HeadBranch)
			if _, err := git("checkout", "-b", pr.HeadBranch, remoteBranch); err != nil {
				fmt.Printf("\r    ❌ Failed to create branch %s\n", pr.HeadBranch)
				return fmt.Errorf("failed to create branch %s from %s: %w", pr.HeadBranch, remoteBranch, err)
			}
		}

		// attempt rebase onto main
		fmt.Printf("\r    ⠼ Rebasing PR #%d onto %s...", pr.Number, config.git.remoteTrunk)
		output, err := git("rebase", originMain)
		if err != nil {
			// check if rebase has conflicts
			if strings.Contains(output, "CONFLICT") || strings.Contains(err.Error(), "conflict") {
				fmt.Printf("\r    ❌ Rebase conflicts for PR #%d\n", pr.Number)
				// abort the rebase
				git("rebase", "--abort")

				// provide helpful message
				fmt.Printf("    💡 To resolve manually:\n")
				fmt.Printf("       git checkout %s\n", pr.HeadBranch)
				fmt.Printf("       git rebase %s\n", originMain)
				fmt.Printf("       # resolve conflicts\n")
				fmt.Printf("       git push -f %s %s\n", config.git.remote, pr.HeadBranch)

				return fmt.Errorf("rebase conflicts detected for PR #%d, manual intervention required", pr.Number)
			}
			fmt.Printf("\r    ❌ Failed to rebase PR #%d\n", pr.Number)
			return fmt.Errorf("failed to rebase PR #%d: %w", pr.Number, err)
		}

		// force push the rebased branch
		fmt.Printf("\r    ⠼ Pushing rebased PR #%d...", pr.Number)
		if _, err := git("push", "-f", config.git.remote, pr.HeadBranch); err != nil {
			fmt.Printf("\r    ❌ Failed to push PR #%d\n", pr.Number)
			return fmt.Errorf("failed to push rebased branch for PR #%d: %w", pr.Number, err)
		}

		fmt.Printf("\r    ✓ Rebased PR #%d (%d/%d)\n", pr.Number, i+1, len(remainingPRs))
	}

	// checkout the last rebased PR's branch to ensure we're on the latest commit
	if len(remainingPRs) > 0 {
		lastPR := remainingPRs[len(remainingPRs)-1]
		fmt.Printf("    ⠼ Checking out last PR's branch %s...", lastPR.HeadBranch)
		if _, err := git("checkout", lastPR.HeadBranch); err != nil {
			debugf("could not checkout last PR branch %s: %v", lastPR.HeadBranch, err)
			// fallback to original branch if it exists
			if currentBranch != "" && currentBranch != config.git.remoteTrunk {
				git("checkout", currentBranch)
			} else {
				git("checkout", config.git.remoteTrunk)
			}
		} else {
			// get the new HEAD commit after rebase
			newHead, _ := git("rev-parse", "HEAD")
			newHead = strings.TrimSpace(newHead)
			fmt.Printf("\r    ✓ Checked out %s (HEAD: %s)\n", lastPR.HeadBranch, newHead[:8])
		}
	} else if currentBranch != "" && currentBranch != config.git.remoteTrunk {
		// restore original branch if no PRs were rebased
		if _, err := git("checkout", currentBranch); err != nil {
			debugf("could not restore branch %s: %v", currentBranch, err)
			git("checkout", config.git.remoteTrunk)
		}
	}

	fmt.Printf("    ✓ Successfully rebased %d PRs\n", len(remainingPRs))

	// wait for GitHub to process the updates
	fmt.Printf("    ⠼ Waiting for GitHub to process updates...")
	time.Sleep(5 * time.Second)
	fmt.Printf("\r    ✓ GitHub updated                        \n")

	return nil
}
