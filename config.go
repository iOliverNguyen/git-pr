package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/zalando/go-keyring"
	"gopkg.in/yaml.v3"
)

var emojisx = []string{"🐹", "🐮", "🐯", "🦊", "🐲", "🐼", "🦁", "🐰", "🐵", "🐻", "🐶", "🐷"}

var config Config

const gitconfigTags = "git-pr.tags"

type Config struct {
	repoDir string // jj-workspace root when running inside a jj workspace; git toplevel otherwise
	gitDir  string // explicit GIT_DIR when running inside a jj workspace; empty otherwise

	git ConfigGit
	gh  ConfigGh
	bl  ConfigBranchless
	jj  ConfigJj

	tags    []string      // git config git-pr.<repo>.tags
	verbose bool          // flag
	timeout time.Duration // flag

	addLabels    []string // --add-label / config add_label: label(s) added to every non-skipped PR
	addTipLabels []string // --add-tip-label / config add_tip_label: label(s) added to the tip PR only

	includeOtherAuthors bool   // flag
	dryRun              bool   // flag: show what would be done without making changes
	stopAfter           string // flag: stop after specific phase
	output              string // --output / config output: resolved template for the PR list printed after push
	autoAccept          bool   // flag: assume "yes" to interactive prompts
	noStack             bool   // flag: skip creating/updating the native GitHub stack

	skipDraft     bool     // flag: skip draft commits by default
	includeDraft  bool     // flag: explicitly include draft commits (highest precedence)
	draftPatterns []string // wildcard patterns for draft detection (case-insensitive)

	commitRange ConfigRange // positional args: optional commit selection
}

// ConfigRange holds the user-supplied commit selection from positional args.
// HasArg=false means "use default origin/<trunk>..HEAD"; otherwise BaseRef and
// TipRef are resolved commit hashes (not symbolic refs).
//
// Selected is non-empty only for the multi-commit form (`git pr A B C`): it
// holds the resolved commit hashes the user named, possibly non-contiguous and
// in any order. In that case BaseRef/TipRef are left empty here and computed in
// main once the visible stack is known (the [lowest..highest] span); the
// in-span commits the user did NOT name are marked Skip during processing.
type ConfigRange struct {
	HasArg   bool
	BaseRef  string   // resolved 40-char commit hash for the base (exclusive)
	TipRef   string   // resolved 40-char commit hash for the tip (inclusive)
	Selected []string // multi-commit form: resolved hashes of the named commits
}

// IsMulti reports whether the user passed the multi-commit form (2+ individual
// commits) rather than the default, single-commit, or range form.
func (r ConfigRange) IsMulti() bool { return len(r.Selected) > 0 }

type ConfigGit struct {
	enabled bool
	user    string // git
	email   string // git

	localTrunk  string // main | trunk branch (optional)
	remoteTrunk string // main | trunk branch

	remote    string // origin
	remoteUrl string // git@github.com:org/repo.git | https://github.com/org/repo.git
	protocol  string // ssh | https
	host      string // github.com
	repo      string // org/repo
}

type ConfigGh struct {
	user  string // gh-cli
	token string // gh-cli
	host  string // github.com
	repo  string // org/repo
}

type ConfigBranchless struct {
	enabled bool
	version string
}

type ConfigJj struct {
	enabled bool
	version string
}

