
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// Color constants
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
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
	execAsync := execCommand.Bool("async", false, "Run commands in parallel")

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
			// JSON output doesn't support streaming/async well in this structure, so we collect results.
			// Async execution for JSON output is still useful for speed.
			type ExecResult struct {
				Name   string `json:"name"`
				Output string `json:"output"`
				Error  string `json:"error,omitempty"`
			}

			results := make([]ExecResult, len(targetRepos))
			var wg sync.WaitGroup

			for i, repo := range targetRepos {
				if repo.LocationType != "local" {
					results[i] = ExecResult{
						Name:   repo.Name,
						Output: "Skipped (non-local)",
					}
					continue
				}

				if *execDryRun {
					results[i] = ExecResult{
						Name:   repo.Name,
						Output: fmt.Sprintf("[DRY RUN] Would execute '%s' in %s", strings.Join(command, " "), repo.Location),
					}
					continue
				}

				execute := func(idx int, r Repo) {
					cmd := exec.Command(command[0], command[1:]...)
					cmd.Dir = r.Location
					out, err := cmd.CombinedOutput()

					result := ExecResult{
						Name:   r.Name,
						Output: string(out),
					}
					if err != nil {
						result.Error = err.Error()
					}
					results[idx] = result
				}

				if *execAsync {
					wg.Add(1)
					go func(idx int, r Repo) {
						defer wg.Done()
						execute(idx, r)
					}(i, repo)
				} else {
					execute(i, repo)
				}
			}

			if *execAsync {
				wg.Wait()
			}

			// Filter out empty results (if any logic skipped index population, though current logic covers all)
			var finalResults []ExecResult
			for _, res := range results {
				if res.Name != "" {
					finalResults = append(finalResults, res)
				}
			}

			jsonOutput, err := json.MarshalIndent(finalResults, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(string(jsonOutput))

		} else {
			// Text output
			var wg sync.WaitGroup
			// Mutex to prevent interleaved output
			var outputMutex sync.Mutex

			for _, repo := range targetRepos {
				if repo.LocationType != "local" {
					fmt.Printf("Skipping non-local repository: %s\n", repo.Name)
					continue
				}

				if *execDryRun {
					fmt.Printf("[DRY RUN] Would execute '%s' in %s\n", strings.Join(command, " "), repo.Location)
					continue
				}

				execute := func(r Repo) {
					cmd := exec.Command(command[0], command[1:]...)
					cmd.Dir = r.Location
					out, err := cmd.CombinedOutput()

					// Buffer output to print atomically
					var buf bytes.Buffer
					fmt.Fprintf(&buf, "--- Output for %s ---\n", r.Name)
					if err != nil {
						fmt.Fprintf(&buf, "Error: %v\n", err)
					}
					fmt.Fprintln(&buf, string(out))

					if *execAsync {
						outputMutex.Lock()
						defer outputMutex.Unlock()
					}
					fmt.Print(buf.String())
				}

				if *execAsync {
					wg.Add(1)
					go func(r Repo) {
						defer wg.Done()
						execute(r)
					}(repo)
				} else {
					execute(repo)
				}
			}

			if *execAsync {
				wg.Wait()
			}
		}
	default:
		output := flag.String("o", "", "Output file path")
		asJson := flag.Bool("json", false, "Output as JSON")
		shortStatus := flag.Bool("short", false, "Use short status format")
		dirtyOnly := flag.Bool("dirty", false, "Only show repositories with changes")
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
				
				args := []string{"status"}
				if *shortStatus {
					args = append(args, "-s")
				}
				
				cmd := exec.Command("git", args...)
				cmd.Dir = repo.Location
				out, err := cmd.CombinedOutput()

				statusStr := string(out)
				if *dirtyOnly && strings.TrimSpace(statusStr) == "" {
					continue
				}

				status := RepoStatus{
					Name:   repo.Name,
					Status: statusStr,
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
				
				args := []string{"status"}
				if *shortStatus {
					args = append(args, "-s")
				}

				cmd := exec.Command("git", args...)
				cmd.Dir = repo.Location
				out, err := cmd.CombinedOutput()
				
				outputStr := string(out)
				isClean := strings.TrimSpace(outputStr) == ""

				if *dirtyOnly && isClean {
					continue
				}

				// Colorize header
				headerColor := ColorBlue
				if !isClean {
					headerColor = ColorYellow
				}
				if err != nil {
					headerColor = ColorRed
				}

				// Only use colors if writing to stdout
				if writer == os.Stdout {
					fmt.Fprintf(writer, "%s--- Git status for %s ---%s\n", headerColor, repo.Name, ColorReset)
				} else {
					fmt.Fprintf(writer, "--- Git status for %s ---\n", repo.Name)
				}
				
				if err != nil {
					fmt.Fprintf(writer, "Error: %v\n", err)
				}
				fmt.Fprintln(writer, outputStr)
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
	fmt.Fprintln(os.Stderr, "\nDefault Command Options:")
	fmt.Fprintln(os.Stderr, "  --short: Use short status format")
	fmt.Fprintln(os.Stderr, "  --dirty: Only show repositories with changes")
	fmt.Fprintln(os.Stderr, "  -o <file>: Output to file")
	fmt.Fprintln(os.Stderr, "  --json: Output as JSON")
}
