package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestLoadAndValidateConfigRejectsEmptyLocation verifies that blank repository paths are rejected.
// It receives the Go test context and reports a test failure if loadAndValidateConfig returns no empty-location error.
func TestLoadAndValidateConfigRejectsEmptyLocation(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: missing-location\n    location: '   '\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadAndValidateConfig(configFile)
	if err == nil {
		t.Fatal("loadAndValidateConfig() succeeded with an empty local location")
	}
	if !strings.Contains(err.Error(), "empty location") {
		t.Fatalf("loadAndValidateConfig() error = %q, want empty-location error", err)
	}
}

// TestLoadAndValidateConfigRejectsEmptyName verifies that blank repository names are rejected.
// It receives the Go test context and reports a test failure if loadAndValidateConfig accepts an empty name.
func TestLoadAndValidateConfigRejectsEmptyName(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: '   '\n    location: ./repo\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadAndValidateConfig(configFile)
	if err == nil {
		t.Fatal("loadAndValidateConfig() succeeded with an empty repository name")
	}
	if !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("loadAndValidateConfig() error = %q, want empty-name error", err)
	}
}

// TestLoadAndValidateConfigRejectsLocationType verifies that unsupported remote configuration is rejected.
// It receives the Go test context and reports a test failure if loadAndValidateConfig accepts a location type.
func TestLoadAndValidateConfigRejectsLocationType(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: remote-repository\n    location: git@example.com:team/repo.git\n    locationtype: remote\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadAndValidateConfig(configFile)
	if err == nil {
		t.Fatal("loadAndValidateConfig() succeeded with an unsupported location type")
	}
	if !strings.Contains(err.Error(), "field locationtype not found") {
		t.Fatalf("loadAndValidateConfig() error = %q, want unsupported-location-type error", err)
	}
}

// TestLoadAndValidateConfigRejectsUnknownFields verifies that misspelled YAML keys are rejected.
// It receives the Go test context and reports a test failure if unknown configuration fields are accepted.
func TestLoadAndValidateConfigRejectsUnknownFields(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: misspelled-field\n    location: ./repo\n    locationtyp: remote\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadAndValidateConfig(configFile)
	if err == nil {
		t.Fatal("loadAndValidateConfig() succeeded with an unknown field")
	}
	if !strings.Contains(err.Error(), "field locationtyp not found") {
		t.Fatalf("loadAndValidateConfig() error = %q, want unknown-field error", err)
	}
}

// TestLoadAndValidateConfigResolvesRelativeLocations verifies the happy path and that relative
// locations resolve against the directory containing the config file.
// It receives the Go test context and reports a test failure when names, trimming, or path resolution behave incorrectly.
func TestLoadAndValidateConfigResolvesRelativeLocations(t *testing.T) {
	configDir := t.TempDir()
	configFile := filepath.Join(configDir, "repos.yaml")
	config := "repositories:\n  - name: first\n    location: ./repo-a\n  - name: second\n    location: repo-b\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config_loaded, err := loadAndValidateConfig(configFile)
	if err != nil {
		t.Fatalf("loadAndValidateConfig() error = %v", err)
	}
	if len(config_loaded.Repos) != 2 {
		t.Fatalf("loadAndValidateConfig() loaded %d repositories, want 2", len(config_loaded.Repos))
	}
	if config_loaded.Repos[0].Name != "first" || config_loaded.Repos[1].Name != "second" {
		t.Fatalf("loadAndValidateConfig() names = %q, %q, want %q, %q", config_loaded.Repos[0].Name, config_loaded.Repos[1].Name, "first", "second")
	}

	// Both relative forms must resolve under the config file's directory.
	wantFirst := filepath.Join(configDir, "repo-a")
	wantSecond := filepath.Join(configDir, "repo-b")
	if config_loaded.Repos[0].Location != wantFirst {
		t.Fatalf("location = %q, want %q", config_loaded.Repos[0].Location, wantFirst)
	}
	if config_loaded.Repos[1].Location != wantSecond {
		t.Fatalf("location = %q, want %q", config_loaded.Repos[1].Location, wantSecond)
	}
}

