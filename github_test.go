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