func LoadConfig() (config Config) {
	var addLabelFlag, addTipLabelFlag stringSliceFlag
	flagVersion := flag.Bool("version", false, "Show version information")
	flag.BoolVar(&config.verbose, "v", false, "Verbose output")
	flag.BoolVar(&config.includeOtherAuthors, "include-other-authors", false, "Create PRs for commits from other authors (default to false: skip)")
	flag.BoolVar(&config.dryRun, "dry-run", false, "Show what would be done without making changes")
	flag.StringVar(&config.stopAfter, "stop-after", "", "Stop after phase: validate|get-commits|rewrite|push|pr-create")
	flag.BoolVar(&config.skipDraft, "skip-draft", false, "Skip commits with [draft] in title")
	flag.BoolVar(&config.includeDraft, "include-draft", false, "Include draft commits (override config)")
	flag.BoolVar(&config.autoAccept, "yes", false, `Assume "yes" to prompts (for non-interactive use)`)
	flag.BoolVar(&config.autoAccept, "y", false, `Assume "yes" to prompts (shorthand for --yes)`)
	flag.BoolVar(&config.noStack, "no-stack", false, "Do not create/update a native GitHub stack on push")

	flagGitHubHosts := flag.String("gh-hosts", "~/.config/gh/hosts.yml", "Path to config.json")
	flagTimeout := flag.Int("timeout", 20, "API call timeout in seconds")
	flagSetTags := flag.String("default-tags", "", "Set default tags for the current repository (comma separated)")
	flagTags := flag.String("t", "", "Set tags for current stack, ignore default (comma separated)")
	flagOutput := flag.String("output", "", "Format of the PR list printed after push: url|url-title|markdown, or a template like '{url} {title}'")
	flagDraftPattern := flag.String("draft-pattern", "", "Wildcard pattern(s) for draft detection (default: wip:*,draft:*,*[wip]*,*[draft]*; comma-separated)")
	flag.Var(&addLabelFlag, "add-label", "Add a GitHub label to every PR in the stack (repeatable; overrides config add_label)")
	flag.Var(&addTipLabelFlag, "add-tip-label", "Add a GitHub label to the tip (top) PR only (repeatable; overrides config add_tip_label)")

	{ // parse flags
		usage := `Usage: git pr [OPTIONS] [COMMITS]

COMMITS may be:
  (omitted)        Push origin/<trunk>..HEAD as stacked PRs (default).
  BASE..TIP        Push the commits in the BASE..TIP range as stacked PRs.
  COMMIT           Push exactly that one commit as a single PR.
  COMMIT...        Push the named commits (2+) as stacked PRs; commits between
                   them on the stack are skipped. Args may be in any order.

A COMMIT may be a git ref/hash, or (in a jj repo) a jj change-id.`
		flag.Usage = func() {
			printf("%s\n", usage)
			flag.PrintDefaults()
		}
		flag.Parse()

		// handle version flag
		if *flagVersion {
			printf("git-pr version %s\n", version)
			os.Exit(0)
		}

		// check environment variables as fallback
		if !config.dryRun && os.Getenv("GIT_PR_DRY_RUN") == "1" {
			config.dryRun = true
		}
		if !config.autoAccept && os.Getenv("GIT_PR_YES") == "1" {
			config.autoAccept = true
		}
		if !config.noStack && os.Getenv("GIT_PR_NO_STACK") == "1" {
			config.noStack = true
		}
		if config.stopAfter == "" && os.Getenv("GIT_PR_STOP_AFTER") != "" {
			config.stopAfter = os.Getenv("GIT_PR_STOP_AFTER")
		}
		// --output is resolved at the end of LoadConfig, once the config file has
		// been read; fold the env value into the flag so that chain sees it.
		if *flagOutput == "" && os.Getenv("GIT_PR_OUTPUT") != "" {
			*flagOutput = os.Getenv("GIT_PR_OUTPUT")
		}
		validStopAfter := map[string]bool{
			"":            true,
			"validate":    true,
			"get-commits": true,
			"rewrite":     true,
			"push":        true,
			"pr-create":   true,
		}
		if !validStopAfter[config.stopAfter] {
			exitf("ERROR: invalid --stop-after %q; must be one of: validate, get-commits, rewrite, push, pr-create", config.stopAfter)
		}
		// environment variables for draft settings
		if !config.skipDraft && os.Getenv("GIT_PR_SKIP_DRAFT") == "1" {
			config.skipDraft = true
		}
		if !config.includeDraft && os.Getenv("GIT_PR_INCLUDE_DRAFT") == "1" {
			config.includeDraft = true
		}

		// configs from flags
		config.timeout = time.Duration(*flagTimeout) * time.Second
		if *flagSetTags != "" {
			tags := saveGitPRConfig(*flagSetTags)
			printf("Set default tags: %v\n", strings.Join(tags, ", "))
			os.Exit(0)
		}
		config.tags = getGitPRConfig()
		if *flagTags != "" {
			config.tags = parseCommaList(*flagTags) // override default tags
		}

		// read git config for skipDraft setting
		if !config.skipDraft {
			skipDraftStr, _ := getGitConfig("git-pr.skipDraft")
			if skipDraftStr == "true" || skipDraftStr == "1" {
				config.skipDraft = true
			}
		}

		// determine draft pattern (precedence: flag > git config > default)
		patternStr := *flagDraftPattern
		if patternStr == "" {
			patternStr, _ = getGitConfig("git-pr.draftPattern")
		}
		if patternStr == "" {
			patternStr = `wip:*,draft:*,*[wip]*,*[draft]*` // default: wip/draft prefix or bracketed
		}

		// parse comma-separated patterns and validate each
		for _, p := range parseCommaList(patternStr) {
			if err := validateWildcardPattern(p); err != nil {
				exitf(`ERROR: invalid wildcard pattern %q: %v

The pattern must be a valid wildcard pattern.
Supported wildcards:
  *          - matches any characters
  ?          - matches exactly one character

Example patterns:
  wip:*,draft:*,*[wip]*,*[draft]*  - default patterns (case-insensitive)
  *[draft]*,*[wip]*                - contains [draft] OR [wip]
  wip:*                            - starts with "wip:"
  *-draft-*                        - contains "-draft-"

Note: Character ranges like [a-z] are NOT supported.
`, p, err)
			}
			config.draftPatterns = append(config.draftPatterns, p)
		}
	}
	{ // detect repository by git, with jj-workspace fallback
		// note: detection-phase calls below run before the package-level
		// config is populated, so verbose logging is suppressed for them.
		errMsg := `
git-pr is a tool for submitting git commits as GitHub stacked pull requests (stacked PRs).

ERROR: You need to run it in a git repository with remote configured.

For more information, see "git-pr --help".`

		if output, err := git("rev-parse", "--show-toplevel"); err == nil {
			config.repoDir = strings.TrimSpace(output)
		} else if root, jerr := jj("git", "root"); jerr == nil {
			// jj workspace: no .git here, but jj knows where the backing one is
			config.gitDir = strings.TrimSpace(root)
			wsRoot, werr := jj("workspace", "root")
			if werr != nil {
				exitf("ERROR: detected jj workspace but failed to resolve workspace root: %v", werr)
			}
			config.repoDir = strings.TrimSpace(wsRoot)
			// export GIT_DIR so every subsequent git subprocess picks it up.
			// the named-return `config` here shadows the package-level var, so
			// per-command env injection in execCmd wouldn't see this field until
			// LoadConfig returns. process-env propagation avoids that ordering trap.
			if err := os.Setenv("GIT_DIR", config.gitDir); err != nil {
				exitf("ERROR: failed to set GIT_DIR: %v", err)
			}
			debugf("detected jj workspace: repoDir=%s gitDir=%s", config.repoDir, config.gitDir)
		} else {
			exitf(errMsg)
		}
		config.git.enabled = true

		// find remote url (push)
		// TODO: support multiple remotes
		out, err := git("remote", "-v")
		if err != nil {
			exitf(errMsg)
		}
		func() {
			line := out // find the line with "(push)"
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(l, "(push)") {
					line = l
					break
				}
			}

			// git@<host>:<user>/<repo>.git
			regexpURL := regexp.MustCompile(`(\w+)\s+(git@([^:\s]+):([^/\s]+)/([^.\s]+)(\.git)?)`)
			matches := regexpURL.FindStringSubmatch(line)
			if len(matches) > 0 {
				config.git.protocol = "ssh"
				config.git.remote = matches[1]
				config.git.remoteUrl = matches[2]
				config.git.host = matches[3]
				config.git.repo = matches[4] + "/" + matches[5]
				return
			}

			// https://<host>/<user>/<repo>.git
			regexpURL = regexp.MustCompile(`(\w+)\s+(https://([^/\s]+)/([^/\s]+)/([^.\s]+)(\.git)?)`)
			matches = regexpURL.FindStringSubmatch(line)
			if len(matches) > 0 {
				config.git.protocol = "https"
				config.git.remote = matches[1]
				config.git.remoteUrl = matches[2]
				config.git.host = matches[3]
				config.git.repo = matches[4] + "/" + matches[5]
				return
			}

			exitf(`
ERROR: failed to parse remote url:
  expect git@<host>:<user>/<repo> or https://<host>/<user>/<repo>
  got %q`, out)
		}()
	}
	{ // detect remote trunk branch
		out, err := git("symbolic-ref", "--short", fmt.Sprintf("refs/remotes/%v/HEAD", config.git.remote))
		if err != nil {
			exitf("ERROR: failed to detect remote trunk branch")
		}
		remoteTrunk := strings.TrimPrefix(out, config.git.remote+"/")
		if remoteTrunk == "" {
			exitf("ERROR: failed to detect remote trunk branch")
		}
		config.git.remoteTrunk = remoteTrunk
		config.git.localTrunk = config.git.remoteTrunk
	}
	{ // get git username and email
		user, err := getGitConfig("user.name")
		if err != nil || user == "" {
			exitf("ERROR: user.name not found in git config")
		}
		email, err := getGitConfig("user.email")
		if err != nil || email == "" {
			exitf("ERROR: user.email not found in git config")
		}
		config.git.user = user
		config.git.email = email
	}
	{ // detect jj
		if _, err := os.Stat(config.repoDir + "/.jj"); err == nil {
			version, err := jj("version")
			if err == nil {
				config.jj.enabled = true
				config.jj.version = strings.TrimPrefix(version, "jj ")
				debugf("detected jj %s", config.jj.version)
			}
		}
	}
	{ // detect git-branchless
		version, err := git("branchless", "--version")
		if err == nil {
			config.bl.enabled = true
			config.bl.version = strings.TrimSpace(version)
			debugf("detected git-branchless %s", config.bl.version)
		}
	}
	{ // parse github config
		ghHosts, err := LoadGitHubConfig(*flagGitHubHosts)
		if err != nil {
			exitf(`
ERROR: failed to load GitHub config at %v: %v
		
Hint: Install github client and login with your account
      https://github.com/cli/cli#installation
Then:
      gh auth login
`, *flagGitHubHosts, err)
		}

		ghHost := ghHosts[config.git.host]
		if ghHost == nil {
			exitf(`
ERROR: no GitHub config for host %v

Hint: Check your ~/.config/gh/hosts.yml
Run the following command and choose your github host:

      gh auth login
`, config.git.host)
			return
		}

		config.gh.user = ghHost.User
		config.gh.token = ghHost.OauthToken

		if config.gh.token == "" { // try getting from keyring
			key := "gh:" + config.git.host
			config.gh.token, _ = keyring.Get(key, "")
		}
		if config.gh.token == "" {
			exitf(`ERROR: no GitHub token found for host %q

Hint: use github cli to login to your account:

      gh auth login
`, config.git.host)
		}
	}

	{ // parse positional args for commit range selection
		// done after repo/jj detection so `git rev-parse` works inside jj
		// workspaces (which need GIT_DIR set above).
		args := flag.Args()
		switch {
		case len(args) == 0:
			// default behavior, no positional args

		case len(args) == 1 && strings.Contains(args[0], ".."):
			arg := args[0]
			if strings.Contains(arg, "...") {
				exitf("ERROR: symmetric-difference range %q is not supported; use BASE..TIP instead", arg)
			}
			parts := strings.SplitN(arg, "..", 2)
			if parts[0] == "" || parts[1] == "" {
				exitf("ERROR: invalid range %q; expected BASE..TIP with both endpoints non-empty", arg)
			}
			baseHash, err := resolveCommitArg(parts[0], config.jj.enabled)
			if err != nil {
				exitf("ERROR: failed to resolve base ref %q: %v", parts[0], err)
			}
			tipHash, err := resolveCommitArg(parts[1], config.jj.enabled)
			if err != nil {
				exitf("ERROR: failed to resolve tip ref %q: %v", parts[1], err)
			}
			config.commitRange = ConfigRange{HasArg: true, BaseRef: baseHash, TipRef: tipHash}

		case len(args) == 1:
			arg := args[0]
			tipHash, err := resolveCommitArg(arg, config.jj.enabled)
			if err != nil {
				exitf("ERROR: failed to resolve commit %q: %v", arg, err)
			}
			baseHash, err := resolveRef(tipHash + "^")
			if err != nil {
				exitf("ERROR: failed to resolve parent of %q: %v", arg, err)
			}
			config.commitRange = ConfigRange{HasArg: true, BaseRef: baseHash, TipRef: tipHash}

		default:
			// multi-commit form: 2+ individual commits, possibly non-contiguous
			// and in any order. resolve each to a hash here; the [lowest..highest]
			// span and the in-span skips are computed in main against the stack.
			var selected []string
			seen := map[string]bool{}
			for _, arg := range args {
				if strings.Contains(arg, "..") {
					exitf("ERROR: cannot combine a range %q with other commit arguments;\n"+
						"pass either a single BASE..TIP range or a list of individual commits", arg)
				}
				hash, err := resolveCommitArg(arg, config.jj.enabled)
				if err != nil {
					exitf("ERROR: failed to resolve commit %q: %v", arg, err)
				}
				if seen[hash] {
					continue // dedup repeated args / change-ids that map to the same commit
				}
				seen[hash] = true
				selected = append(selected, hash)
			}
			config.commitRange = ConfigRange{HasArg: true, Selected: selected}
		}
	}

	config.gh.host = config.git.host // assume github.com
	config.gh.repo = config.git.repo // assume org/repo

	// Label config: file provides defaults, CLI flags override. File discovery
	// walks up from the cwd to config.repoDir (the repo boundary), so a submodule
	// never inherits its parent repo's config. See configfile.go.
	{
		wd, _ := os.Getwd()
		fc := loadFileConfig(wd, config.repoDir)
		config.addLabels = fc.AddLabel
		if len(addLabelFlag) > 0 {
			config.addLabels = addLabelFlag
		}
		config.addTipLabels = fc.AddTipLabel
		if len(addTipLabelFlag) > 0 {
			config.addTipLabels = addTipLabelFlag
		}

		// output format precedence: flag > env > config file > default preset
		tmpl, err := resolveOutputFormat(coalesce(*flagOutput, fc.Output))
		if err != nil {
			exitf("ERROR: %v", err)
		}
		config.output = tmpl
	}
	return config
}