// TestLoadAndValidateConfigTrimsFields verifies that surrounding whitespace is removed from stored names and locations.
// It receives the Go test context and reports a test failure if whitespace survives validation.
func TestLoadAndValidateConfigTrimsFields(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: ' api '\n    location: ' ./repo '\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config_loaded, err := loadAndValidateConfig(configFile)
	if err != nil {
		t.Fatalf("loadAndValidateConfig() error = %v", err)
	}
	if config_loaded.Repos[0].Name != "api" {
		t.Fatalf("name = %q, want %q", config_loaded.Repos[0].Name, "api")
	}
	if config_loaded.Repos[0].Location != filepath.Join(filepath.Dir(configFile), "repo") {
		t.Fatalf("location = %q, want trimmed path", config_loaded.Repos[0].Location)
	}
}

// TestLoadAndValidateConfigRejectsDuplicateNames verifies that duplicate repository names are rejected.
// It receives the Go test context and reports a test failure if duplicate names are accepted.
func TestLoadAndValidateConfigRejectsDuplicateNames(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: api\n    location: ./repo-a\n  - name: api\n    location: ./repo-b\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadAndValidateConfig(configFile)
	if err == nil {
		t.Fatal("loadAndValidateConfig() succeeded with duplicate names")
	}
	if !strings.Contains(err.Error(), "duplicate repository name") {
		t.Fatalf("loadAndValidateConfig() error = %q, want duplicate-name error", err)
	}
}

// TestLoadAndValidateConfigRejectsCaseInsensitiveDuplicates verifies that names differing only by letter case are rejected.
// It receives the Go test context and reports a test failure if case variants of one name are both accepted.
func TestLoadAndValidateConfigRejectsCaseInsensitiveDuplicates(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: api\n    location: ./repo-a\n  - name: API\n    location: ./repo-b\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadAndValidateConfig(configFile)
	if err == nil {
		t.Fatal("loadAndValidateConfig() succeeded with case-insensitive duplicate names")
	}
	if !strings.Contains(err.Error(), "duplicate repository name") {
		t.Fatalf("loadAndValidateConfig() error = %q, want duplicate-name error", err)
	}
}

