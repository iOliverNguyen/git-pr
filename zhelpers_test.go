package main

import (
	"testing"
)

type T interface {
	Log(args ...any)
	Logf(format string, args ...any)
	Error(args ...any)
	Errorf(format string, args ...any)
	Fatal(args ...any)
	Fatalf(format string, args ...any)
}

type successT struct{ t *testing.T }

func (e *successT) Log(args ...any) {
	e.t.Helper()
	e.t.Log(args...)
}
func (e *successT) Logf(format string, args ...any) {
	e.t.Helper()
	e.t.Logf(format, args...)
}
func (e *successT) Error(args ...any) {
	e.t.Helper()
	// no error
}
func (e *successT) Errorf(format string, args ...any) {
	e.t.Helper()
	// no error
}
func (e *successT) Fatal(args ...any) {
	e.t.Helper()
	// no fatal
}
func (e *successT) Fatalf(format string, args ...any) {
	e.t.Helper()
	// no fatal
}

func assert(t *testing.T, condition bool) T {
	if condition {
		return &successT{t}
	} else {
		return t
	}
}