// resolveRef resolves a git ref string (symbolic name, hash, HEAD~N, etc.) to
// a full 40-char commit hash. `^{commit}` peels tags and rejects non-commit
// objects.
func resolveRef(ref string) (string, error) {
	out, err := git("rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// resolveCommitArg resolves a user-supplied positional commit argument to a full
// 40-char git commit hash. In jj repos it first tries to resolve the argument as
// a jj revision, so jj change-ids (e.g. "yrvptmm") and other revsets work, and
// falls back to git resolution for things jj does not recognize (raw git refs,
// HEAD~N, tags). Outside jj it resolves via git only.
//
// jjEnabled is passed explicitly rather than read from the package-level config:
// this is called from within LoadConfig, whose named return shadows the global
// config, so config.jj.enabled is not yet visible here (see config.go detection
// block for the same shadowing trap).
func resolveCommitArg(arg string, jjEnabled bool) (string, error) {
	if jjEnabled {
		if hash, err := resolveJJRevision(arg); err == nil {
			return hash, nil
		}
	}
	return resolveRef(arg)
}

// resolveJJRevision resolves a single jj revision (change-id, commit-id,
// bookmark, etc.) to its git commit hash. It errors unless the revision matches
// exactly one commit, so an ambiguous change-id prefix is rejected rather than
// silently picking one.
func resolveJJRevision(rev string) (string, error) {
	out, err := jj("log", "-r", rev, "--no-graph", "-T", `commit_id ++ "\n"`)
	if err != nil {
		return "", err
	}
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	switch len(lines) {
	case 0:
		return "", errorf("jj revision %q matched no commits", rev)
	case 1:
		return lines[0], nil
	default:
		return "", errorf("jj revision %q matched %d commits; expected exactly one", rev, len(lines))
	}
}

type GitHubConfigHostsFile map[string]*GitHubConfigHost

type GitHubConfigHost struct {
	User        string `yaml:"user"`
	OauthToken  string `yaml:"oauth_token"`
	GitProtocol string `yaml:"git_protocol"`
}

func LoadGitHubConfig(configPath string) (out GitHubConfigHostsFile, _ error) {
	configPath = expandPath(configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(data, &out)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func getGitConfig(name string) (string, error) {
	out, err := git("config", "--get", name)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	if path[0] == '~' {
		return os.Getenv("HOME") + path[1:]
	}
	return path
}

func getGitPRConfig() []string {
	rawTags, _ := git("config", "--get", gitconfigTags)
	return parseCommaList(rawTags)
}

func saveGitPRConfig(rawTags string) []string {
	tags := parseCommaList(rawTags)
	_, _ = git("config", "--unset-all", gitconfigTags)
	must(git("config", "--add", gitconfigTags, strings.Join(tags, ",")))
	return tags
}

// validateWildcardPattern checks if pattern contains only valid characters
// Valid: alphanumeric, spaces, common punctuation, *, and ?
// Invalid: syntax that looks like character classes or ranges
func validateWildcardPattern(pattern string) error {
	// pattern is valid if it doesn't look like it's trying to use unsupported features
	// we're being permissive here - just checking it's not empty
	if pattern == "" {
		return fmt.Errorf("pattern cannot be empty")
	}
	return nil
}

// matchWildcard checks if text matches a wildcard pattern (case-insensitive)
// Supports only * (any chars) and ? (one char), no ranges or character classes
// Returns true if pattern matches the text
func matchWildcard(pattern, text string) bool {
	// convert both to lowercase for case-insensitive matching
	pattern = strings.ToLower(pattern)
	text = strings.ToLower(text)

	return matchWildcardImpl(pattern, text)
}

// matchWildcardImpl implements simple wildcard matching with * and ?
// * matches zero or more characters
// ? matches exactly one character
func matchWildcardImpl(pattern, text string) bool {
	pIdx, tIdx := 0, 0
	pLen, tLen := len(pattern), len(text)
	starIdx, matchIdx := -1, 0

	for tIdx < tLen {
		// characters match or pattern has ?
		if pIdx < pLen && (pattern[pIdx] == '?' || pattern[pIdx] == text[tIdx]) {
			pIdx++
			tIdx++
		} else if pIdx < pLen && pattern[pIdx] == '*' {
			// found *, record position and try to match rest
			starIdx = pIdx
			matchIdx = tIdx
			pIdx++
		} else if starIdx != -1 {
			// no match, but we have a * before, backtrack
			pIdx = starIdx + 1
			matchIdx++
			tIdx = matchIdx
		} else {
			// no match and no * to backtrack
			return false
		}
	}

	// consume remaining * in pattern
	for pIdx < pLen && pattern[pIdx] == '*' {
		pIdx++
	}

	return pIdx == pLen
}

// matchAnyPattern checks if text matches any of the patterns
func matchAnyPattern(patterns []string, text string) bool {
	for _, pattern := range patterns {
		if matchWildcard(pattern, text) {
			return true
		}
	}
	return false
}
