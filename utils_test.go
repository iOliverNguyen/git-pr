package main

import (
	"reflect"
	"testing"
)

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
