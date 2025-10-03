package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	listCommand := flag.NewFlagSet("list", flag.ExitOnError)
	execCommand := flag.NewFlagSet("exec", flag.ExitOnError)
	pathCommand := flag.NewFlagSet("path", flag.ExitOnError)

	execRepos := execCommand.String("repos", "", "Comma-separated list of repo positions or names to run the command on")
	execDryRun := execCommand.Bool("dry-run", false, "Show what commands would be executed, without running them")

	switch os.Args[1] {
	case "list":
		listAsJson := listCommand.Bool("json", false, "Output as JSON")
		listCommand.Parse(os.Args[2:])
		if len(listCommand.Args()) != 1 {
			fmt.Fprintf(os.Stderr, "Usage: %s list <config_file>\n", os.Args[0])
			os.Exit(1)
		}
		configFile := listCommand.Args()[0]
		config, err := readConfig(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}

		if *listAsJson {
			type RepoWithIndex struct {
				Index        int    `json:"index"`
				Name         string `json:"name"`
				Location     string `json:"location"`
				LocationType string `json:"locationtype"`
			}

			var reposWithIndex []RepoWithIndex
			for i, repo := range config.Repos {
				reposWithIndex = append(reposWithIndex, RepoWithIndex{
					Index:        i + 1,
					Name:         repo.Name,
					Location:     repo.Location,
					LocationType: repo.LocationType,
				})
			}

			jsonOutput, err := json.MarshalIndent(reposWithIndex, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(string(jsonOutput))
		} else {
			for i, repo := range config.Repos {
				fmt.Printf("%d: %s (%s) (%s)\n", i+1, repo.Name, repo.Location, repo.LocationType)
			}
		}
	case "path":
		pathAsJson := pathCommand.Bool("json", false, "Output as JSON")
		pathCommand.Parse(os.Args[2:])
		if len(pathCommand.Args()) != 2 {
			fmt.Fprintf(os.Stderr, "Usage: %s path <name_or_index> <config_file>\n", os.Args[0])
			os.Exit(1)
		}
		repoIdentifier := pathCommand.Args()[0]
		configFile := pathCommand.Args()[1]
		config, err := readConfig(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}

		repo, err := findRepo(config, repoIdentifier)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}

		if *pathAsJson {
			type PathResult struct {
				Path string `json:"path"`
			}

			result := PathResult{Path: repo.Location}
			jsonOutput, err := json.MarshalIndent(result, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(string(jsonOutput))
		} else {
			fmt.Print(repo.Location)
		}

	case "exec":
		execAsJson := execCommand.Bool("json", false, "Output as JSON")
		execCommand.Parse(os.Args[2:])
		if len(execCommand.Args()) < 2 {
			fmt.Fprintf(os.Stderr, "Usage: %s exec [options] <config_file> <command>\n", os.Args[0])
			execCommand.PrintDefaults()
			os.Exit(1)
		}
		configFile := execCommand.Args()[0]
		command := execCommand.Args()[1:]

		config, err := readConfig(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}

		var targetRepos []Repo
		if *execRepos != "" {
			repoIdentifiers := strings.Split(*execRepos, ",")
			for _, identifier := range repoIdentifiers {
				repo, err := findRepo(config, strings.TrimSpace(identifier))
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					continue
				}
				targetRepos = append(targetRepos, *repo)
			}
		} else {
			targetRepos = config.Repos
		}

		if *execAsJson {
			type ExecResult struct {
				Name   string `json:"name"`
				Output string `json:"output"`
				Error  string `json:"error,omitempty"`
			}

			var results []ExecResult

			for _, repo := range targetRepos {
				if repo.LocationType != "local" {
					results = append(results, ExecResult{
						Name:   repo.Name,
						Output: "Skipped (non-local)",
					})
					continue
				}
				if *execDryRun {
					results = append(results, ExecResult{
						Name:   repo.Name,
						Output: fmt.Sprintf("[DRY RUN] Would execute '%s' in %s", strings.Join(command, " "), repo.Location),
					})
				} else {
					cmd := exec.Command(command[0], command[1:]...)
					cmd.Dir = repo.Location
					out, err := cmd.CombinedOutput()

					result := ExecResult{
						Name:   repo.Name,
						Output: string(out),
					}
					if err != nil {
						result.Error = err.Error()
					}
					results = append(results, result)
				}
			}

			jsonOutput, err := json.MarshalIndent(results, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(string(jsonOutput))
		} else {
			for _, repo := range targetRepos {
				if repo.LocationType != "local" {
					fmt.Printf("Skipping non-local repository: %s\n", repo.Name)
					continue
				}
				if *execDryRun {
					fmt.Printf("[DRY RUN] Would execute '%s' in %s\n", strings.Join(command, " "), repo.Location)
				} else {
					cmd := exec.Command(command[0], command[1:]...)
					cmd.Dir = repo.Location
					out, err := cmd.CombinedOutput()

					fmt.Printf("--- Output for %s ---\n", repo.Name)
					if err != nil {
						fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					}
					fmt.Println(string(out))
				}
			}
		}
	default:
		output := flag.String("o", "", "Output file path")
		asJson := flag.Bool("json", false, "Output as JSON")
		flag.Parse()

		if len(flag.Args()) == 0 {
			printUsage()
			os.Exit(1)
		}
		configFile := flag.Args()[0]

		var writer io.Writer
		if *output != "" {
			f, err := os.Create(*output)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
				os.Exit(1)
			}
			defer f.Close()
			writer = f
		} else {
			writer = os.Stdout
		}

		config, err := readConfig(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}

		if *asJson {
			type RepoStatus struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			}

			var statuses []RepoStatus

			for _, repo := range config.Repos {
				if repo.LocationType != "local" {
					statuses = append(statuses, RepoStatus{
						Name:   repo.Name,
						Status: "Skipped (non-local)",
					})
					continue
				}
				cmd := exec.Command("git", "status")
				cmd.Dir = repo.Location
				out, err := cmd.CombinedOutput()

				status := RepoStatus{
					Name:   repo.Name,
					Status: string(out),
				}
				if err != nil {
					status.Error = err.Error()
				}
				statuses = append(statuses, status)
			}

			jsonOutput, err := json.MarshalIndent(statuses, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Fprint(writer, string(jsonOutput))

		} else {
			for _, repo := range config.Repos {
				if repo.LocationType != "local" {
					fmt.Fprintf(writer, "--- Skipping non-local repository: %s ---\n", repo.Name)
					continue
				}
				cmd := exec.Command("git", "status")
				cmd.Dir = repo.Location
				out, err := cmd.CombinedOutput()

				fmt.Fprintf(writer, "--- Git status for %s ---\n", repo.Name)
				if err != nil {
					fmt.Fprintf(writer, "Error: %v\n", err)
				}
				fmt.Fprintln(writer, string(out))
			}
		}
	}
}

func findRepo(config *Config, identifier string) (*Repo, error) {
	// Try to parse as index first
	if index, err := strconv.Atoi(identifier); err == nil {
		if index < 1 || index > len(config.Repos) {
			return nil, fmt.Errorf("index out of range: %d", index)
		}
		return &config.Repos[index-1], nil
	}

	// Otherwise, search by name
	for _, repo := range config.Repos {
		if repo.Name == identifier {
			return &repo, nil
		}
	}

	return nil, fmt.Errorf("repository not found: %s", identifier)
}

func printUsage() {
	fmt.Fprintf(os.Stderr, "Usage: %s <command> [options] <config_file>\n", os.Args[0])
	fmt.Fprintln(os.Stderr, "Commands:")
	fmt.Fprintln(os.Stderr, "  list: List repositories and their positions")
	fmt.Fprintln(os.Stderr, "  path: Get the path of a repository at a given index or name")
	fmt.Fprintln(os.Stderr, "  exec: Execute a command in repository directories")
	fmt.Fprintln(os.Stderr, "  (default): Show git status for all repositories")
}