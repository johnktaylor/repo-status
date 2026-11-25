package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	"time"
)

// Color constants
const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
)

// maxConcurrentExec bounds how many commands --async runs in parallel so many
// configured repositories cannot exhaust file descriptors or overwhelm remotes.
const maxConcurrentExec = 8

// gitStatusTimeout and gitStatusWaitDelay bound one Git status query. Both are variables so tests
// can shorten them; exec commands deliberately have no deadline because long-running intentional
// commands are supported.
var (
	gitStatusTimeout   = 30 * time.Second
	gitStatusWaitDelay = 5 * time.Second
)

// main parses command-line arguments and runs the selected repository operation.
// It reads os.Args and delegates to a subcommand handler, exiting with the handler's
// non-zero code for invalid input, configuration errors, or failed status and exec commands.
func main() {
	if len(os.Args) < 2 {
		printUsage(os.Stderr)
		os.Exit(1)
	}

	// Dispatch to the requested subcommand; an unrecognized first argument uses the default status view.
	var exitCode int
	switch os.Args[1] {
	case "list":
		exitCode = runList(os.Args[2:], os.Stdout, os.Stderr)
	case "path":
		exitCode = runPath(os.Args[2:], os.Stdout, os.Stderr)
	case "exec":
		exitCode = runExec(os.Args[2:], os.Stdout, os.Stderr)
	default:
		exitCode = runStatus(os.Args[1:], os.Stdout, os.Stderr)
	}
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// runList implements the "list" subcommand that prints configured repositories with their one-based indexes.
// It accepts the command-line arguments after "list" plus output and error streams, and returns the
// process exit code: non-zero for invalid usage, configuration errors, failed output writes, or a
// rendered-output budget check failure.
func runList(args []string, stdout io.Writer, stderr io.Writer) int {
	listCommand := flag.NewFlagSet("list", flag.ExitOnError)
	listAsJSON := listCommand.Bool("json", false, "Output as JSON")
	listCommand.Parse(args)
	if len(listCommand.Args()) != 1 {
		fmt.Fprintf(stderr, "Usage: %s list <config_file>\n", os.Args[0])
		return 1
	}
	configFile := listCommand.Args()[0]
	config, err := loadAndValidateConfig(configFile)
	if err != nil {
		writeDisplayError(stderr, "Error reading config file: ", err)
		return 1
	}

	if *listAsJSON {
		type RepoWithIndex struct {
			Index    int    `json:"index"`
			Name     string `json:"name"`
			Location string `json:"location"`
		}

		// Encode one record at a time so the array never requires a full-size JSON buffer.
		render := func(writer io.Writer) error {
			return writeJSONArray(writer, len(config.Repos), func(index int) ([]byte, error) {
				repo := config.Repos[index]
				return marshalIndentedRecord(RepoWithIndex{
					Index:    index + 1,
					Name:     repo.Name,
					Location: repo.Location,
				})
			})
		}
		// A preflight count avoids emitting a malformed partial array on budget rejection.
		if err := render(newRenderLimitWriter(io.Discard)); err != nil {
			writeDisplayError(stderr, "Error writing list output: ", err)
			return 1
		}
		if err := render(newRenderLimitWriter(stdout)); err != nil {
			writeDisplayError(stderr, "Error writing list output: ", err)
			return 1
		}
		return 0
	}

	// Render the configured repositories in their selection order, escaping any terminal control
	// characters that a hand-edited configuration could contain.
	for i, repo := range config.Repos {
		if _, err := fmt.Fprintf(stdout, "%d: %s (%s)\n", i+1, escapeDisplay(repo.Name), escapeDisplay(repo.Location)); err != nil {
			writeDisplayError(stderr, "Error writing list output: ", err)
			return 1
		}
	}
	return 0
}

// runPath implements the "path" subcommand that prints one repository's local path.
// It accepts command-line arguments after "path" plus output and error streams, and returns the
// process exit code: non-zero for invalid usage, configuration errors, an unknown selector, or a
// failed output write. --name forces the selector to be interpreted as an exact label, while the
// plain form stays byte-exact for non-terminal consumers.
func runPath(args []string, stdout io.Writer, stderr io.Writer) int {
	pathCommand := flag.NewFlagSet("path", flag.ExitOnError)
	pathAsJSON := pathCommand.Bool("json", false, "Output as JSON")
	pathByName := pathCommand.Bool("name", false, "Interpret the selector as an exact repository name, even when numeric or starting with name:")
	pathCommand.Parse(args)
	if len(pathCommand.Args()) != 2 {
		fmt.Fprintf(stderr, "Usage: %s path [--name] [--json] <selector> <config_file>\n", os.Args[0])
		return 1
	}
	repoIdentifier := pathCommand.Args()[0]
	configFile := pathCommand.Args()[1]
	config, err := loadAndValidateConfig(configFile)
	if err != nil {
		writeDisplayError(stderr, "Error reading config file: ", err)
		return 1
	}

	var repo *Repo
	if *pathByName {
		repo, err = findRepoByName(config, repoIdentifier)
	} else {
		repo, err = findRepo(config, repoIdentifier)
	}
	if err != nil {
		writeDisplayError(stderr, "Error: ", err)
		return 1
	}

	if *pathAsJSON {
		type PathResult struct {
			Path string `json:"path"`
		}

		result := PathResult{Path: repo.Location}
		jsonOutput, err := json.MarshalIndent(result, "", "  ")
		if err != nil {
			writeDisplayError(stderr, "Error marshalling JSON: ", err)
			return 1
		}
		if _, err := io.WriteString(stdout, string(jsonOutput)); err != nil {
			writeDisplayError(stderr, "Error writing path output: ", err)
			return 1
		}
		return 0
	}

	// Keep the plain form byte-exact for non-interactive consumers such as command substitution.
	// Escape only when writing to a terminal, where unsafe characters could alter the display.
	pathText := repo.Location
	if terminalFile, ok := stdout.(*os.File); ok && isTerminal(terminalFile) {
		pathText = escapeDisplay(pathText)
	}
	if _, err := io.WriteString(stdout, pathText); err != nil {
		writeDisplayError(stderr, "Error writing path output: ", err)
		return 1
	}
	return 0
}

// resolveTargetRepos selects the repositories for exec from a comma-separated selector list.
// It accepts a non-nil Config and the --repos value, and returns the selected repositories in
// the order the selectors are given (configuration order when no selectors are supplied), or an
// error for an empty or unknown selector. Bare selectors keep index precedence and treat a
// name: prefix literally; use resolveNamedRepos for exact-label selection.
func resolveTargetRepos(config *Config, selectors string) ([]Repo, error) {
	if selectors == "" {
		// With no explicit selection, run against every configured repository.
		return config.Repos, nil
	}

	// Resolve every requested selector before executing so a typo cannot cause a partial run.
	selectedRepos := make(map[string]bool)
	var targetRepos []Repo
	for _, rawIdentifier := range strings.Split(selectors, ",") {
		identifier := strings.TrimSpace(rawIdentifier)
		if identifier == "" {
			return nil, fmt.Errorf("empty repository selector")
		}
		repo, err := findRepo(config, identifier)
		if err != nil {
			return nil, err
		}
		// Run each repository at most once even when selectors overlap.
		if selectedRepos[repo.Name] {
			continue
		}
		selectedRepos[repo.Name] = true
		targetRepos = append(targetRepos, *repo)
	}
	return targetRepos, nil
}

// resolveNamedRepos selects repositories from a comma-separated list of exact labels.
// It accepts a non-nil Config and raw --names value, and returns repositories in selector order
// without numeric-index interpretation, or an error for an empty or unknown label.
func resolveNamedRepos(config *Config, labels string) ([]Repo, error) {
	selectedRepos := make(map[string]bool)
	var targetRepos []Repo
	for _, rawLabel := range strings.Split(labels, ",") {
		label := strings.TrimSpace(rawLabel)
		if label == "" {
			return nil, fmt.Errorf("empty repository name")
		}
		repo, err := findRepoByName(config, label)
		if err != nil {
			return nil, err
		}
		// Run each repository at most once even when labels overlap.
		if selectedRepos[repo.Name] {
			continue
		}
		selectedRepos[repo.Name] = true
		targetRepos = append(targetRepos, *repo)
	}
	return targetRepos, nil
}

// runExec implements the "exec" subcommand that runs a command in selected repository directories.
// It accepts command-line arguments after "exec" plus output and error streams, and returns the
// process exit code: non-zero for invalid usage, configuration errors, an empty, invalid, or
// contradictory repository selection, a failed output write, any command failure, or truncated
// command output. It deliberately applies no deadline to user commands.
func runExec(args []string, stdout io.Writer, stderr io.Writer) int {
	execCommand := flag.NewFlagSet("exec", flag.ExitOnError)
	execRepos := execCommand.String("repos", "", "Comma-separated repository indexes or names")
	execNames := execCommand.String("names", "", "Comma-separated exact repository names; mutually exclusive with --repos")
	execDryRun := execCommand.Bool("dry-run", false, "Show what commands would be executed, without running them")
	execAsync := execCommand.Bool("async", false, "Run commands in parallel")
	execAsJSON := execCommand.Bool("json", false, "Output as JSON")
	execCommand.Parse(args)
	if len(execCommand.Args()) < 2 {
		fmt.Fprintf(stderr, "Usage: %s exec [options] <config_file> <command>\n", os.Args[0])
		execCommand.PrintDefaults()
		return 1
	}
	configFile := execCommand.Args()[0]
	command := execCommand.Args()[1:]

	// Both selection switches are opt-in and mutually exclusive. Check this before loading the
	// configuration or starting any command so a contradictory invocation has no side effects.
	reposProvided := false
	namesProvided := false
	execCommand.Visit(func(visited *flag.Flag) {
		switch visited.Name {
		case "repos":
			reposProvided = true
		case "names":
			namesProvided = true
		}
	})
	if reposProvided && namesProvided {
		fmt.Fprintln(stderr, "Error: --repos and --names are mutually exclusive")
		return 1
	}
	if reposProvided && strings.TrimSpace(*execRepos) == "" {
		fmt.Fprintln(stderr, "Error: --repos requires at least one repository selector")
		return 1
	}
	if namesProvided && strings.TrimSpace(*execNames) == "" {
		fmt.Fprintln(stderr, "Error: --names requires at least one repository name")
		return 1
	}

	config, err := loadAndValidateConfig(configFile)
	if err != nil {
		writeDisplayError(stderr, "Error reading config file: ", err)
		return 1
	}

	var targetRepos []Repo
	switch {
	case namesProvided:
		// Resolve every name before executing so a typo cannot cause a partial run.
		targetRepos, err = resolveNamedRepos(config, *execNames)
	case reposProvided:
		// Resolve every requested selector before executing so a typo cannot cause a partial run.
		targetRepos, err = resolveTargetRepos(config, *execRepos)
	default:
		targetRepos = config.Repos
	}
	if err != nil {
		writeDisplayError(stderr, "Error: ", err)
		return 1
	}

	if *execAsJSON {
		return runExecJSON(targetRepos, command, *execDryRun, *execAsync, stdout, stderr)
	}
	return runExecText(targetRepos, command, *execDryRun, *execAsync, stdout, stderr)
}

// execResultRecord is the bounded command result retained for one repository before JSON output.
type execResultRecord struct {
	Name   string `json:"name"`
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

// runExecJSON runs the command against the target repositories and emits one JSON result per repository.
// It accepts the target repositories, the command words, dry-run and async switches, and output
// streams, and returns the process exit code: non-zero when any command, output write, output
// capture truncation, or rendered-output budget check fails.
func runExecJSON(targetRepos []Repo, command []string, dryRun bool, async bool, stdout io.Writer, stderr io.Writer) int {
	var results []execResultRecord
	var hadFailure atomic.Bool

	if !dryRun {
		results = make([]execResultRecord, len(targetRepos))
		// Each worker owns one result index, so concurrent writes keep selection order without
		// sharing a result slot. Combined output is bounded per target.
		captureLimit := captureLimitForTargets(len(targetRepos))
		execute := func(index int) {
			repo := targetRepos[index]
			capture := newBoundedBuffer(captureLimit)
			cmd := exec.Command(command[0], command[1:]...)
			cmd.Dir = repo.Location
			cmd.Stdout = capture
			cmd.Stderr = capture
			runErr := cmd.Run()
			truncated := capture.Truncated()

			result := execResultRecord{
				Name:   repo.Name,
				Output: capture.String(),
			}
			if failure := execFailureText(runErr, truncated, captureLimit); failure != "" {
				result.Error = failure
				hadFailure.Store(true)
			}
			results[index] = result
		}

		if async {
			runBoundedConcurrently(len(targetRepos), execute)
		} else {
			for index := range targetRepos {
				execute(index)
			}
		}
	}

	// Prepare the dry-run command text once; each record formats only its own short output.
	commandText := ""
	if dryRun {
		commandText = strings.Join(command, " ")
	}
	render := func(writer io.Writer) error {
		return writeJSONArray(writer, len(targetRepos), func(index int) ([]byte, error) {
			if dryRun {
				repo := targetRepos[index]
				return marshalIndentedRecord(execResultRecord{
					Name:   repo.Name,
					Output: fmt.Sprintf("[DRY RUN] Would execute '%s' in %s", commandText, repo.Location),
				})
			}
			return marshalIndentedRecord(results[index])
		})
	}
	// A preflight count keeps budget rejection from leaving malformed JSON on stdout.
	if err := render(newRenderLimitWriter(io.Discard)); err != nil {
		writeDisplayError(stderr, "Error writing exec output: ", err)
		return 1
	}
	if err := render(newRenderLimitWriter(stdout)); err != nil {
		writeDisplayError(stderr, "Error writing exec output: ", err)
		return 1
	}
	if hadFailure.Load() {
		return 1
	}
	return 0
}

// runExecText runs the command against the target repositories and streams human-readable output.
// It accepts the target repositories, the command words, dry-run and async switches, output and
// error streams, and returns the process exit code: non-zero when any command, output write,
// output capture truncation, or rendered-output budget check fails.
func runExecText(targetRepos []Repo, command []string, dryRun bool, async bool, stdout io.Writer, stderr io.Writer) int {
	// The mutex keeps each repository's report intact and also guards writeFailure, because
	// asynchronous workers can write at the same time. A rendered-output limit bounds the total
	// text stream even when repeated dry-run command text is long.
	var outputMutex sync.Mutex
	var writeFailure error
	var hadFailure atomic.Bool
	limitedStdout := newRenderLimitWriterWithLimit(stdout, maxExecTextBytesPerRun)

	// writeBlock prints one complete report and records the first output error instead of
	// allowing repeated failures to interleave.
	writeBlock := func(block string) {
		outputMutex.Lock()
		defer outputMutex.Unlock()
		if writeFailure != nil {
			return
		}
		if _, err := io.WriteString(limitedStdout, block); err != nil {
			writeFailure = err
		}
	}

	// Run the command and buffer its bounded output so a repository's report is printed
	// atomically.
	captureLimit := captureLimitForTargets(len(targetRepos))
	execute := func(repo Repo) {
		capture := newBoundedBuffer(captureLimit)
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Dir = repo.Location
		cmd.Stdout = capture
		cmd.Stderr = capture
		runErr := cmd.Run()
		truncated := capture.Truncated()

		var buf bytes.Buffer
		fmt.Fprintf(&buf, "--- Output for %s ---\n", escapeDisplay(repo.Name))
		if failure := execFailureText(runErr, truncated, captureLimit); failure != "" {
			hadFailure.Store(true)
			fmt.Fprintf(&buf, "Error: %s\n", escapeDisplay(failure))
		}
		// Command output is deliberately passed through because the user chose the command.
		fmt.Fprintln(&buf, capture.String())
		writeBlock(buf.String())
	}

	if dryRun {
		// Prepare the command text once and escape it at this text display boundary.
		commandText := escapeDisplay(strings.Join(command, " "))
		for _, repo := range targetRepos {
			writeBlock(fmt.Sprintf("[DRY RUN] Would execute '%s' in %s\n", commandText, escapeDisplay(repo.Location)))
		}
	} else if async {
		// A fixed worker pool bounds concurrency regardless of how many repositories are selected.
		runBoundedConcurrently(len(targetRepos), func(index int) {
			execute(targetRepos[index])
		})
	} else {
		for _, repo := range targetRepos {
			execute(repo)
		}
	}

	outputMutex.Lock()
	err := writeFailure
	outputMutex.Unlock()
	if err != nil {
		writeDisplayError(stderr, "Error writing exec output: ", err)
		return 1
	}
	if hadFailure.Load() {
		return 1
	}
	return 0
}

// runStatus implements the default command that reports Git status for every configured repository.
// It accepts command-line arguments (including option flags), output and error streams, and returns
// the process exit code: non-zero for invalid usage, configuration errors, output failures, a Git
// timeout or output truncation, or any repository status failure. With -o, the report is collected
// first and then published to a new file without overwriting an existing path.
func runStatus(args []string, stdout io.Writer, stderr io.Writer) int {
	statusCommand := flag.NewFlagSet("status", flag.ExitOnError)
	outputPath := statusCommand.String("o", "", "Write the report to a new file (never overwrites)")
	asJSON := statusCommand.Bool("json", false, "Output as JSON")
	// --short is kept so existing invocations do not break; porcelain output is always short.
	statusCommand.Bool("short", false, "Accepted for compatibility; output is always porcelain (short) format")
	dirtyOnly := statusCommand.Bool("dirty", false, "Only show repositories with changes")
	statusCommand.Parse(args)

	// Exactly one positional argument (the config file) is required; extras are a usage error.
	if len(statusCommand.Args()) != 1 {
		printUsage(stderr)
		return 1
	}
	configFile := statusCommand.Args()[0]

	config, err := loadAndValidateConfig(configFile)
	if err != nil {
		writeDisplayError(stderr, "Error reading config file: ", err)
		return 1
	}

	// Refuse an existing destination before doing any work, so a report cannot clobber a file,
	// directory, link, hard link, or the configuration file itself.
	writingToFile := *outputPath != ""
	if writingToFile {
		if err := ensureNewOutputPath(*outputPath); err != nil {
			writeDisplayError(stderr, "Error: ", err)
			return 1
		}
	}

	// Collect every bounded, typed result before rendering or creating an output file. This keeps
	// a temporary report inside a monitored repository from appearing as a status change.
	captureLimit := captureLimitForTargets(len(config.Repos))
	statuses := make([]repoStatusRecord, 0, len(config.Repos))
	hadStatusFailure := false
	for _, repo := range config.Repos {
		// A single porcelain invocation provides both the displayed text and the dirty flag.
		out, diagnostics, statusErr := fetchRepoStatusDetailsLimited(repo.Location, captureLimit)
		dirty := strings.TrimSpace(out) != ""
		if statusErr == nil && *dirtyOnly && !dirty {
			continue
		}
		if statusErr != nil {
			hadStatusFailure = true
		}

		status := repoStatusRecord{
			Name:        repo.Name,
			Status:      out,
			Diagnostics: diagnostics,
		}
		if statusErr != nil {
			status.Error = statusErr.Error()
		}
		statuses = append(statuses, status)
	}

	// Color is only appropriate for an interactive terminal, and never for -o reports.
	useColor := false
	if !writingToFile {
		if terminalFile, ok := stdout.(*os.File); ok && isTerminal(terminalFile) {
			useColor = true
		}
	}
	render := func(writer io.Writer) error {
		if *asJSON {
			return renderStatusJSON(writer, statuses)
		}
		return renderStatusText(writer, statuses, useColor)
	}

	if writingToFile {
		// Render only after all Git checks complete; publishRenderedFile removes a private,
		// failed, or over-budget temporary report instead of exposing it.
		if err := publishRenderedFile(*outputPath, render); err != nil {
			writeDisplayError(stderr, "Error writing status output: ", err)
			return 1
		}
	} else {
		// JSON gets a preflight budget check so a rejected report never leaves malformed stdout.
		if *asJSON {
			if err := render(newRenderLimitWriter(io.Discard)); err != nil {
				writeDisplayError(stderr, "Error writing status output: ", err)
				return 1
			}
		}
		if err := render(newRenderLimitWriter(stdout)); err != nil {
			writeDisplayError(stderr, "Error writing status output: ", err)
			return 1
		}
	}
	if hadStatusFailure {
		return 1
	}
	return 0
}

// repoStatusRecord is the bounded status result retained for one repository before rendering.
type repoStatusRecord struct {
	Name        string `json:"name"`
	Status      string `json:"status"`
	Diagnostics string `json:"diagnostics,omitempty"`
	Error       string `json:"error,omitempty"`
}

// renderStatusJSON writes the status array one record at a time, preserving selection order and
// the existing schema. It accepts a destination and collected records and returns encoding or
// write errors.
func renderStatusJSON(writer io.Writer, statuses []repoStatusRecord) error {
	return writeJSONArray(writer, len(statuses), func(index int) ([]byte, error) {
		return marshalIndentedRecord(statuses[index])
	})
}

// renderStatusText writes the human-readable status report. It accepts a destination, collected
// records, and whether ANSI colors may be used, and returns the first write error. Captured
// porcelain lines are retained even when the status query failed, escaped line by line so the
// normal report separators stay intact.
func renderStatusText(writer io.Writer, statuses []repoStatusRecord, useColor bool) error {
	for _, status := range statuses {
		isClean := status.Error == "" && strings.TrimSpace(status.Status) == ""
		headerColor := ColorYellow
		if isClean {
			headerColor = ColorGreen
		}
		if status.Error != "" {
			headerColor = ColorRed
		}

		displayName := escapeDisplay(status.Name)
		if useColor {
			if _, err := fmt.Fprintf(writer, "%s--- Git status for %s ---%s\n", headerColor, displayName, ColorReset); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintf(writer, "--- Git status for %s ---\n", displayName); err != nil {
				return err
			}
		}

		if status.Error != "" {
			if status.Status != "" {
				if _, err := fmt.Fprintln(writer, escapeDisplayLines(status.Status)); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(writer, "Error: %s\n", escapeDisplay(status.Error)); err != nil {
				return err
			}
		} else {
			if _, err := fmt.Fprintln(writer, escapeDisplayLines(status.Status)); err != nil {
				return err
			}
		}
		// Git can write warnings on stderr even when the status query succeeded. Keep them
		// separate from the porcelain status and render them as a single escaped line.
		if status.Diagnostics != "" {
			if _, err := fmt.Fprintf(writer, "Git diagnostics: %s\n", escapeDisplay(status.Diagnostics)); err != nil {
				return err
			}
		}
	}
	return nil
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

// fetchRepoStatusDetails runs one Git porcelain status query using the single-repository capture
// limit and returns the same values as fetchRepoStatusDetailsLimited.
func fetchRepoStatusDetails(location string) (status string, diagnostics string, err error) {
	return fetchRepoStatusDetailsLimited(location, maxCapturedBytesPerTarget)
}

// fetchRepoStatusDetailsLimited runs one bounded Git porcelain status query for a repository
// directory. It accepts a local repository directory and a byte limit, and returns the porcelain
// stdout text, Git's trimmed stderr diagnostics, and an error when Git cannot inspect the
// directory or its output was truncated. Stdout and stderr are captured separately so callers can
// report warnings without mixing them into status text.
func fetchRepoStatusDetailsLimited(location string, captureLimit int) (status string, diagnostics string, err error) {
	// Use porcelain output because it is empty for a clean working tree, so one invocation
	// yields both the displayed status text and the dirty flag without a second Git process.
	// Optional locks and the fsmonitor hook are disabled only to keep this read-only query from
	// writing index or monitor state; this is not a sandbox.
	statusTimeout := gitStatusTimeout
	waitDelay := gitStatusWaitDelay
	ctx, cancel := context.WithTimeout(context.Background(), statusTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "--no-optional-locks", "-c", "core.fsmonitor=false", "status", "--porcelain")
	cmd.Dir = location
	cmd.WaitDelay = waitDelay
	capture := newBoundedStreams(captureLimit)
	cmd.Stdout = capture.StdoutWriter()
	cmd.Stderr = capture.StderrWriter()
	runErr := cmd.Run()
	status, stderrText, stdoutTruncated, stderrTruncated := capture.Snapshot()
	diagnostics = strings.TrimSpace(stderrText)
	truncation := gitTruncationMessage(captureLimit, stdoutTruncated, stderrTruncated)

	if runErr != nil && (ctx.Err() == context.DeadlineExceeded || errors.Is(runErr, context.DeadlineExceeded)) {
		if truncation != "" {
			return status, diagnostics, fmt.Errorf("checking status timed out after %s: %w; %s", statusTimeout, context.DeadlineExceeded, truncation)
		}
		return status, diagnostics, fmt.Errorf("checking status timed out after %s: %w", statusTimeout, context.DeadlineExceeded)
	}
	if runErr != nil {
		if truncation != "" {
			return status, diagnostics, fmt.Errorf("checking status: %w; %s", runErr, truncation)
		}
		return status, diagnostics, fmt.Errorf("checking status: %w", runErr)
	}
	if truncation != "" {
		return status, diagnostics, errors.New(truncation)
	}
	return status, diagnostics, nil
}

// fetchRepoStatus runs one Git porcelain status query and returns only the status text.
// It accepts a local repository directory and returns the stdout text, or an error that includes
// Git's stderr diagnostics when the query fails. It exists for callers that do not display warnings.
func fetchRepoStatus(location string) (string, error) {
	status, diagnostics, err := fetchRepoStatusDetails(location)
	if err != nil {
		if diagnostics != "" {
			return status, fmt.Errorf("%w: %s", err, diagnostics)
		}
		return status, err
	}
	return status, nil
}

// isRepoDirty checks Git's porcelain status for uncommitted or untracked changes.
// It accepts a local repository directory and returns whether it is dirty, or an error when Git cannot inspect the directory.
func isRepoDirty(location string) (bool, error) {
	out, err := fetchRepoStatus(location)
	if err != nil {
		return false, err
	}

	return strings.TrimSpace(out) != "", nil
}

// findRepo resolves a repository in config by one-based index or exact name.
// It accepts a non-nil Config and selector string, and returns a pointer to the matching slice
// element or an error when no valid match exists. A valid bare numeric selector retains index
// precedence; names beginning with name: have no special meaning in a bare selector.
func findRepo(config *Config, identifier string) (*Repo, error) {
	identifier = strings.TrimSpace(identifier)

	// Parse the selector once; a valid integer prefers an in-range index before name lookup.
	index, indexErr := strconv.Atoi(identifier)
	if indexErr == nil && index >= 1 && index <= len(config.Repos) {
		return &config.Repos[index-1], nil
	}

	// Search by name so numeric names remain usable when they are not valid indexes.
	for i := range config.Repos {
		if config.Repos[i].Name == identifier {
			return &config.Repos[i], nil
		}
	}

	// Preserve a clear index error when an out-of-range numeric selector is not a repository name.
	if indexErr == nil {
		return nil, fmt.Errorf("index out of range: %d", index)
	}

	return nil, fmt.Errorf("repository not found: %s", identifier)
}

// findRepoByName resolves a repository by exact label with no numeric-index interpretation.
// It accepts a non-nil Config and label, and returns the matching slice element or a clear error
// when the label is empty or absent.
func findRepoByName(config *Config, label string) (*Repo, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, fmt.Errorf("repository name is empty")
	}
	for i := range config.Repos {
		if config.Repos[i].Name == label {
			return &config.Repos[i], nil
		}
	}
	return nil, fmt.Errorf("repository not found: %s", label)
}

// printUsage writes command syntax and supported options to the given error stream.
// It accepts the stream to write to and returns no value; callers use it before reporting invalid invocation.
func printUsage(stderr io.Writer) {
	programName := filepath.Base(os.Args[0])
	fmt.Fprintf(stderr, `Usage:
  %[1]s [--dirty] [-o <new_output_file>] [--json] <config_file>
  %[1]s list [--json] <config_file>
  %[1]s path [--name] [--json] <selector> <config_file>
  %[1]s exec [--repos <selectors> | --names <labels>] [--async] [--dry-run] [--json] <config_file> <command> [command_arguments...]

Options must appear before positional arguments.

Commands:
  (default)  Show Git status for every configured repository.
  list       List repositories and their one-based indexes.
  path       Print a repository path by its one-based index or exact name.
  exec       Run a command in each selected repository directory.

Selectors:
  A bare selector is a one-based index or an exact name; a valid number uses the index first.
  A bare name: prefix has no special meaning. Use path --name to interpret the selector
  position as an exact name, including numeric labels and labels beginning with name:.
  Use exec --names for comma-separated exact labels.

Default status options:
  --short              Accepted for compatibility; output is always porcelain (short) format.
  --dirty              Show only repositories with uncommitted or untracked changes.
  -o <output_file>     Write the report to a new file; existing files are never overwritten.
  --json               Emit JSON output.

Exec options:
  --repos <selectors>  Comma-separated indexes or names, for example: 1,api
                       Omit --repos and --names to run on every configured repository.
  --names <labels>     Comma-separated exact labels, including numeric or name:-prefixed ones.
                       Mutually exclusive with --repos; either flag requires a value.
  --async              Run commands in parallel (at most %[2]d at a time).
  --dry-run            Show commands without running them.
  --json               Emit JSON output.

Examples:
  %[1]s repos.yaml
  %[1]s --short --dirty repos.yaml
  %[1]s list --json repos.yaml
  %[1]s path 1 repos.yaml
  %[1]s path api repos.yaml
  %[1]s path --name 42 repos.yaml
  %[1]s exec repos.yaml git status
  %[1]s exec --async repos.yaml git fetch
  %[1]s exec --repos 1,api repos.yaml git pull
  %[1]s exec --names 42,name:legacy repos.yaml git pull
`, programName, maxConcurrentExec)
}
