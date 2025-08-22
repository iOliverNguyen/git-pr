package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

var (
	regexpCommitHash = regexp.MustCompile(`^commit ([0-9a-f]{40})$`)
	regexpAuthor     = regexp.MustCompile(`^Author: (.*) <(.*)>$`)
	regexpDate       = regexp.MustCompile(`^Date:   (.*)$`)
	regexpKeyVal     = regexp.MustCompile(`^\s+([a-zA-Z0-9-]+):(.*)$`)
	dateLayouts      = []string{"Mon Jan _2 15:04:05 2006 -0700", "2006-01-02 15:04:05 -0700"}
)

func gitLogs(size int, extra ...string) (string, error) {
	args := []string{"log", fmt.Sprintf("-%v", size)}
	args = append(args, extra...)
	return git(args...)
}

func parseLogs(logs string) (out CommitList, _ error) {
	if strings.TrimSpace(logs) == "" {
		return nil, nil
	}
	lines := strings.Split(logs, "\n")
	part := []string{}
	for _, line := range lines {
		if m := regexpCommitHash.FindStringSubmatch(line); m != nil {
			if len(part) > 0 {
				item, err := parseLogsCommit(part)
				if err != nil {
					return nil, err
				}
				out = append(out, item)
			}
			part = part[:0]
		}
		part = append(part, line)
	}
	item, err := parseLogsCommit(part)
	if err != nil {
		return nil, err
	}
	out = append(out, item)
	return out, err
}

func parseLogsCommit(lines []string) (*Commit, error) {
	if len(lines) == 0 {
		return nil, nil
	}
	backup := lines
	out := &Commit{}
	// parse header
	bodyStart := 0
	for i, line := range lines {
		if line == "" {
			bodyStart = i + 1
			break
		}
		if m := regexpCommitHash.FindStringSubmatch(line); m != nil {
			out.Hash = m[1]
		}
		if m := regexpAuthor.FindStringSubmatch(line); m != nil {
			out.AuthorName = m[1]
			out.AuthorEmail = m[2]
		}
		if m := regexpDate.FindStringSubmatch(line); m != nil {
			var date time.Time
			var err error
			for _, layout := range dateLayouts {
				date, err = time.Parse(layout, m[1])
				if err == nil {
					break
				}
			}
			if err != nil {
				panicf(nil, "failed to parse time from %q", m[1])
			}
			out.Date = date.UTC()
		}
	}
	// truncate empty lines
	bodyEnd := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			bodyEnd = i + 1
			break
		}
	}
	lines = lines[bodyStart:bodyEnd]
	// parse footer
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		if strings.TrimSpace(line) == "" {
			continue
		}
		if m := regexpKeyVal.FindStringSubmatch(line); m != nil {
			key, val := strings.ToLower(m[1]), strings.TrimSpace(m[2])
			out.Attrs = append(out.Attrs, KeyVal{key, val})
		} else {
			bodyEnd = i + 1
			break
		}
	}
	// parse body
	out.Title, out.Message = parseBody(lines[:bodyEnd])
	// validate
	if out.Hash == "" || out.AuthorName == "" || out.AuthorEmail == "" || out.Title == "" {
		panicf(nil, "failed to parse commit with log:\n%v", strings.Join(backup, "\n"))
	}
	return out, nil
}

func parseBody(lines []string) (string, string) {
	if len(lines) == 0 {
		return "", ""
	}
	title := strings.TrimSpace(lines[0])
	var b strings.Builder
	for _, line := range lines[1:] {
		b.WriteString(strings.TrimPrefix(line, "    "))
		b.WriteByte('\n')
	}
	return title, strings.TrimSpace(b.String())
}

func getStackedCommits(base, target string) ([]*Commit, error) {
	logs, err := gitLogs(100, fmt.Sprintf("%v..%v", base, target))
	if err != nil {
		return nil, wrapf(err, "failed to find common ancestor for %v and %v", base, target)
	}
	list, err := parseLogs(logs)
	if err != nil {
		return nil, err
	}
	// sort from oldest to newest
	return revert(list), nil
}

func deleteBranch(branch string) error {
	branches, err := git("branch")
	if err != nil {
		return err
	}
	if strings.Contains(branches, branch+"\n") {
		_, err = git("branch", "-D", branch) // delete branch
	}
	return err
}
