package main

import "testing"

func TestFormatKey(t *testing.T) {
	out := formatKey("remote-ref")
	if out != "Remote-Ref" {
		t.Errorf("formatKey() = %v, want %v", out, "Remote-Ref")
	}
}
