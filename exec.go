package main

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

func git(args ...string) (string, error)  { return execCmd("git", args...) }
func gh(args ...string) (string, error)   { return execCmd("gh", args...) }
func jj(args ...string) (string, error)   { return execCmd("jj", args...) }
func _git(args ...string) (string, error) { return execCmd("git", args...) }
func _gh(args ...string) (string, error)  { return execCmd("gh", args...) }
func _jj(args ...string) (string, error)  { return execCmd("jj", args...) }

type execError struct {
	exitCode int
	err      error
	output   string
}

func (e *execError) Error() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("exit code %d", e.exitCode))
	if e.output != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(e.output))
	}
	return b.String()
}

func execCmd(name string, args ...string) (string, error) {
	if config.verbose {
		var b strings.Builder
		b.WriteString(name)
		for _, arg := range args {
			b.WriteString(" ")
			if strings.Contains(arg, " ") {
				b.WriteString(fmt.Sprintf("%q", arg))
			} else {
				b.WriteString(arg)
			}
		}
		debugf(b.String())
	}
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			err = &execError{exitCode: exitErr.ExitCode(), err: err, output: string(output)}
		} else {
			err = &execError{exitCode: 199, err: err, output: string(output)}
		}
	}
	if config.verbose {
		if err != nil {
			debugf(err.Error())
		} else {
			debugf(strings.TrimSpace(string(output)))
		}
	}
	return strings.TrimSpace(string(output)), err
}
