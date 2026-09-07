package main

import (
	"os"
	"reflect"
	"testing"
)

func TestConfirm(t *testing.T) {
	// --yes/-y (config.autoAccept) short-circuits to true without touching stdin.
	t.Run("autoAccept short-circuits", func(t *testing.T) {
		defer func(v bool) { config.autoAccept = v }(config.autoAccept)
		config.autoAccept = true
		if !confirm("proceed?") {
			t.Errorf("confirm() with autoAccept = true; want true")
		}
	})

	// Non-interactive stdin (a pipe, not a char device) declines without
	// blocking on a read. Replace os.Stdin with a pipe to make this
	// deterministic regardless of how the test binary is invoked.
	t.Run("non-interactive declines", func(t *testing.T) {
		defer func(v bool) { config.autoAccept = v }(config.autoAccept)
		config.autoAccept = false

		r, w, err := os.Pipe()
		if err != nil {
			t.Fatalf("os.Pipe: %v", err)
		}
		defer func() { _ = r.Close() }()
		defer func() { _ = w.Close() }()
		defer func(s *os.File) { os.Stdin = s }(os.Stdin)
		os.Stdin = r

		if confirm("proceed?") {
			t.Errorf("confirm() on non-interactive stdin; want false")
		}
	})
}

func TestParseCommaList(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"only spaces", "   ", nil},
		{"only commas", ",,,", nil},
		{"single", "a", []string{"a"}},
		{"trim and drop empties", "a, , b ,c", []string{"a", "b", "c"}},
		{"trailing comma", "a,b,", []string{"a", "b"}},
		{"leading comma", ",a,b", []string{"a", "b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseCommaList(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseCommaList(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestFormatKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"remote-ref", "Remote-Ref"},
		{"", ""},
		{"single", "Single"},
		{"ALL-CAPS", "All-Caps"},
		{"a-b-c", "A-B-C"},
		{"trailing-", "Trailing-"},
		{"--double", "--Double"},
		{"MixedCase-key", "Mixedcase-Key"},
	}
	for _, tc := range cases {
		if got := formatKey(tc.in); got != tc.want {
			t.Errorf("formatKey(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
