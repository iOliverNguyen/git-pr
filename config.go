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

var (
	emojis0 = []string{"♈️", "♉️", "♊️", "♋️", "♌️", "♍️", "♎️", "♏️", "♐️", "♑️", "♒️", "♓️"}
	emojis1 = []string{"🐹", "🐮", "🐯", "🦊", "🐲", "🐼", "🦁", "🐰", "🐵", "🐻", "🐶", "🐷"}
	emojis2 = []string{"🏠", "🏡", "🏘️", "🏚️", "🏢", "🏬", "🏣", "🏤", "🏥", "🏦", "🏨", "🏩", "🏪", "🏫", "🏭", "🏯", "🏰", "🏟️", "🏛️", "🏗️", "🌇", "🌆", "🌃", "🏙️"}
	emojis3 = []string{"🚗", "🚕", "🚆", "🚄", "🚅", "🚈", "🚇", "🚝", "🚋", "🚌", "🚎", "🏎️", "🚓", "🚑", "🚒", "🚚", "🚛", "🚜", "🏍️", "🛵", "🚲", "🛴"}
	emojis4 = []string{"🍏", "🍎", "🍐", "🍊", "🍋", "🍌", "🍉", "🍇", "🍓", "🍈", "🍒", "🍑", "🥭", "🍍", "🥥", "🥝", "🍅", "🍆", "🥑", "🥦", "🥬", "🥒", "🌶️", "🌽", "🥕", "🧄", "🧅", "🥔", "🍠", "🥐", "🥯", "🍞", "🥖", "🥨", "🧀", "🥚", "🍳", "🧈", "🥞", "🧇", "🥓", "🥩", "🍗", "🍖", "🦴", "🌭", "🍔", "🍟", "🍕", "🥪", "🥙", "🧆", "🌮", "🌯", "🥗", "🥘", "🥫", "🍝", "🍜", "🍲", "🍛", "🍣", "🍱", "🥟", "🦪", "🍤", "🍙", "🍚", "🍘", "🍥", "🥮", "🥠", "🍢", "🍡", "🍧", "🍨", "🍦", "🥧", "🧁", "🍰", "🎂", "🍮", "🍭", "🍬", "🍫", "🍿", "🍩", "🍪", "🌰", "🥜", "🍯", "🥛", "🍼", "☕", "🍵", "🧃", "🥤", "🍶", "🍺", "🍻"}
)

var (
	emojisx = emojis1 // config emojis
	config  Config
)

const gitconfigTags = "git-pr.tags"
const prDelimiterToGenerated = "[//]: # (BEGIN GIT-PR FOOTER)"

var prDelimiterRegexp = regexp.MustCompile(`\[//]:[^\n]+\bGIT-PR\b`)

type Config struct {
	repoDir string // git

	git ConfigGit
	gh  ConfigGh
	bl  ConfigBranchless
	jj  ConfigJj

	tags    []string      // git config git-pr.<repo>.tags
	verbose bool          // flag
	timeout time.Duration // flag

	includeOtherAuthors bool // flag
}

