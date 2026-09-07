package main

import (
	"strings"
	"testing"
)

// withTestRepo points the global config at a known host/repo so ghWebURL (and
// therefore {url}) is deterministic, restoring it afterwards.
func withTestRepo(t *testing.T) {
	t.Helper()
	saved := config
	config.git.host = "github.com"
	config.git.repo = "org/repo"
	t.Cleanup(func() { config = saved })
}

func testCommit() *Commit {
	return &Commit{
		Hash:        "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678",
		Title:       "Fix the parser",
		AuthorName:  "Oliver N",
		AuthorEmail: "oliver@example.com",
		PRNumber:    1234,
		Attrs:       []KeyVal{{KeyRemoteRef, "oliver/a1b2c3d4"}},
	}
}

func TestFormatPROutput_Presets(t *testing.T) {
	withTestRepo(t)
	commit := testCommit()

	tests := []struct {
		spec string
		want string
	}{
		{"", "https://github.com/org/repo/pull/1234 Fix the parser"}, // default
		{"url", "https://github.com/org/repo/pull/1234"},
		{"url-title", "https://github.com/org/repo/pull/1234 Fix the parser"},
		{"markdown", "[#1234](https://github.com/org/repo/pull/1234) Fix the parser"},
	}
	for _, tt := range tests {
		tmpl, err := resolveOutputFormat(tt.spec)
		assert(t, err == nil).Fatalf("resolveOutputFormat(%q): %v", tt.spec, err)
		got := formatPROutput(tmpl, commit)
		assert(t, got == tt.want).Errorf("output %q = %q, want %q", tt.spec, got, tt.want)
	}
}

func TestFormatPROutput_CustomTemplates(t *testing.T) {
	withTestRepo(t)
	commit := testCommit()

	tests := []struct {
		spec string
		want string
	}{
		{"{shorthash} #{number} {title}", "a1b2c3d4 #1234 Fix the parser"},
		{"{repo}#{number} on {host}", "org/repo#1234 on github.com"},
		{"{branch} by {author} <{email}>", "oliver/a1b2c3d4 by Oliver N <oliver@example.com>"},
		{"{hash}", "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"},
		{"{number} {number}", "1234 1234"},                             // repeated placeholder
		{"{ } {title} {", "{ } Fix the parser {"},                      // lone braces stay literal
		{"- [ ] {url}", "- [ ] https://github.com/org/repo/pull/1234"}, // brackets are not special
	}
	for _, tt := range tests {
		tmpl, err := resolveOutputFormat(tt.spec)
		assert(t, err == nil).Fatalf("resolveOutputFormat(%q): %v", tt.spec, err)
		got := formatPROutput(tmpl, commit)
		assert(t, got == tt.want).Errorf("template %q = %q, want %q", tt.spec, got, tt.want)
	}
}

func TestResolveOutputFormat_DefaultsToURLTitle(t *testing.T) {
	tmpl, err := resolveOutputFormat("")
	assert(t, err == nil).Fatalf("unexpected error: %v", err)
	assert(t, tmpl == outputPresets[defaultOutputPreset]).
		Errorf("empty spec = %q, want %q", tmpl, outputPresets[defaultOutputPreset])

	// surrounding whitespace must not defeat preset lookup
	tmpl, err = resolveOutputFormat("  markdown  ")
	assert(t, err == nil).Fatalf("unexpected error: %v", err)
	assert(t, tmpl == outputPresets["markdown"]).Errorf("padded preset = %q", tmpl)
}

func TestResolveOutputFormat_UnknownPlaceholder(t *testing.T) {
	_, err := resolveOutputFormat("{url} {nope}")
	assert(t, err != nil).Fatalf("expected an error for an unknown placeholder")
	assert(t, strings.Contains(err.Error(), "{nope}")).
		Errorf("error must name the offending placeholder, got: %v", err)
	assert(t, strings.Contains(err.Error(), "{title}")).
		Errorf("error must list the valid placeholders, got: %v", err)
}

func TestResolveOutputFormat_UnknownPreset(t *testing.T) {
	_, err := resolveOutputFormat("bogus")
	assert(t, err != nil).Fatalf("expected an error for an unknown preset")
	for _, want := range []string{"bogus", "markdown", "url-title"} {
		assert(t, strings.Contains(err.Error(), want)).
			Errorf("error must mention %q, got: %v", want, err)
	}
}

// outputFieldNames drives validation while outputReplacements drives rendering;
// a field in one but not the other is a silent bug.
func TestOutputFieldsAndReplacementsInSync(t *testing.T) {
	withTestRepo(t)
	pairs := outputReplacements(testCommit())
	assert(t, len(pairs) == 2*len(outputFieldNames)).
		Fatalf("outputReplacements has %d pairs, outputFieldNames has %d entries", len(pairs)/2, len(outputFieldNames))

	got := make(map[string]bool, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		got[pairs[i]] = true
	}
	for _, name := range outputFieldNames {
		assert(t, got["{"+name+"}"]).Errorf("field %q listed in outputFieldNames but not rendered", name)
	}
}
