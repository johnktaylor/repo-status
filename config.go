package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Configuration bounds keep a mistaken or generated file from exhausting memory or spawning
// an unreasonable number of repository operations in one run.
const (
	maxConfigFileBytes = 1 << 20 // 1 MiB
	maxConfiguredRepos = 1000
)

type Repo struct {
	Location string `yaml:"location"`
	Name     string `yaml:"name"`
}

type Config struct {
	Repos []Repo `yaml:"repositories"`
}

// loadAndValidateConfig loads and validates repository configuration from a YAML file.
// It accepts a config-file path and returns a Config with trimmed names, local paths resolved
// relative to that file, or an error for unreadable or oversized input, multiple YAML documents,
// no document, too many repositories, unsafe names, ambiguous Windows paths, unknown fields, or
// other invalid repository entries.
func loadAndValidateConfig(configFile string) (*Config, error) {
	if err := rejectWindowsAlternateDataStream(configFile); err != nil {
		return nil, err
	}
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return nil, fmt.Errorf("error getting absolute path for %s: %w", configFile, err)
	}

	// Reject non-regular paths before opening so a directory, device, or named pipe cannot be
	// mistaken for configuration input or block while being read.
	pathInfo, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}
	if !pathInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("config file must be a regular file: %s", configFile)
	}
	if pathInfo.Size() > maxConfigFileBytes {
		return nil, fmt.Errorf("config file is too large: %d bytes (maximum %d bytes): %s", pathInfo.Size(), maxConfigFileBytes, configFile)
	}

	configHandle, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}
	defer configHandle.Close()

	// Check the opened file again in case the path changed after the preflight stat.
	fileInfo, err := configHandle.Stat()
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}
	if !fileInfo.Mode().IsRegular() {
		return nil, fmt.Errorf("config file must be a regular file: %s", configFile)
	}

	// Read at most one byte beyond the limit, then reject anything that actually exceeded it.
	// This bounds memory even when the file grows while it is being read.
	yamlFile, err := io.ReadAll(io.LimitReader(configHandle, maxConfigFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}
	if len(yamlFile) > maxConfigFileBytes {
		return nil, fmt.Errorf("config file is too large: more than %d bytes (maximum %d bytes): %s", maxConfigFileBytes, maxConfigFileBytes, configFile)
	}

	var config Config
	// Reject misspelled fields instead of silently applying defaults to incomplete configuration.
	decoder := yaml.NewDecoder(bytes.NewReader(yamlFile))
	decoder.KnownFields(true)
	err = decoder.Decode(&config)
	if err != nil {
		// yaml.v3 reports io.EOF when the file contains no document, which needs a clear message.
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("config file contains no repositories: %s", configFile)
		}
		return nil, fmt.Errorf("error unmarshalling yaml: %w", err)
	}

	// A second document after a --- separator would otherwise be silently ignored.
	var extraDocument Config
	if err := decoder.Decode(&extraDocument); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, fmt.Errorf("error unmarshalling yaml: %w", err)
		}
		return nil, fmt.Errorf("config file must contain exactly one YAML document: %s", configFile)
	}

	// A null document (for example a file containing only "---") decodes without error but defines nothing.
	if len(config.Repos) == 0 {
		return nil, fmt.Errorf("config file contains no repositories: %s", configFile)
	}
	if len(config.Repos) > maxConfiguredRepos {
		return nil, fmt.Errorf("config file defines %d repositories; the maximum is %d: %s", len(config.Repos), maxConfiguredRepos, configFile)
	}

	// Track names case-insensitively while validating so repository selectors stay unambiguous.
	names := make(map[string]bool)
	configDir := filepath.Dir(absPath)

	// Validate repository identities and normalize fields needed by subsequent commands.
	for i := range config.Repos {
		repo := &config.Repos[i]

		// Trim before storing so surrounding whitespace cannot make selectors or paths unusable.
		repo.Name = strings.TrimSpace(repo.Name)
		repo.Location = strings.TrimSpace(repo.Location)

		if repo.Name == "" {
			return nil, fmt.Errorf("repository has an empty name")
		}
		// A comma would split one name into selectors, and control characters could corrupt or
		// spoof terminal output, so neither can be represented safely in a repository name.
		if strings.ContainsRune(repo.Name, ',') {
			return nil, fmt.Errorf("repository name must not contain a comma: %q", repo.Name)
		}
		if containsControlCharacter(repo.Name) {
			return nil, fmt.Errorf("repository name must not contain control characters or bidi formatting characters: %q", repo.Name)
		}
		if names[strings.ToLower(repo.Name)] {
			return nil, fmt.Errorf("duplicate repository name found: %s", repo.Name)
		}
		names[strings.ToLower(repo.Name)] = true

		if repo.Location == "" {
			return nil, fmt.Errorf("repository %q has an empty location", repo.Name)
		}
		if err := validateWindowsLocation(repo.Location); err != nil {
			return nil, fmt.Errorf("repository %q has an invalid location: %w", repo.Name, err)
		}
		if !filepath.IsAbs(repo.Location) {
			repo.Location = filepath.Join(configDir, repo.Location)
		}
	}

	return &config, nil
}

