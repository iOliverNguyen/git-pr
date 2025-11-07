package main

import (
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

var gray = "\033[38;5;245m"
var reset = "\033[0m"
var stdout = &wrapWriter{w: os.Stdout, last: '\n'}
var stderr = &wrapWriter{w: os.Stderr, last: '\n'}

type wrapWriter struct {
	w    io.Writer
	last byte
}

func (w *wrapWriter) ensureNewline() {
	if w.last != '\n' {
		_, _ = w.w.Write([]byte{'\n'})
		w.last = '\n'
	}
}

func (w *wrapWriter) Write(p []byte) (n int, err error) {
	if len(p) > 0 {
		// quick and dirty detection for color codes "\033[...m"
		if len(p) > 2 && p[0] == 27 && p[1] == '[' && p[len(p)-1] == 'm' {
			// do nothing
		} else {
			w.last = p[len(p)-1]
		}
	}
	return w.w.Write(p)
}

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

func printf(format string, args ...any) {
	stdout.ensureNewline()
	stderr.ensureNewline()
	fprintf(stdout, format, args...)
	stdout.ensureNewline()
}

func stderrf(format string, args ...any) {
	if len(args) > 0 {
		fprintf(stderr, format, args...)
	} else {
		fprint(stderr, format)
	}
}

func errorf(msg string, args ...any) error {
	return fmt.Errorf(msg, args...)
}

func wrapf(err error, msg string, args ...any) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%v: %w", fmt.Sprintf(msg, args...), err)
}

func debugf(msg string, args ...any) {
	if !config.verbose {
		return
	}
	if len(args) > 0 {
		msg = fmt.Sprintf(msg, args...)
	}
	switch {
	case len(msg) == 0:
		return
	case msg[len(msg)-1] == '\n':
		msg = msg[:len(msg)-1]
	}
	stdout.ensureNewline()
	stderr.ensureNewline()
	stderrf(gray)

	switch {
	case strings.Count(msg, "\n") == 0:
		stderrf(" │ ")
		stderrf(msg)

	default: // multi-line
		first := true
		for i := strings.Index(msg, "\n"); i >= 0; {
			if first {
				stderrf(" ┌ ")
				first = false
			} else {
				stderrf(" │ ")
			}
			stderrf(msg[:i])
			stderrf("\n")
			msg = msg[i+1:]
			i = strings.Index(msg, "\n")
		}
		stderrf(" └ ")
		stderrf(msg)
		stderrf("\n")
	}
	// stderr.ensureNewline()
	stderrf(reset)
}

func exitf(msg string, args ...any) {
	msg = trimPrefixNewline(msg)
	stderrf(msg, args...)
	if !strings.HasSuffix(msg, "\n") {
		stderrf("\n")
	}
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
		stderrf("ERROR: %v\n", err)
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

var rePrefixNewline = regexp.MustCompile(`^\n *`)

func trimPrefixNewline(s string) string {
	return rePrefixNewline.ReplaceAllString(s, "")
}
