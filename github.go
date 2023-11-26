package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

type NewPRBody struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Head  string `json:"head"`
	Base  string `json:"base"`
}

func githubGetLastPRNumber() (int, error) {
	type PR struct{}

	ghURL := fmt.Sprintf("https://api.%v/repos/%v/pulls?state=all&sort=created&direction=desc&per_page=1", config.Host, config.Repo)
	jsonBody, err := httpGET(ghURL)
	if err != nil {
		return 0, err
	}

	var out []PR
	err = json.Unmarshal(jsonBody, &out)
	if err != nil {
		return 0, errorf("failed to parse request body: %v", err)
	}
	if len(out) == 0 {
		return 0, nil
	} else {
		number := gjson.GetBytes(jsonBody, "0.number").Int()
		if number == 0 {
			return 0, errors.New("failed to find last pull request number")
		}
		return int(number), nil
	}
}

func githubGetPRNumberForCommit(commit, prev *Commit) (int, error) {
	type PR struct {
		Number int `json:"number"`
		Head   struct {
			Ref string `json:"ref"`
		} `json:"head"`
		UpdatedAt *time.Time
	}

	ghURL := fmt.Sprintf("https://api.%v/repos/%v/commits/%v/pulls?per_page=100", config.Host, config.Repo, commit.Hash)
	jsonBody, err := httpGET(ghURL)
	if err != nil && strings.Contains(err.Error(), "No commit found") {
		return githubSearchPRNumberForCommit(commit)
	}
	if err != nil {
		return 0, err
	}
	if err == nil {
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
	}

	// The commit was pushed and got "Everything up-to-date", try creating new pr
	err = githubCreatePRForCommit(commit, prev)
	if err != nil {
		return 0, err
	}
	return commit.PRNumber, nil
}

func githubCreatePRForCommit(commit *Commit, prev *Commit) error {
	base := config.MainBranch
	if prev != nil {
		base = prev.GetRemoteRef()
	}
	args := []string{"pr", "create", "--title", commit.Title, "--body", "", "--head", commit.GetRemoteRef(), "--base", base}
	if tags := commit.GetTags(config.Tags...); len(tags) > 0 {
		args = append(args, "--label", strings.Join(tags, ","))
	}
	_, err := execGh(args...)
	return err
}

var regexpNumber = regexp.MustCompile(`[0-9]+`)

func githubSearchPRNumberForCommit(commit *Commit) (int, error) {
	query := fmt.Sprintf("in:title %v", commit.Title)
	result, err := execGh("pr", "list", "--limit=1", "--search", query)
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
