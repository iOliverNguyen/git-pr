package main

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// defaultOutputPreset is used when --output / GIT_PR_OUTPUT / the config file
// all leave the format unset.
const defaultOutputPreset = "url-title"

// outputPresets maps a --output preset name to its template. Presets are just
// shorthand for a template a user could have typed out.
var outputPresets = map[string]string{
	"url":       `{url}`,
	"url-title": `{url} {title}`,
	"markdown":  `[#{number}]({url}) {title}`,
}

// outputFieldNames lists every placeholder accepted in an output template. It
// must stay in sync with outputReplacements below — keep the two together.
var outputFieldNames = []string{
	"url", "number", "title", "hash", "shorthash",
	"repo", "host", "branch", "author", "email",
}

// outputReplacements returns the {placeholder}, value pairs for commit, in the
// form strings.NewReplacer wants. Must stay in sync with outputFieldNames.
func outputReplacements(commit *Commit) []string {
	return []string{
		"{url}", ghWebURL("pull/%v", commit.PRNumber),
		"{number}", fmt.Sprintf("%d", commit.PRNumber),
		"{title}", commit.Title,
		"{hash}", commit.Hash,
		"{shorthash}", commit.ShortHash(),
		"{repo}", config.git.repo,
		"{host}", config.git.host,
		"{branch}", commit.GetRemoteRef(),
		"{author}", commit.AuthorName,
		"{email}", commit.AuthorEmail,
	}
}

// regexpOutputToken matches a braced placeholder candidate, e.g. "{title}". A
// lone "{" or "{ }" does not match and is left alone as a literal.
var regexpOutputToken = regexp.MustCompile(`\{[a-zA-Z0-9_-]+\}`)

// resolveOutputFormat turns a --output value into a template string:
// empty → the default preset, a preset name → its template, anything
// containing a placeholder → itself (after validating every placeholder).
func resolveOutputFormat(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		s = defaultOutputPreset
	}
	if tmpl, ok := outputPresets[s]; ok {
		return tmpl, nil
	}
	if strings.Contains(s, "{") {
		known := make(map[string]bool, len(outputFieldNames))
		for _, name := range outputFieldNames {
			known["{"+name+"}"] = true
		}
		for _, token := range regexpOutputToken.FindAllString(s, -1) {
			if !known[token] {
				return "", fmt.Errorf("unknown placeholder %v in --output template\n%v", token, outputFormatHelp())
			}
		}
		return s, nil
	}
	return "", fmt.Errorf("invalid --output %q\n%v", s, outputFormatHelp())
}

// outputFormatHelp lists the presets and placeholders, appended to any
// resolveOutputFormat error.
func outputFormatHelp() string {
	names := make([]string, 0, len(outputPresets))
	for name := range outputPresets {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	fprintf(&b, "\nPresets:\n")
	for _, name := range names {
		suffix := ""
		if name == defaultOutputPreset {
			suffix = "  (default)"
		}
		fprintf(&b, "  %-10v %v%v\n", name, outputPresets[name], suffix)
	}
	fprintf(&b, "\nOr a custom template using these placeholders:\n  ")
	for i, name := range outputFieldNames {
		if i > 0 {
			fprint(&b, ", ")
		}
		fprintf(&b, "{%v}", name)
	}
	fprintf(&b, "\n\nExample:\n  git pr --output '{shorthash} #{number} {title}'\n")
	return b.String()
}

// formatPROutput renders one output line for commit using tmpl, which must
// already have gone through resolveOutputFormat.
func formatPROutput(tmpl string, commit *Commit) string {
	return strings.NewReplacer(outputReplacements(commit)...).Replace(tmpl)
}