type ConfigGit struct {
	enabled bool
	user    string // git
	email   string // git

	localTrunk  string // main | trunk branch
	remoteTrunk string // main | trunk branch

	remote     string // origin
	remotePath string // git@github.com:org/repo.git | https://github.com/org/repo.git
	protocol   string // ssh | https
	host       string // github.com
	repo       string // org/repo
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
	flag.BoolVar(&config.verbose, "v", false, "Verbose output")
	flag.StringVar(&config.git.remote, "remote", "origin", "Remote name")
	flag.StringVar(&config.git.localTrunk, "main", "main", "Main branch name")
	flag.BoolVar(&config.includeOtherAuthors, "include-other-authors", false, "Create PRs for commits from other authors (default to false: skip)")

	flagGitHubHosts := flag.String("gh-hosts", "~/.config/gh/hosts.yml", "Path to config.json")
	flagTimeout := flag.Int("timeout", 20, "API call timeout in seconds")
	flagSetTags := flag.String("default-tags", "", "Set default tags for the current repository (comma separated)")
	flagTags := flag.String("t", "", "Set tags for current stack, ignore default (comma separated)")

	// parse flags
	usage := "Usage: git pr [options]"
	flag.Usage = func() {
		fmt.Println(usage)
		flag.PrintDefaults()
	}
	flag.Parse()

	// configs from flags
	config.timeout = time.Duration(*flagTimeout) * time.Second
	if *flagSetTags != "" {
		tags := saveGitPRConfig(strings.Split(*flagSetTags, ","))
		fmt.Printf("Set default tags: %v\n", strings.Join(tags, ", "))
		os.Exit(0)
	}
	config.tags = getGitPRConfig()
	if *flagTags != "" {
		config.tags = nil // override default tags
		tags := strings.Split(*flagTags, ",")
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				config.tags = append(config.tags, tag)
			}
		}
	}

	// detect repository
	out, err := _execCmd("git", "remote", "-v")
	if err != nil {
		exitf(`
git-pr is a tool for submitting git commits as GitHub stacked pull requests (stacked PRs).

ERROR: You need to run it in a git repository with remote configured.

For more information, see "git-pr --help".`)
	}

	regexpURL := regexp.MustCompile(`git@([^:\s]+):([^/\s]+)/([^.\s]+)(\.git)?`)
	matches := regexpURL.FindStringSubmatch(out)
	if matches == nil {
		// match https url
		regexpURL = regexp.MustCompile(`https://(github\.com)/([^/\s]+)\/([^.\s]+)(\.git)?`)
		matches = regexpURL.FindStringSubmatch(out)
		if matches == nil {
			exitf("failed to parse remote url: expect git@<host>:<user>/<repo> or https://github.com/<user>/<repo> (got %q)", out)
		}
	}
	config.git.host = matches[1]
	config.git.repo = matches[2] + "/" + matches[3]
	config.repoDir = must(findRepoDir())

	// parse github config
	ghHosts, err := LoadGitHubConfig(*flagGitHubHosts)
	if err != nil {
		exitf("failed to load GitHub config at %v: %v\n", *flagGitHubHosts, err)
		fmt.Printf(`
Hint: Install github client and login with your account
      https://github.com/cli/cli#installation
Then:
      gh auth login
`)
	}
	ghHost := ghHosts[config.git.host]
	if ghHost == nil {
		fmt.Printf("no GitHub config for host %v\n", config.git.host)
		fmt.Print(`
Hint: Check your ~/.config/gh/hosts.yml
Run the following command and choose your github host:

      gh auth login
`)
		os.Exit(1)
	}
	config.gh.user = ghHost.User
	config.gh.token = ghHost.OauthToken
	email, err := getGitConfig("user.email")
	if err != nil {
		fmt.Println("user.email not found in git config")
		os.Exit(1)
	}
	if email == "" {
		fmt.Println("user.email found in git config, but it's empty")
		os.Exit(1)
	}
	config.git.email = email
	if config.gh.token == "" { // try getting from keyring
		key := "gh:" + config.git.host
		config.gh.token, _ = keyring.Get(key, "")
	}
	if config.gh.token == "" {
		fmt.Printf("no GitHub token found for host %v\n", config.git.host)
		fmt.Print(`
Hint: use github cli to login to your account:

      gh auth login
`)
		os.Exit(1)
	}

	validateConfig("user", config.gh.user)
	validateConfig("email", config.git.email)
	return config
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

func validateConfig[T comparable](name string, value T) {
	var zero T
	if value == zero {
		exitf("missing config %q", name)
	}
}

func getGitPRConfig() (tags []string) {
	rawTags, _ := git("config", "--get", gitconfigTags)
	for _, tag := range strings.Split(rawTags, ",") {
		tag = strings.TrimSpace(tag)
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func saveGitPRConfig(tags []string) []string {
	var xtags []string
	for i := range tags {
		tag := strings.TrimSpace(tags[i])
		if tag != "" {
			xtags = append(xtags, tag)
		}
	}
	rawTags := strings.Join(xtags, ",")

	_, _ = git("config", "--unset-all", gitconfigTags)
	must(git("config", "--add", gitconfigTags, rawTags))
	return xtags
}
