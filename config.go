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
	for _, repo := range config.Repos {
		if names[repo.Name] {
			return nil, fmt.Errorf("duplicate repository name found: %s", repo.Name)
		}
		names[repo.Name] = true
	}

	// Set default location type
	for i := range config.Repos {
		if config.Repos[i].LocationType == "" {
			config.Repos[i].LocationType = "local"
		}
	}

	return &config, nil
}
