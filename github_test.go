package main

import (
	"errors"
	"testing"
)

func TestIsBaseChangeBlockedByStack(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{
			// the real shape gh returns, wrapped by execCmd.
			"real gh error",
			&execError{exitCode: 1, output: "GraphQL: Cannot change the base branch because the pull request is part of a stack. (updatePullRequest)"},
			true,
		},
		{"plain wrapped", errors.New("something is part of a stack now"), true},
		{"unrelated exec error", &execError{exitCode: 1, output: "GraphQL: Could not resolve to a PullRequest"}, false},
		{"unrelated plain", errors.New("network timeout"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isBaseChangeBlockedByStack(tc.err); got != tc.want {
				t.Errorf("isBaseChangeBlockedByStack(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestIsGhStackMissing(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"nil", "", nil, false},
		{"extension missing", `unknown command "stack" for "gh"`, &execError{exitCode: 1, output: `unknown command "stack" for "gh"`}, true},
		{"unrelated error", "", &execError{exitCode: 1, output: "some other failure"}, false},
		{"missing text but no error", `unknown command "stack" for "gh"`, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isGhStackMissing(tc.out, tc.err); got != tc.want {
				t.Errorf("isGhStackMissing(%q, %v) = %v, want %v", tc.out, tc.err, got, tc.want)
			}
		})
	}
}

const (
	// the real shapes gh-stack prints when `link` cannot grow the existing stack.
	ghStackWouldRemove = "✗ Cannot update stack: this would remove #22054 from the stack"
	ghStackNotTop      = "✗ Cannot update stack: new PRs must be added to the top of the existing stack"
)

func TestIsStackNeedsRebuild(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"nil", "", nil, false},
		{
			"would remove",
			ghStackWouldRemove,
			&execError{exitCode: 5, output: ghStackWouldRemove},
			true,
		},
		{
			// a new PR belongs below the top of the stack (e.g. a new bottom commit).
			"must be added to the top",
			ghStackNotTop,
			&execError{exitCode: 5, output: ghStackNotTop},
			true,
		},
		{
			// the reason may only reach us via the error, not the captured output.
			"reason only in error",
			"",
			&execError{exitCode: 5, output: ghStackNotTop},
			true,
		},
		{"unrelated error", "", &execError{exitCode: 1, output: "network timeout"}, false},
		{"refusal text but no error", ghStackWouldRemove, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStackNeedsRebuild(tc.out, tc.err); got != tc.want {
				t.Errorf("isStackNeedsRebuild(%q, %v) = %v, want %v", tc.out, tc.err, got, tc.want)
			}
		})
	}
}

func TestIsStackPartiallyUnstacked(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"empty", "", false},
		{
			// gh stack unstack exits 0 but keeps the stack in this case.
			"queued for merge",
			"Some pull requests are queued for merge or have auto-merge enabled and\n" +
				"remain stacked on GitHub",
			true,
		},
		{"clean unstack", "Unstacked 4 pull requests", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStackPartiallyUnstacked(tc.out); got != tc.want {
				t.Errorf("isStackPartiallyUnstacked(%q) = %v, want %v", tc.out, got, tc.want)
			}
		})
	}
}

func TestGhStackReason(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want string
	}{
		{"empty", "", ""},
		{
			"strips marker",
			ghStackNotTop,
			"Cannot update stack: new PRs must be added to the top of the existing stack",
		},
		{
			// gh prints progress lines before and the stack listing after the refusal.
			"picks the refusal line out of surrounding output",
			"Checking existing stacks...\nLooking up PRs for 5 branches...\n" +
				ghStackWouldRemove + "\nCurrent stack: #22485, #22486",
			"Cannot update stack: this would remove #22054 from the stack",
		},
		{"no refusal line", "Checking existing stacks...\nnetwork timeout", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ghStackReason(tc.out); got != tc.want {
				t.Errorf("ghStackReason(%q) = %q, want %q", tc.out, got, tc.want)
			}
		})
	}
}
