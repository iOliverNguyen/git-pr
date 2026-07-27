package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestReadConfigFile_JSONAndYAMLParseIdentically(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "j.json"), `{"add_label":["a","b"],"add_tip_label":["tip"]}`)
	writeFile(t, filepath.Join(dir, "y.yaml"), "add_label:\n  - a\n  - b\nadd_tip_label:\n  - tip\n")

	j, okJ := readConfigFile(filepath.Join(dir, "j"))
	y, okY := readConfigFile(filepath.Join(dir, "y"))
	assert(t, okJ && okY).Errorf("expected both files to load: json=%v yaml=%v", okJ, okY)
	assert(t, reflect.DeepEqual(j, y)).Errorf("json %+v != yaml %+v", j, y)
	assert(t, reflect.DeepEqual(j.AddLabel, []string{"a", "b"})).Errorf("add_label = %v", j.AddLabel)
	assert(t, reflect.DeepEqual(j.AddTipLabel, []string{"tip"})).Errorf("add_tip_label = %v", j.AddTipLabel)
}

func TestReadConfigFile_JSONPreferredOverYAML(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "git-pr.json"), `{"add_label":["json"]}`)
	writeFile(t, filepath.Join(dir, "git-pr.yaml"), "add_label:\n  - yaml\n")
	fc, ok := readConfigFile(filepath.Join(dir, "git-pr"))
	assert(t, ok).Errorf("expected load")
	assert(t, reflect.DeepEqual(fc.AddLabel, []string{"json"})).Errorf("expected json to win, got %v", fc.AddLabel)
}

func TestReadConfigFile_MalformedTreatedAsAbsent(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "git-pr.json"), `{not valid json`)
	_, ok := readConfigFile(filepath.Join(dir, "git-pr"))
	assert(t, !ok).Errorf("malformed JSON must be treated as absent")
}

func TestFindNearestProjectConfigDir_NearestWins(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, ".config", "git-pr.json"), `{"add_label":["root"]}`)
	sub := filepath.Join(root, "a", "b")
	writeFile(t, filepath.Join(sub, ".config", "git-pr.json"), `{"add_label":["sub"]}`)

	got := findNearestProjectConfigDir(sub, root)
	assert(t, got == sub).Errorf("nearest dir = %q, want %q", got, sub)
}

func TestFindNearestProjectConfigDir_StopsAtRepoRoot_SubmoduleBoundary(t *testing.T) {
	// parent repo has config; the "submodule" (its own repoRoot) does not.
	// Discovery must NOT ascend past the submodule root into the parent.
	parent := t.TempDir()
	writeFile(t, filepath.Join(parent, ".config", "git-pr.json"), `{"add_label":["parent"]}`)
	submodule := filepath.Join(parent, "vendored")
	if err := os.MkdirAll(filepath.Join(submodule, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := findNearestProjectConfigDir(filepath.Join(submodule, "deep"), submodule)
	assert(t, got == "").Errorf("submodule must not inherit parent config; got %q", got)
}

func TestLoadFileConfig_Precedence_GlobalProjectLocal(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	writeFile(t, filepath.Join(home, ".config", "git-pr", "config.json"),
		`{"add_label":["global"],"add_tip_label":["global-tip"]}`)

	repo := t.TempDir()
	// project overrides global's add_label; leaves add_tip_label from global.
	writeFile(t, filepath.Join(repo, ".config", "git-pr.json"), `{"add_label":["project"]}`)
	// local overrides project's add_label again.
	writeFile(t, filepath.Join(repo, ".config", "git-pr.local.json"), `{"add_label":["local"]}`)

	fc := loadFileConfig(repo, repo)
	assert(t, reflect.DeepEqual(fc.AddLabel, []string{"local"})).
		Errorf("add_label precedence local>project>global failed: %v", fc.AddLabel)
	assert(t, reflect.DeepEqual(fc.AddTipLabel, []string{"global-tip"})).
		Errorf("add_tip_label should fall through to global: %v", fc.AddTipLabel)
}

func TestLoadFileConfig_NoConfigIsEmpty(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	repo := t.TempDir()
	fc := loadFileConfig(repo, repo)
	assert(t, fc.AddLabel == nil && fc.AddTipLabel == nil).
		Errorf("absent config must leave labelling off, got %+v", fc)
}

func TestDedupStrings(t *testing.T) {
	got := dedupStrings([]string{"a", "", "b", "a", "c", "b"})
	assert(t, reflect.DeepEqual(got, []string{"a", "b", "c"})).Errorf("dedupStrings = %v", got)
}
