package main

import (
	"errors"
	"fmt"
	"io"
	"os"
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
