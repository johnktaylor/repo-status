package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Color constants
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
)

// main parses command-line arguments and runs the selected repository operation.
// It reads os.Args and configuration files, writes results to standard streams or an output file,
// and exits non-zero for invalid input, configuration errors, or failed status and exec commands.
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

	// Dispatch to the requested subcommand; an unrecognized first argument uses the default status view.
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
				Index    int    `json:"index"`
				Name     string `json:"name"`
				Location string `json:"location"`
			}

			// Convert configured repositories into records that preserve their one-based CLI positions.
			reposWithIndex := make([]RepoWithIndex, 0, len(config.Repos))
			for i, repo := range config.Repos {
				reposWithIndex = append(reposWithIndex, RepoWithIndex{
					Index:    i + 1,
					Name:     repo.Name,
					Location: repo.Location,
				})
			}

			jsonOutput, err := json.MarshalIndent(reposWithIndex, "", "  ")
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error marshalling JSON: %v\n", err)
				os.Exit(1)
			}
			fmt.Print(string(jsonOutput))
		} else {
			// Render the configured repositories in their selection order.
			for i, repo := range config.Repos {
				fmt.Printf("%d: %s (%s)\n", i+1, repo.Name, repo.Location)
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
			// Resolve every requested selector before executing so a typo cannot cause a partial run.
			repoIdentifiers := strings.Split(*execRepos, ",")
			selectedRepos := make(map[string]bool)
			for _, identifier := range repoIdentifiers {
				repo, err := findRepo(config, strings.TrimSpace(identifier))
				if err != nil {
					fmt.Fprintf(os.Stderr, "Error: %v\n", err)
					os.Exit(1)
				}
				// Run each repository at most once even when selectors overlap.
				if selectedRepos[repo.Name] {
					continue
				}
				selectedRepos[repo.Name] = true
				targetRepos = append(targetRepos, *repo)
			}
		} else {
			// With no explicit selection, run against every configured repository.
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
			var hadFailure atomic.Bool

			// Execute local repositories and retain one ordered result per target for JSON output.
			for i, repo := range targetRepos {
				if *execDryRun {
					results[i] = ExecResult{
						Name:   repo.Name,
						Output: fmt.Sprintf("[DRY RUN] Would execute '%s' in %s", strings.Join(command, " "), repo.Location),
					}
					continue
				}

				// Run the supplied command in the repository and capture combined output for its result.
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
						hadFailure.Store(true)
					}
					results[idx] = result
				}

				if *execAsync {
					// Run independent repository commands concurrently while preserving their result indexes.
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

			// Omit any result slots that were not populated before serializing the response.
			finalResults := make([]ExecResult, 0, len(results))
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
			if hadFailure.Load() {
				os.Exit(1)
			}

		} else {
			// Stream human-readable output, optionally running repositories concurrently.
			var wg sync.WaitGroup
			// Mutex to prevent interleaved output
			var outputMutex sync.Mutex
			var hadFailure atomic.Bool

			// Execute each selected repository.
			for _, repo := range targetRepos {
				if *execDryRun {
					fmt.Printf("[DRY RUN] Would execute '%s' in %s\n", strings.Join(command, " "), repo.Location)
					continue
				}

				// Run the command and buffer its output so a repository's report is printed atomically.
				execute := func(r Repo) {
					cmd := exec.Command(command[0], command[1:]...)
					cmd.Dir = r.Location
					out, err := cmd.CombinedOutput()

					// Buffer output to print atomically
					var buf bytes.Buffer
					fmt.Fprintf(&buf, "--- Output for %s ---\n", r.Name)
					if err != nil {
						hadFailure.Store(true)
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
					// Start independent repository commands concurrently.
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
			if hadFailure.Load() {
				os.Exit(1)
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

		config, err := readConfig(configFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error reading config file: %v\n", err)
			os.Exit(1)
		}

		// Buffer the full report so the final write and file close errors can be reported reliably.
		var report bytes.Buffer
		writer := io.Writer(&report)
		var outputFile *os.File
		if *output != "" {
			f, err := os.Create(*output)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Error creating output file: %v\n", err)
				os.Exit(1)
			}
			outputFile = f
		}

		// Preserve a failing exit status after reporting every repository's status.
		hadStatusFailure := false
		if *asJson {
			type RepoStatus struct {
				Name   string `json:"name"`
				Status string `json:"status"`
				Error  string `json:"error,omitempty"`
			}

			statuses := make([]RepoStatus, 0, len(config.Repos))

			// Query each repository and build a structured status response.
			for _, repo := range config.Repos {
				args := []string{"status"}
				if *shortStatus {
					args = append(args, "-s")
				}

				cmd := exec.Command("git", args...)
				cmd.Dir = repo.Location
				out, err := cmd.CombinedOutput()

				statusStr := string(out)
				if err == nil {
					dirty, dirtyErr := isRepoDirty(repo.Location)
					if dirtyErr != nil {
						err = dirtyErr
					} else if *dirtyOnly && !dirty {
						continue
					}
				}
				if err != nil {
					hadStatusFailure = true
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
			// Query and render status for each repository in configuration order.
			for _, repo := range config.Repos {
				args := []string{"status"}
				if *shortStatus {
					args = append(args, "-s")
				}

				cmd := exec.Command("git", args...)
				cmd.Dir = repo.Location
				out, err := cmd.CombinedOutput()

				outputStr := string(out)
				isClean := false
				if err == nil {
					dirty, dirtyErr := isRepoDirty(repo.Location)
					if dirtyErr != nil {
						err = dirtyErr
					} else {
						isClean = !dirty
					}
				}

				if err != nil {
					hadStatusFailure = true
				}
				// Keep failures visible even when clean repositories are omitted.
				if *dirtyOnly && err == nil && isClean {
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

				// Only use colors when standard output is an interactive terminal.
				if outputFile == nil && isTerminal(os.Stdout) {
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
		// Flush the complete report and surface write or close failures to the caller.
		destination := io.Writer(os.Stdout)
		if outputFile != nil {
			destination = outputFile
		}
		if _, err := io.Copy(destination, &report); err != nil {
			if outputFile != nil {
				_ = outputFile.Close()
			}
			fmt.Fprintf(os.Stderr, "Error writing status output: %v\n", err)
			os.Exit(1)
		}
		if outputFile != nil {
			if err := outputFile.Close(); err != nil {
				fmt.Fprintf(os.Stderr, "Error closing status output: %v\n", err)
				os.Exit(1)
			}
		}
		if hadStatusFailure {
			os.Exit(1)
		}
	}
}

// isTerminal reports whether file is attached to an interactive character device.
// It accepts an open file and returns false when its metadata cannot be read or it is redirected.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

// isRepoDirty checks Git's porcelain status for uncommitted or untracked changes.
// It accepts a local repository directory and returns whether it is dirty, or an error when Git cannot inspect the directory.
func isRepoDirty(location string) (bool, error) {
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = location
	out, err := cmd.CombinedOutput()
	if err != nil {
		return false, fmt.Errorf("checking dirty status: %w: %s", err, strings.TrimSpace(string(out)))
	}

	return strings.TrimSpace(string(out)) != "", nil
}

// findRepo resolves a repository in config by one-based index or exact name.
// It accepts a non-nil Config and selector string, and returns the matching Repo or an error when no valid match exists.
func findRepo(config *Config, identifier string) (*Repo, error) {
	// Try to parse as index first
	if index, err := strconv.Atoi(identifier); err == nil {
		if index >= 1 && index <= len(config.Repos) {
			return &config.Repos[index-1], nil
		}
	}

	// Search by name so numeric names remain usable when they are not valid indexes.
	for _, repo := range config.Repos {
		if repo.Name == identifier {
			return &repo, nil
		}
	}

	// Preserve a clear index error when an out-of-range numeric selector is not a repository name.
	if index, err := strconv.Atoi(identifier); err == nil {
		return nil, fmt.Errorf("index out of range: %d", index)
	}

	return nil, fmt.Errorf("repository not found: %s", identifier)
}

// printUsage writes command syntax and supported options to standard error.
// It takes no inputs and returns no value; callers use it before exiting on invalid invocation.
func printUsage() {
	programName := filepath.Base(os.Args[0])
	fmt.Fprintf(os.Stderr, `Usage:
  %[1]s [--short] [--dirty] [-o <output_file>] [--json] <config_file>
  %[1]s list [--json] <config_file>
  %[1]s path [--json] <name_or_index> <config_file>
  %[1]s exec [--repos <indexes_or_names>] [--async] [--dry-run] [--json] <config_file> <command> [command_arguments...]

Options must appear before positional arguments.

Commands:
  (default)  Show Git status for every configured repository.
  list       List repositories and their one-based indexes.
  path       Print a repository path by its one-based index or exact name.
  exec       Run a command in each selected repository directory.

Default status options:
  --short              Use Git's short status format.
  --dirty              Show only repositories with uncommitted or untracked changes.
  -o <output_file>     Write the report to a file.
  --json               Emit JSON output.

Exec options:
  --repos <selectors>  Comma-separated indexes or names, for example: 1,api
  --async              Run commands in parallel.
  --dry-run            Show commands without running them.
  --json               Emit JSON output.

Examples:
  %[1]s repos.yaml
  %[1]s --short --dirty repos.yaml
  %[1]s list --json repos.yaml
  %[1]s path 1 repos.yaml
  %[1]s path api repos.yaml
  %[1]s exec repos.yaml git status
  %[1]s exec --async repos.yaml git fetch
  %[1]s exec --repos 1,api repos.yaml git pull
`, programName)
}