// containsControlCharacter reports whether value contains a control rune or a bidi formatting
// rune that could corrupt or spoof terminal output. It accepts a string and returns true when any
// rune must be rejected from a repository name.
func containsControlCharacter(value string) bool {
	for _, char := range value {
		if unicode.IsControl(char) || unicode.Is(unicode.Bidi_Control, char) {
			return true
		}
	}
	return false
}

// rejectWindowsAlternateDataStream rejects Windows paths that address a named data stream rather
// than an ordinary file or directory. It accepts a path and returns an error only on Windows when
// a colon appears after the volume name.
func rejectWindowsAlternateDataStream(path string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	volume := filepath.VolumeName(path)
	if strings.Contains(path[len(volume):], ":") {
		return fmt.Errorf("alternate data stream paths are not supported: %q", path)
	}
	return nil
}

// validateWindowsLocation rejects Windows paths that name a drive or root without a complete
// absolute location, because resolving them against the config directory would silently point
// somewhere else. It also rejects alternate data stream syntax. It accepts ordinary relative
// paths and complete absolute paths, including UNC shares, and does nothing on other operating
// systems.
func validateWindowsLocation(location string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if err := rejectWindowsAlternateDataStream(location); err != nil {
		return err
	}

	// Go reports a bare UNC host such as \\server as absolute even though it has no share, so
	// check the UNC shape before accepting absolute paths.
	if (strings.HasPrefix(location, `\\`) || strings.HasPrefix(location, `//`)) && !isCompleteUNCPath(location) {
		return fmt.Errorf("incomplete UNC path is not supported: %q", location)
	}
	if filepath.IsAbs(location) {
		return nil
	}
	if filepath.VolumeName(location) != "" {
		return fmt.Errorf("drive-relative path is not supported; use a full absolute path: %q", location)
	}
	if strings.HasPrefix(location, `\`) || strings.HasPrefix(location, "/") {
		return fmt.Errorf("drive-rooted path is not supported; include a drive letter or use a relative path: %q", location)
	}
	return nil
}

// isCompleteUNCPath reports whether a UNC-shaped Windows path includes both a host and a share.
// Device paths such as \\?\C:\folder use their own syntax and are accepted without inspection.
func isCompleteUNCPath(location string) bool {
	if strings.HasPrefix(location, `\\?\`) || strings.HasPrefix(location, `//?/`) ||
		strings.HasPrefix(location, `\\.\`) || strings.HasPrefix(location, `//./`) {
		return true
	}
	parts := strings.FieldsFunc(location[2:], func(char rune) bool {
		return char == '\\' || char == '/'
	})
	return len(parts) >= 2
}
