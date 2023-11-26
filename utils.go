package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

func fprint(w io.Writer, args ...any) {
	_, err := fmt.Fprint(w, args...)
	if err != nil {
		panic(err)
	}
}

func fprintf(w io.Writer, format string, args ...any) {
	_, err := fmt.Fprintf(w, format, args...)
	if err != nil {
		panic(err)
	}
}

func errorf(msg string, args ...any) error {
	return errors.New(fmt.Sprintf(msg, args...))
}

func wrapf(err error, msg string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%v: %v", fmt.Sprintf(msg, args...), err)
}

func debugf(msg string, args ...any) {
	if config.Verbose {
		fmt.Printf("[DEBUG] "+msg, args...)
	}
}

func exitf(msg string, args ...any) {
	fmt.Printf(msg+"\n", args...)
	os.Exit(1)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func panicf(err error, msg string, args ...any) {
	if err != nil {
		fmt.Println("ERROR: ", err)
	}

	panic(fmt.Sprintf(msg, args...))
}
func revert[T any](list []T) []T {
	out := make([]T, len(list))
	for i, v := range list {
		out[len(list)-i-1] = v
	}
	return out
}

func formatKey(key string) string {
	var b strings.Builder
	key = strings.ToLower(key)
	for i, word := range strings.Split(key, "-") {
		if i > 0 {
			b.WriteString("-")
		}
		if word == "" {
			continue
		}
		b.WriteString(strings.ToUpper(word[0:1]))
		b.WriteString(word[1:])
	}
	return b.String()
}

func maxAttrsLength(attrs []KeyVal) int {
	maxL := 0
	for _, item := range attrs {
		if len(item[0]) > maxL {
			maxL = len(item[0])
		}
	}
	return maxL
}

func execGit(args ...string) (string, error) {
	return execCommand("git", args...)
}

func execGh(args ...string) (string, error) {
	return execCommand("gh", args...)
}

func execCommand(name string, args ...string) (string, error) {
	if config.Verbose {
		fmt.Print(name, " ")
		for _, arg := range args {
			if strings.Contains(arg, " ") {
				fmt.Printf("%q", arg)
			} else {
				fmt.Print(arg, " ")
			}
		}
		fmt.Println()
	}
	stdout := bytes.Buffer{}
	cmd := exec.Command(name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stdout
	err := cmd.Run()
	if err != nil {
		fmt.Println(stdout.String())
	}
	return stdout.String(), err
}
