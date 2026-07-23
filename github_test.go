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

func TestIsStackWouldRemove(t *testing.T) {
	cases := []struct {
		name string
		out  string
		err  error
		want bool
	}{
		{"nil", "", nil, false},
		{
			// the real shape gh-stack prints when link would drop a PR.
			"would remove",
			"✗ Cannot update stack: this would remove #22054 from the stack",
			&execError{exitCode: 5, output: "✗ Cannot update stack: this would remove #22054 from the stack"},
			true,
		},
		{"unrelated error", "", &execError{exitCode: 1, output: "network timeout"}, false},
		{"would-remove text but no error", "this would remove #1 from the stack", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStackWouldRemove(tc.out, tc.err); got != tc.want {
				t.Errorf("isStackWouldRemove(%q, %v) = %v, want %v", tc.out, tc.err, got, tc.want)
			}
		})
	}
}
