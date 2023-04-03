package main

import (
	"encoding/json"
	"errors"
	"fmt"
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

func githubGetPRNumberForCommit(commit *Commit) (int, error) {
	type PR struct {
		Number int `json:"number"`
		Head   struct {
			Ref string `json:"ref"`
		} `json:"head"`
	}

	ghURL := fmt.Sprintf("https://api.%v/repos/%v/commits/%v/pulls?per_page=100", config.Host, config.Repo, commit.Hash)
	jsonBody, err := httpGET(ghURL)
	if err != nil {
		return 0, err
	}
	var out []PR
	err = json.Unmarshal(jsonBody, &out)
	if err != nil {
		return 0, errorf("failed to parse request body: %v", err)
	}
	remoteRef := commit.GetAttr(KeyRemoteRef)
	for _, pr := range out {
		if pr.Head.Ref == remoteRef {
			return pr.Number, nil
		}
	}
	// The commit was pushed and got "Everything up-to-date", try creating new pr
	err = githubCreatePRForCommit(commit)
	if err != nil {
		return 0, err
	}
	return commit.PRNumber, nil
}

func githubCreatePRForCommit(commit *Commit) error {
	// attempt to create new PR
	ghURL := fmt.Sprintf("https://api.%v/repos/%v/pulls", config.Host, config.Repo)
	body := NewPRBody{
		Title: commit.Title,
		Body:  commit.Message,
		Head:  commit.GetAttr(KeyRemoteRef),
		Base:  config.MainBranch,
	}
	fmt.Printf("create pull request for %q\n", commit.Title)
	jsonBody := must(httpPOST(ghURL, body))
	number := gjson.GetBytes(jsonBody, "number").Int()
	if number == 0 {
		return errorf("unexpected")
	}
	commit.PRNumber = int(number)
	time.Sleep(1 * time.Second)
	return nil
}
