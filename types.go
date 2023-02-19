package main

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

type KeyVal [2]string

type Commit struct {
	Hash        string
	Date        time.Time
	AuthorName  string
	AuthorEmail string
	Title       string
	Message     string
	Attrs       []KeyVal

	PRNumber int
}

func (commit *Commit) String() string {
	remoteRef := commit.GetAttr(KeyRemoteRef)
	if remoteRef != "" {
		remoteRef = fmt.Sprintf("(%v)", remoteRef)
	}
	return fmt.Sprintf("%v %v %v", commit.ShortHash(), remoteRef, commit.Title)
}

func (commit *Commit) Format(s fmt.State, verb rune) {
	switch verb {
	case 'v':
		if s.Flag('+') {
			fprintf(s, "commit %v\nAuthor: %v <%v>\nDate: %v\n\n%v\n\n%v\n", commit.Hash, commit.AuthorName, commit.AuthorEmail, commit.Date, commit.Title, commit.Message)
			return
		}
		fallthrough
	case 's', 'q':
		fprint(s, commit.String())
	}
}

func (commit *Commit) ShortHash() string {
	return commit.Hash[:8]
}

func (commit *Commit) GetAttr(key string) string {
	for _, kv := range commit.Attrs {
		if kv[0] == key {
			return kv[1]
		}
	}
	return ""
}

func (commit *Commit) SetAttr(key, value string) {
	for i, kv := range commit.Attrs {
		if kv[0] == key {
			commit.Attrs[i][1] = value
			return
		}
	}
	commit.Attrs = append(commit.Attrs, KeyVal{key, value})
	sort.Slice(commit.Attrs, func(i, j int) bool {
		return commit.Attrs[i][0] < commit.Attrs[j][0]
	})
}

func (commit *Commit) FullMessage() string {
	var b strings.Builder
	fprint(&b, commit.Title, "\n\n", commit.Message, "\n\n")
	for _, kv := range commit.Attrs {
		fprintf(&b, "%v: %v\n", formatKey(kv[0]), kv[1])
	}
	return strings.TrimSpace(b.String())
}

type CommitList []*Commit

func (list CommitList) ByHash(hash string) *Commit {
	_, commit := list.FindHash(hash)
	return commit
}

func (list CommitList) FindHash(hash string) (index int, commit *Commit) {
	if len(hash) < 8 {
		panic("invalid hash")
	}
	for i, item := range list {
		if strings.HasPrefix(item.Hash, hash) {
			return i, item
		}
	}
	return -1, nil
}

func (list CommitList) LatestCommitByAuthor(email string) *Commit {
	for _, item := range list {
		if item.AuthorEmail == email {
			return item
		}
	}
	return nil
}
