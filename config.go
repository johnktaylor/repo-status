package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type Repo struct {
	Location string `yaml:"location"`
	Name     string `yaml:"name"`
}

type Config struct {
	Repos []Repo `yaml:"repositories"`
}

// readConfig loads and validates repository configuration from a YAML file.
// It accepts a config-file path and returns a Config with local paths resolved relative to that file, or an error for unreadable or invalid input.
func readConfig(configFile string) (*Config, error) {
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return nil, fmt.Errorf("error getting absolute path for %s: %w", configFile, err)
	}

	yamlFile, err := os.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var config Config
	// Reject misspelled fields instead of silently applying defaults to incomplete configuration.
	decoder := yaml.NewDecoder(bytes.NewReader(yamlFile))
	decoder.KnownFields(true)
	err = decoder.Decode(&config)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling yaml: %w", err)
	}

	// Check for duplicate names
	names := make(map[string]bool)
	configDir := filepath.Dir(absPath)

	// Validate repository identities and normalize fields needed by subsequent commands.
	for i := range config.Repos {
		repo := &config.Repos[i]
		if strings.TrimSpace(repo.Name) == "" {
			return nil, fmt.Errorf("repository has an empty name")
		}
		if names[repo.Name] {
			return nil, fmt.Errorf("duplicate repository name found: %s", repo.Name)
		}
		names[repo.Name] = true

		if strings.TrimSpace(repo.Location) == "" {
			return nil, fmt.Errorf("repository %q has an empty location", repo.Name)
		}
		if !filepath.IsAbs(repo.Location) {
			repo.Location = filepath.Join(configDir, repo.Location)
		}
	}

	return &config, nil
}
