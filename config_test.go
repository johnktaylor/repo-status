package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadConfigRejectsEmptyLocation verifies that blank repository paths are rejected.
// It receives the Go test context and reports a test failure if readConfig returns no empty-location error.
func TestReadConfigRejectsEmptyLocation(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: missing-location\n    location: '   '\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := readConfig(configFile)
	if err == nil {
		t.Fatal("readConfig() succeeded with an empty local location")
	}
	if !strings.Contains(err.Error(), "empty location") {
		t.Fatalf("readConfig() error = %q, want empty-location error", err)
	}
}

// TestReadConfigRejectsEmptyName verifies that blank repository names are rejected.
// It receives the Go test context and reports a test failure if readConfig accepts an empty name.
func TestReadConfigRejectsEmptyName(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: '   '\n    location: ./repo\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := readConfig(configFile)
	if err == nil {
		t.Fatal("readConfig() succeeded with an empty repository name")
	}
	if !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("readConfig() error = %q, want empty-name error", err)
	}
}

// TestReadConfigRejectsLocationType verifies that unsupported remote configuration is rejected.
// It receives the Go test context and reports a test failure if readConfig accepts a location type.
func TestReadConfigRejectsLocationType(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: remote-repository\n    location: git@example.com:team/repo.git\n    locationtype: remote\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := readConfig(configFile)
	if err == nil {
		t.Fatal("readConfig() succeeded with an unsupported location type")
	}
	if !strings.Contains(err.Error(), "field locationtype not found") {
		t.Fatalf("readConfig() error = %q, want unsupported-location-type error", err)
	}
}

// TestReadConfigRejectsUnknownFields verifies that misspelled YAML keys are rejected.
// It receives the Go test context and reports a test failure if unknown configuration fields are accepted.
func TestReadConfigRejectsUnknownFields(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: misspelled-field\n    location: ./repo\n    locationtyp: remote\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := readConfig(configFile)
	if err == nil {
		t.Fatal("readConfig() succeeded with an unknown field")
	}
	if !strings.Contains(err.Error(), "field locationtyp not found") {
		t.Fatalf("readConfig() error = %q, want unknown-field error", err)
	}
}

// TestFindRepoAllowsNumericName verifies that a numeric selector falls back to an exact repository name.
// It receives the Go test context and reports a test failure if an out-of-range index cannot select a numeric name.
func TestFindRepoAllowsNumericName(t *testing.T) {
	config := &Config{Repos: []Repo{{Name: "42", Location: "./repo"}}}

	repo, err := findRepo(config, "42")
	if err != nil {
		t.Fatalf("findRepo() error = %v", err)
	}
	if repo.Name != "42" {
		t.Fatalf("findRepo() name = %q, want %q", repo.Name, "42")
	}
}

// TestIsRepoDirty distinguishes a clean repository from one with an untracked file.
// It receives the Go test context and reports a test failure if Git porcelain status is interpreted incorrectly.
func TestIsRepoDirty(t *testing.T) {
	repoDir := t.TempDir()
	init := exec.Command("git", "init", repoDir)
	if output, err := init.CombinedOutput(); err != nil {
		t.Fatalf("initialize repository: %v\n%s", err, output)
	}

	// A newly initialized repository has no changes.
	dirty, err := isRepoDirty(repoDir)
	if err != nil {
		t.Fatalf("check clean repository: %v", err)
	}
	if dirty {
		t.Fatal("isRepoDirty() reported a new repository as dirty")
	}

	// An untracked file must make the repository dirty.
	if err := os.WriteFile(filepath.Join(repoDir, "untracked.txt"), []byte("content"), 0o600); err != nil {
		t.Fatalf("write untracked file: %v", err)
	}
	dirty, err = isRepoDirty(repoDir)
	if err != nil {
		t.Fatalf("check dirty repository: %v", err)
	}
	if !dirty {
		t.Fatal("isRepoDirty() did not report an untracked file")
	}
}
