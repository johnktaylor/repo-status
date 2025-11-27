package main

import (
	"fmt"
	"io/ioutil"
	"path/filepath"

	"gopkg.in/yaml.v2"
)

type Repo struct {
	Location     string `yaml:"location"`
	LocationType string `yaml:"locationtype"`
	Name         string `yaml:"name"`
}

type Config struct {
	Repos []Repo `yaml:"repositories"`
}

func readConfig(configFile string) (*Config, error) {
	absPath, err := filepath.Abs(configFile)
	if err != nil {
		return nil, fmt.Errorf("error getting absolute path for %s: %w", configFile, err)
	}

	yamlFile, err := ioutil.ReadFile(absPath)
	if err != nil {
		return nil, fmt.Errorf("error reading config file: %w", err)
	}

	var config Config
	err = yaml.Unmarshal(yamlFile, &config)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling yaml: %w", err)
	}

	// Check for duplicate names
	names := make(map[string]bool)
	configDir := filepath.Dir(absPath)

	for i := range config.Repos {
		repo := &config.Repos[i]
		if names[repo.Name] {
			return nil, fmt.Errorf("duplicate repository name found: %s", repo.Name)
		}
		names[repo.Name] = true

		// Set default location type
		if repo.LocationType == "" {
			repo.LocationType = "local"
		}

		// Resolve relative paths for local repos
		if repo.LocationType == "local" {
			if !filepath.IsAbs(repo.Location) {
				repo.Location = filepath.Join(configDir, repo.Location)
			}
		}
	}

	return &config, nil
}