// TestLoadAndValidateConfigRejectsMultipleDocuments verifies that repositories in a second YAML document are rejected instead of ignored.
// It receives the Go test context and reports a test failure if appended documents are silently dropped.
func TestLoadAndValidateConfigRejectsMultipleDocuments(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	config := "repositories:\n  - name: first\n    location: ./repo-a\n---\nrepositories:\n  - name: second\n    location: ./repo-b\n"
	if err := os.WriteFile(configFile, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadAndValidateConfig(configFile)
	if err == nil {
		t.Fatal("loadAndValidateConfig() succeeded with multiple YAML documents")
	}
	if !strings.Contains(err.Error(), "exactly one YAML document") {
		t.Fatalf("loadAndValidateConfig() error = %q, want multiple-document error", err)
	}
}

// TestLoadAndValidateConfigRejectsEmptyFile verifies that an empty, comments-only, or null-document
// config produces a clear error instead of a silently empty configuration.
// It receives the Go test context and reports a test failure if such a file is accepted or reports a cryptic YAML EOF message.
func TestLoadAndValidateConfigRejectsEmptyFile(t *testing.T) {
	// Both variants decode without a YAML error yet define no repositories.
	contents := []string{"# comments only\n", "---\n"}

	for _, content := range contents {
		configFile := filepath.Join(t.TempDir(), "repos.yaml")
		if err := os.WriteFile(configFile, []byte(content), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}

		_, err := loadAndValidateConfig(configFile)
		if err == nil {
			t.Fatalf("loadAndValidateConfig() succeeded with content %q", content)
		}
		if !strings.Contains(err.Error(), "config file contains no repositories") {
			t.Fatalf("loadAndValidateConfig() error = %q, want no-document error", err)
		}
	}
}

// TestFindRepoByIndexAndName verifies one-based index and exact-name selection.
// It receives the Go test context and reports a test failure if selectors resolve to the wrong repository.
func TestFindRepoByIndexAndName(t *testing.T) {
	config := &Config{Repos: []Repo{
		{Name: "first", Location: "./repo-a"},
		{Name: "second", Location: "./repo-b"},
	}}

	// Index selectors must point at the underlying slice element.
	byIndex, err := findRepo(config, "2")
	if err != nil {
		t.Fatalf("findRepo(index) error = %v", err)
	}
	if byIndex.Name != "second" {
		t.Fatalf("findRepo(index) name = %q, want %q", byIndex.Name, "second")
	}
	if byIndex != &config.Repos[1] {
		t.Fatal("findRepo(index) did not return the slice element pointer")
	}

	// Name selectors must also point at the underlying slice element.
	byName, err := findRepo(config, "first")
	if err != nil {
		t.Fatalf("findRepo(name) error = %v", err)
	}
	if byName != &config.Repos[0] {
		t.Fatal("findRepo(name) did not return the slice element pointer")
	}
}

// TestFindRepoRejectsOutOfRangeIndex verifies the error for a numeric selector beyond the repository count.
// It receives the Go test context and reports a test failure if out-of-range indexes are not reported clearly.
func TestFindRepoRejectsOutOfRangeIndex(t *testing.T) {
	config := &Config{Repos: []Repo{{Name: "first", Location: "./repo-a"}}}

	_, err := findRepo(config, "5")
	if err == nil {
		t.Fatal("findRepo() succeeded with an out-of-range index")
	}
	if !strings.Contains(err.Error(), "index out of range: 5") {
		t.Fatalf("findRepo() error = %q, want out-of-range error", err)
	}
}

// TestFindRepoRejectsUnknownName verifies the error for a selector that matches no repository.
// It receives the Go test context and reports a test failure if unknown names are not reported clearly.
func TestFindRepoRejectsUnknownName(t *testing.T) {
	config := &Config{Repos: []Repo{{Name: "first", Location: "./repo-a"}}}

	_, err := findRepo(config, "missing")
	if err == nil {
		t.Fatal("findRepo() succeeded with an unknown name")
	}
	if !strings.Contains(err.Error(), "repository not found: missing") {
		t.Fatalf("findRepo() error = %q, want not-found error", err)
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

// TestLoadAndValidateConfigRejectsUnsafeNames verifies that repository labels
// containing a selector separator or control characters are rejected, because
// they cannot be selected reliably or displayed safely.
func TestLoadAndValidateConfigRejectsUnsafeNames(t *testing.T) {
	configurations := []string{
		"repositories:\n  - name: \"with,comma\"\n    location: ./repo\n",
		"repositories:\n  - name: \"tab\\tname\"\n    location: ./repo\n",
		"repositories:\n  - name: \"line\\nbreak\"\n    location: ./repo\n",
	}

	for _, configuration := range configurations {
		configFile := filepath.Join(t.TempDir(), "repos.yaml")
		if err := os.WriteFile(configFile, []byte(configuration), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}

		if _, err := loadAndValidateConfig(configFile); err == nil {
			t.Fatalf("loadAndValidateConfig() accepted unsafe repository name in %q", configuration)
		}
	}
}

// writeSizedConfig writes a valid one-repository YAML document of exactly
// totalBytes bytes, padding it with a comment line, and returns its path.
func writeSizedConfig(t *testing.T, totalBytes int) string {
	t.Helper()

	prefix := "repositories:\n  - name: boundary\n    location: ./repo\n"
	padding := totalBytes - len(prefix)
	if padding < 2 {
		t.Fatalf("cannot pad config to %d bytes; prefix already uses %d", totalBytes, len(prefix))
	}
	contents := prefix + "#" + strings.Repeat("x", padding-2) + "\n"
	if len(contents) != totalBytes {
		t.Fatalf("built config has %d bytes, want %d", len(contents), totalBytes)
	}

	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	if err := os.WriteFile(configFile, []byte(contents), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configFile
}

// TestLoadAndValidateConfigEnforcesByteLimit verifies that a configuration of
// exactly maxConfigFileBytes is accepted while one additional byte is rejected.
func TestLoadAndValidateConfigEnforcesByteLimit(t *testing.T) {
	cases := []struct {
		name      string
		size      int
		wantValid bool
	}{
		{name: "exact maximum", size: maxConfigFileBytes, wantValid: true},
		{name: "one byte over", size: maxConfigFileBytes + 1, wantValid: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			configFile := writeSizedConfig(t, testCase.size)
			config, err := loadAndValidateConfig(configFile)
			if testCase.wantValid {
				if err != nil {
					t.Fatalf("loadAndValidateConfig() rejected %d-byte config: %v", testCase.size, err)
				}
				if len(config.Repos) != 1 {
					t.Fatalf("loadAndValidateConfig() loaded %d repositories, want 1", len(config.Repos))
				}
				return
			}
			if err == nil {
				t.Fatalf("loadAndValidateConfig() accepted %d-byte config", testCase.size)
			}
			if !strings.Contains(err.Error(), "maximum") {
				t.Fatalf("loadAndValidateConfig() error = %q, want oversized-config error", err)
			}
		})
	}
}

// TestLoadAndValidateConfigEnforcesRepositoryLimit verifies that exactly
// maxConfiguredRepos entries are accepted while one additional entry is rejected.
func TestLoadAndValidateConfigEnforcesRepositoryLimit(t *testing.T) {
	cases := []struct {
		name      string
		count     int
		wantValid bool
	}{
		{name: "exact maximum", count: maxConfiguredRepos, wantValid: true},
		{name: "one over", count: maxConfiguredRepos + 1, wantValid: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var builder strings.Builder
			builder.WriteString("repositories:\n")
			for i := 0; i < testCase.count; i++ {
				fmt.Fprintf(&builder, "  - name: repo-%d\n    location: ./repo-%d\n", i, i)
			}

			configFile := filepath.Join(t.TempDir(), "repos.yaml")
			if err := os.WriteFile(configFile, []byte(builder.String()), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			config, err := loadAndValidateConfig(configFile)
			if testCase.wantValid {
				if err != nil {
					t.Fatalf("loadAndValidateConfig() rejected %d repositories: %v", testCase.count, err)
				}
				if len(config.Repos) != testCase.count {
					t.Fatalf("loadAndValidateConfig() loaded %d repositories, want %d", len(config.Repos), testCase.count)
				}
				return
			}
			if err == nil {
				t.Fatalf("loadAndValidateConfig() accepted %d repositories", testCase.count)
			}
			if !strings.Contains(err.Error(), "maximum") {
				t.Fatalf("loadAndValidateConfig() error = %q, want repository-limit error", err)
			}
		})
	}
}

// TestLoadAndValidateConfigRejectsWindowsPartialLocations verifies that
// drive-relative and root-relative locations are rejected on Windows instead
// of being silently joined to the configuration directory.
func TestLoadAndValidateConfigRejectsWindowsPartialLocations(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path validation test")
	}

	locations := []string{"C:", "C:repos", `\repos`}
	for _, location := range locations {
		configFile := filepath.Join(t.TempDir(), "repos.yaml")
		configuration := "repositories:\n  - name: partial\n    location: '" + location + "'\n"
		if err := os.WriteFile(configFile, []byte(configuration), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}

		if _, err := loadAndValidateConfig(configFile); err == nil {
			t.Fatalf("loadAndValidateConfig() accepted partial Windows location %q", location)
		}
	}
}

// TestLoadAndValidateConfigRejectsWindowsAlternateDataStreams verifies that
// Windows alternate-data-stream paths are rejected for both the config file and
// repository locations.
func TestLoadAndValidateConfigRejectsWindowsAlternateDataStreams(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows alternate data stream test")
	}

	t.Run("config path", func(t *testing.T) {
		configFile := filepath.Join(t.TempDir(), "repos.yaml:metadata")
		_, err := loadAndValidateConfig(configFile)
		if err == nil {
			t.Fatal("loadAndValidateConfig() accepted a config path with an alternate data stream")
		}
		if !strings.Contains(err.Error(), "alternate data stream") {
			t.Fatalf("loadAndValidateConfig() error = %q, want alternate-data-stream error", err)
		}
	})

	t.Run("location", func(t *testing.T) {
		locations := []string{`C:\repo:stream`, "./repo:stream"}
		for _, location := range locations {
			configFile := filepath.Join(t.TempDir(), "repos.yaml")
			configuration := fmt.Sprintf("repositories:\n  - name: ads\n    location: '%s'\n", location)
			if err := os.WriteFile(configFile, []byte(configuration), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}

			_, err := loadAndValidateConfig(configFile)
			if err == nil {
				t.Fatalf("loadAndValidateConfig() accepted location with alternate data stream %q", location)
			}
			if !strings.Contains(err.Error(), "alternate data stream") {
				t.Fatalf("loadAndValidateConfig() error = %q, want alternate-data-stream error", err)
			}
		}
	})
}

// TestIsRepoDirty distinguishes a clean repository from one with an untracked file.
// It receives the Go test context and reports a test failure if Git porcelain status is interpreted incorrectly.
func TestIsRepoDirty(t *testing.T) {
	repoDir := t.TempDir()
	// Initialize an empty repository for clean and dirty status checks.
	gitInitCommand := exec.Command("git", "init", repoDir)
	if output, err := gitInitCommand.CombinedOutput(); err != nil {
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
