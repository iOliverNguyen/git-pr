package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type NewPRBody struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}
type PR struct {
	Number int    `json:"number"`
	Body   string `json:"body"`
	Head   struct {
		Ref string `json:"ref"`
	} `json:"head"`
	UpdatedAt *time.Time
}

func githubGetPRNumberForCommit(commit, prev *Commit) (int, error) {
	if commit.PRNumber != 0 {
		return commit.PRNumber, nil
	}
	ghURL := fmt.Sprintf("https://api.%v/repos/%v/commits/%v/pulls?per_page=100", config.git.host, config.git.repo, commit.Hash)
	jsonBody, err := httpGET(ghURL)
	switch {
	case err != nil && strings.Contains(err.Error(), "No commit found"):
		return githubSearchPRNumberForCommit(commit)
	case err != nil:
		return 0, err
	}

	var out []PR
	err = json.Unmarshal(jsonBody, &out)
	if err != nil {
		return 0, errorf("failed to parse request body: %v", err)
	}

	remoteRef := commit.GetRemoteRef()
	if remoteRef != "" {
		for _, pr := range out {
			if pr.Head.Ref == remoteRef {
				return pr.Number, nil
			}
		}
	}
	if commit.Skip {
		return githubSearchPRNumberForCommit(commit)
	}

	// the commit was pushed and got "Everything up-to-date", try creating new pr
	err = githubCreatePRForCommit(commit, prev)
	if err != nil {
		return 0, err
	}
	return commit.PRNumber, nil
}

func githubGetPRByNumber(number int) (*PR, error) {
	ghURL := fmt.Sprintf("https://api.%v/repos/%v/pulls/%d", config.git.host, config.git.repo, number)
	jsonBody, err := httpGET(ghURL)
	if err != nil {
		return nil, err
	}

	var out PR
	err = json.Unmarshal(jsonBody, &out)
	if err != nil {
		return nil, errorf("failed to parse request body: %v", err)
	}

	return &out, nil
}

func githubCreatePRForCommit(commit *Commit, prev *Commit) error {
	base := config.git.remoteTrunk
	if prev != nil {
		base = prev.GetRemoteRef()
	}
	args := []string{"pr", "create", "--title", commit.Title, "--body", "", "--head", commit.GetRemoteRef(), "--base", base}
	if tags := commit.GetTags(config.tags...); len(tags) > 0 {
		args = append(args, "--label", strings.Join(tags, ","))
	}
	fmt.Printf("create pull request for %q\n", commit.Title)
	_, err := gh(args...)
	return err
}

func githubPRUpdateBaseForCommit(commit *Commit, prev *Commit) error {
	base := xif(prev != nil, prev.GetRemoteRef(), config.git.remoteTrunk)
	prNumber := must(githubGetPRNumberForCommit(commit, prev))
	_, err := gh("pr", "edit", strconv.Itoa(prNumber), "--base", base)
	return err
}

var regexpNumber = regexp.MustCompile(`[0-9]+`)

func githubSearchPRNumberForCommit(commit *Commit) (int, error) {
	query := fmt.Sprintf("in:title %v", commit.Title)
	result, err := gh("pr", "list", "--limit=1", "--search", query)
	if err != nil {
		debugf("failed to search PR for commit (ignored) %q: %v\n", commit.Title, err)
		return 0, nil
	}
	s := regexpNumber.FindString(result)
	if s == "" {
		return 0, nil
	}
	return must(strconv.Atoi(s)), nil
}
