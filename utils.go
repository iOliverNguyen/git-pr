package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
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
	return fmt.Errorf(msg, args)
}

func wrapf(err error, msg string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%v: %v", fmt.Sprintf(msg, args...), err)
}

func debugf(msg string, args ...any) {
	if config.verbose {
		fmt.Printf("[DEBUG] "+msg, args...)
	}
}

func exitf(msg string, args ...any) {
	msg = trimPrefixNewline(msg) + "\n"
	fmt.Printf(msg, args...)
	os.Exit(1)
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(fmt.Sprintf("ERROR: %v", err))
	}
	return v
}

func panicf(err error, msg string, args ...any) {
	if err != nil {
		fmt.Println("ERROR: ", err)
	}
	panic("ERROR: " + fmt.Sprintf(msg, args...))
}

func xif[T any](cond bool, a, b T) T {
	if cond {
		return a
	}
	return b
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

func findRepoDir() (string, error) {
	output, err := _execCmd("git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", errors.New(output)
	}
	return strings.TrimSpace(output), nil
}

var rePrefixNewline = regexp.MustCompile(`^\n *`)

func trimPrefixNewline(s string) string {
	return rePrefixNewline.ReplaceAllString(s, "")
}
