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

type Config struct {
	Repo       string // git
	Remote     string // flag
	MainBranch string // flag

	Host  string // git
	User  string // gh-cli
	Token string // gh-cli
	Email string // git config user.email

	Tags []string // git config git-pr.<repo>.tags

	IncludeOtherAuthors bool // flag

	Verbose bool          // flag
	Timeout time.Duration // flag
}

func LoadConfig() (config Config) {
	flag.BoolVar(&config.Verbose, "v", false, "Verbose output")
	flag.StringVar(&config.Remote, "remote", "origin", "Remote name")
	flag.StringVar(&config.MainBranch, "main", "main", "Main branch name")
	flag.BoolVar(&config.IncludeOtherAuthors, "include-other-authors", false, "Create PRs for commits from other authors (default to false: skip)")

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
	config.Timeout = time.Duration(*flagTimeout) * time.Second
	if *flagSetTags != "" {
		tags := saveGitPRConfig(strings.Split(*flagSetTags, ","))
		fmt.Printf("Set default tags: %v\n", strings.Join(tags, ", "))
		os.Exit(0)
	}
	config.Tags = getGitPRConfig()
	if *flagTags != "" {
		config.Tags = nil // override default tags
		tags := strings.Split(*flagTags, ",")
		for _, tag := range tags {
			tag = strings.TrimSpace(tag)
			if tag != "" {
				config.Tags = append(config.Tags, tag)
			}
		}
	}

	// detect repository
	out, err := execGit("remote", "show", config.Remote)
	if err != nil {
		exitf("not a git repository")
	}
	regexpURL := regexp.MustCompile(`git@([^:\s]+):([^/\s]+)/([^.\s]+)(\.git)?`)
	matches := regexpURL.FindStringSubmatch(out)
	if matches == nil {
		exitf("failed to parse remote url: expect git@<host>:<user>/<repo> (got %q)", out)
	}
	config.Host = matches[1]
	config.Repo = matches[2] + "/" + matches[3]

	// parse github config
	ghHosts, err := LoadGitHubConfig(*flagGitHubHosts)
	if err != nil {
		fmt.Printf("failed to load GitHub config at %v: %v\n", *flagGitHubHosts, err)
		fmt.Printf(`
Hint: Install github client and login with your account
      https://github.com/cli/cli#installation
Then:
      gh auth login
`)
		os.Exit(1)
	}
	ghHost := ghHosts[config.Host]
	if ghHost == nil {
		fmt.Printf("no GitHub config for host %v\n", config.Host)
		fmt.Print(`
Hint: Check your ~/.config/gh/hosts.yml
Run the following command and choose your github host:

      gh auth login
`)
		os.Exit(1)
	}
	config.User = ghHost.User
	config.Token = ghHost.OauthToken
	config.Email = must(getGitConfig("user.email"))
	if config.Token == "" { // try getting from keyring
		key := "gh:" + config.Host
		config.Token, _ = keyring.Get(key, "")
	}
	if config.Token == "" {
		fmt.Printf("no GitHub token found for host %v\n", config.Host)
		fmt.Print(`
Hint: use github cli to login to your account:

      gh auth login
`)
		os.Exit(1)
	}

	validateConfig("user", config.User)
	validateConfig("email", config.Email)
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
	out, err := execGit("config", "--get", name)
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
	rawTags, _ := execGit("config", "--get", gitconfigTags)
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

	_, _ = execGit("config", "--unset-all", gitconfigTags)
	must(execGit("config", "--add", gitconfigTags, rawTags))
	return xtags
}
