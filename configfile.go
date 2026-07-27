package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// stringSliceFlag is a repeatable string flag: each occurrence appends one
// value (e.g. `--add-label a --add-label b` => ["a","b"]). Labels may contain
// spaces (and a "•"), so occurrences are NOT split on commas.
type stringSliceFlag []string

func (s *stringSliceFlag) String() string { return strings.Join(*s, ",") }

func (s *stringSliceFlag) Set(v string) error {
	*s = append(*s, v)
	return nil
}

// dedupStrings returns xs with blanks and duplicates removed, order preserved.
func dedupStrings(xs []string) []string {
	seen := make(map[string]bool, len(xs))
	var out []string
	for _, x := range xs {
		if x == "" || seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}

// fileConfig mirrors the on-disk git-pr config file. JSON is the default,
// canonical format; YAML (.yaml/.yml) is also accepted and parses to the same
// struct. Fields correspond to the equivalent CLI flags.
type fileConfig struct {
	AddLabel    []string `json:"add_label" yaml:"add_label"`
	AddTipLabel []string `json:"add_tip_label" yaml:"add_tip_label"`
}

// mergeFileConfig overlays b onto a: any non-nil slice in b replaces a's. A
// present-but-empty list (`[]`) is meaningful (explicitly clears) and wins over
// an absent one (nil).
func mergeFileConfig(a, b fileConfig) fileConfig {
	if b.AddLabel != nil {
		a.AddLabel = b.AddLabel
	}
	if b.AddTipLabel != nil {
		a.AddTipLabel = b.AddTipLabel
	}
	return a
}

// readConfigFile reads <base>.json, else <base>.yaml, else <base>.yml (first
// that exists). Returns (_, false) when none exist. A malformed file is logged
// and treated as absent, so a bad config can never wedge a push.
func readConfigFile(base string) (fileConfig, bool) {
	// JSON is the default/canonical format, tried first.
	if data, err := os.ReadFile(base + ".json"); err == nil {
		var fc fileConfig
		if uerr := json.Unmarshal(data, &fc); uerr != nil {
			debugf("git-pr: ignoring malformed %s.json: %v", base, uerr)
			return fileConfig{}, false
		}
		return fc, true
	}
	for _, ext := range []string{".yaml", ".yml"} {
		if data, err := os.ReadFile(base + ext); err == nil {
			var fc fileConfig
			if uerr := yaml.Unmarshal(data, &fc); uerr != nil {
				debugf("git-pr: ignoring malformed %s%s: %v", base, ext, uerr)
				return fileConfig{}, false
			}
			return fc, true
		}
	}
	return fileConfig{}, false
}

// findNearestProjectConfigDir walks up from startDir to repoRoot (inclusive)
// and returns the FIRST directory containing a .config/git-pr.{json,yaml,yml}.
// It never ascends above repoRoot — so a git submodule (whose repoRoot is its
// own working tree) will not inherit the parent repo's config. Returns "" when
// none is found.
func findNearestProjectConfigDir(startDir, repoRoot string) string {
	dir := filepath.Clean(startDir)
	repoRoot = filepath.Clean(repoRoot)
	for {
		if _, ok := readConfigFile(filepath.Join(dir, ".config", "git-pr")); ok {
			return dir
		}
		if dir == repoRoot {
			return ""
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "" // filesystem root: safety stop (startDir wasn't under repoRoot)
		}
		dir = parent
	}
}

// loadFileConfig resolves git-pr's file configuration, lowest precedence first:
//  1. ~/.config/git-pr/config.{json,yaml,yml}      (global)
//  2. <nearest>/.config/git-pr.{json,yaml,yml}     (project; nearest ancestor wins)
//  3. <nearest>/.config/git-pr.local.{json,...}    (per-user project override)
//
// where <nearest> is the closest ancestor of startDir (up to and including
// repoRoot) that holds a project config. Project files are NOT merged across
// parent levels — only the nearest is read. CLI flags (applied by the caller)
// take precedence over everything returned here.
func loadFileConfig(startDir, repoRoot string) fileConfig {
	var merged fileConfig
	if home := os.Getenv("HOME"); home != "" {
		if fc, ok := readConfigFile(filepath.Join(home, ".config", "git-pr", "config")); ok {
			merged = mergeFileConfig(merged, fc)
		}
	}
	if dir := findNearestProjectConfigDir(startDir, repoRoot); dir != "" {
		if fc, ok := readConfigFile(filepath.Join(dir, ".config", "git-pr")); ok {
			merged = mergeFileConfig(merged, fc)
		}
		if fc, ok := readConfigFile(filepath.Join(dir, ".config", "git-pr.local")); ok {
			merged = mergeFileConfig(merged, fc)
		}
	}
	return merged
}
