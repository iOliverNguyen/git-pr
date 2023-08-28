package main

import (
	"flag"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"regexp"
	"strings"
	"time"
)

var config Config

type Config struct {
	Repo       string // git
	Remote     string // flag
	MainBranch string // flag

	Host  string // git
	User  string // gh-cli
	Token string // gh-cli
	Email string // git config user.email

	IncludeOtherAuthors bool // flag

	Verbose bool          // flag
	Timeout time.Duration // flag
}

func LoadConfig() (config Config) {
	flag.BoolVar(&config.Verbose, "v", false, "Verbose output")
	flag.StringVar(&config.Remote, "remote", "origin", "Remote name")
	flag.StringVar(&config.MainBranch, "main", "main", "Main branch name")
	flag.BoolVar(&config.IncludeOtherAuthors, "include-other-authors", false, "Include commits from other authors (default to false: skip)")

	flagGitHubHosts := flag.String("gh-hosts", "~/.config/gh/hosts.yml", "Path to config.json")
	flagTimeout := flag.Int("timeout", 20, "API call timeout in seconds")

	// parse flags
	usage := "Usage: git pr [options]"
	flag.Usage = func() {
		fmt.Println(usage)
		flag.PrintDefaults()
	}
	flag.Parse()
	config.Timeout = time.Duration(*flagTimeout) * time.Second

	// detect repository
	out, err := execGit("remote", "show", config.Remote)
	if err != nil {
		exitf("not a git repository")
	}
	regexpURL := regexp.MustCompile(`git@([^:]+):([^/]+)/(.+)\.git`)
	matches := regexpURL.FindStringSubmatch(out)
	if len(matches) != 4 {
		exitf("failed to parse remote url")
	}
	config.Host = matches[1]
	config.Repo = matches[2] + "/" + matches[3]

	// parse github config
	ghHosts, err := LoadGitHubConfig(*flagGitHubHosts)
	if err != nil {
		fmt.Printf("failed to load GitHub config at %v: %v\n", *flagGitHubHosts, err)
		fmt.Printf(`
Hint: Install github client and login with your account
      https://cli.github.com/manual/installation
`)
		os.Exit(1)
	}
	ghHost := ghHosts[config.Host]
	if ghHost == nil {
		fmt.Printf("no GitHub config for host %v\n", config.Host)
		fmt.Print(`
Hint: Add host to ~/.config/gh/hosts.yml
`)
		os.Exit(1)
	}
	config.User = ghHost.User
	config.Token = ghHost.OauthToken
	config.Email = must(getGitConfig("user.email"))
	validateConfig("user", config.User)
	validateConfig("token", config.Token)
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
