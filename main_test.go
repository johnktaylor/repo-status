package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// init installs a deterministic fake Git when the test binary is copied onto
// PATH under the name "git". The normal test binary never sets the marker
// environment variable and therefore falls through to the testing package.
func init() {
	if os.Getenv("REPO_STATUS_FAKE_GIT") != "1" {
		return
	}

	workingDir, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fake git: get working directory: %v\n", err)
		os.Exit(1)
	}

	// Record every invocation so a test can prove Git ran once per repository.
	if logPath := os.Getenv("REPO_STATUS_FAKE_GIT_LOG"); logPath != "" {
		if logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(logFile, "%s|%s\n", strings.Join(os.Args[1:], " "), workingDir)
			_ = logFile.Close()
		}
	}

	// The repository directory name selects a canned status response.
	base := filepath.Base(workingDir)
	switch {
	case strings.Contains(base, "fail"):
		fmt.Fprint(os.Stderr, "fatal: fake git failure\n")
		os.Exit(1)
	case strings.Contains(base, "sleep"):
		time.Sleep(5 * time.Second)
		os.Exit(0)
	case strings.Contains(base, "held"):
		// Hold the status query open until the parent releases it, so a test can
		// create the output destination between the preflight check and publish.
		releasePath := os.Getenv("REPO_STATUS_FAKE_GIT_RELEASE")
		deadline := time.Now().Add(10 * time.Second)
		for releasePath != "" {
			if _, err := os.Stat(releasePath); err == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(2)
			}
			time.Sleep(10 * time.Millisecond)
		}
		os.Exit(0)
	case strings.Contains(base, "flood-both"):
		fmt.Fprint(os.Stdout, strings.Repeat("O", 64))
		fmt.Fprint(os.Stderr, strings.Repeat("E", 64))
		os.Exit(0)
	case strings.Contains(base, "flood-stdout"):
		fmt.Fprint(os.Stdout, strings.Repeat("O", 64))
		os.Exit(0)
	case strings.Contains(base, "flood-stderr"):
		fmt.Fprint(os.Stderr, strings.Repeat("E", 64))
		os.Exit(0)
	case strings.Contains(base, "multiline-diagnostic"):
		fmt.Fprint(os.Stdout, " M first.txt\n M second.txt\n")
		fmt.Fprint(os.Stderr, "warning: first line\nwarning: second line\u2028third\u2029fourth\n")
		os.Exit(0)
	case strings.Contains(base, "mixed"):
		fmt.Fprint(os.Stdout, " M mixed.txt\n")
		fmt.Fprint(os.Stderr, "warning: fake mixed diagnostic\n")
		os.Exit(0)
	case strings.Contains(base, "diagnostic"):
		fmt.Fprint(os.Stderr, "warning: fake diagnostic\n")
		os.Exit(0)
	case strings.Contains(base, "dirty"):
		fmt.Fprint(os.Stdout, " M changed.txt\n")
		os.Exit(0)
	default:
		os.Exit(0)
	}
}

// failingWriter is an output stream whose writes always fail, used to verify
// that handlers surface output failures instead of silently reporting success.
type failingWriter struct{}

// Write always reports a synthetic failure so callers must inspect the error.
func (failingWriter) Write(p []byte) (int, error) {
	return 0, errors.New("synthetic write failure")
}

// installFakeGit copies the current test binary into a temporary directory as
// "git", prepends that directory to PATH, and returns the invocation log path.
// It registers environment cleanup through t.Setenv, so tests must not run in
// parallel.
func installFakeGit(t *testing.T) string {
	t.Helper()

	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("read test executable: %v", err)
	}

	installDir := t.TempDir()
	binaryName := "git"
	if runtime.GOOS == "windows" {
		binaryName = "git.exe"
	}
	if err := os.WriteFile(filepath.Join(installDir, binaryName), binary, 0o755); err != nil {
		t.Fatalf("write fake git executable: %v", err)
	}

	logPath := filepath.Join(installDir, "git-calls.log")
	t.Setenv("PATH", installDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("REPO_STATUS_FAKE_GIT", "1")
	t.Setenv("REPO_STATUS_FAKE_GIT_LOG", logPath)
	return logPath
}

// writeRepoConfigFile marshals repositories into a temporary YAML file and
// returns its path. YAML marshalling keeps Windows paths correctly quoted.
func writeRepoConfigFile(t *testing.T, repos []Repo) string {
	t.Helper()

	document := struct {
		Repositories []Repo `yaml:"repositories"`
	}{Repositories: repos}
	contents, err := yaml.Marshal(document)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}

	configFile := filepath.Join(t.TempDir(), "repos.yaml")
	if err := os.WriteFile(configFile, contents, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configFile
}

// makeTestDir creates a named directory beneath base and returns its path so a
// fake Git response can be selected from the directory name.
func makeTestDir(t *testing.T, base string, name string) string {
	t.Helper()

	dir := filepath.Join(base, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create directory %s: %v", dir, err)
	}
	return dir
}

// readGitCalls returns the fake Git invocation log lines, or nil when no Git
// process ran.
func readGitCalls(t *testing.T, logPath string) []string {
	t.Helper()

	contents, err := os.ReadFile(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read fake git log: %v", err)
	}
	trimmed := strings.TrimSpace(string(contents))
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}

// countStartMarkers counts helper-process start markers written during the
// asynchronous exec test.
func countStartMarkers(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".start") {
			count++
		}
	}
	return count
}

// TestHelperExecWritesMarker is a helper process that records that exec ran.
// It skips during a normal test run and writes the marker named by
// REPO_STATUS_MARKER_PATH when invoked by runExec.
func TestHelperExecWritesMarker(t *testing.T) {
	markerPath := os.Getenv("REPO_STATUS_MARKER_PATH")
	if markerPath == "" {
		t.Skip("helper process only")
	}
	if err := os.WriteFile(markerPath, []byte("executed"), 0o600); err != nil {
		t.Fatalf("write exec marker: %v", err)
	}
}

// TestHelperAsyncTarget is a helper process for the asynchronous exec test. It
// records that it started, waits for a release file, and then exits with
// failure when its working-directory name contains "fail".
func TestHelperAsyncTarget(t *testing.T) {
	coordinationDir := os.Getenv("REPO_STATUS_ASYNC_DIR")
	if coordinationDir == "" {
		t.Skip("helper process only")
	}

	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}
	name := filepath.Base(workingDir)
	if err := os.WriteFile(filepath.Join(coordinationDir, name+".start"), []byte("started"), 0o600); err != nil {
		t.Fatalf("write start marker: %v", err)
	}

	// Hold the process open until the parent releases it, so concurrency is
	// observable rather than a race between short-lived processes.
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(coordinationDir, "release")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			os.Exit(3)
		}
		time.Sleep(10 * time.Millisecond)
	}

	if err := os.WriteFile(filepath.Join(coordinationDir, name+".end"), []byte("finished"), 0o600); err != nil {
		t.Fatalf("write finish marker: %v", err)
	}
	if strings.Contains(name, "fail") {
		fmt.Fprintln(os.Stderr, "intentional fake command failure")
		os.Exit(1)
	}
}

// TestRunExecUsesAllReposWhenReposOmitted verifies that leaving out --repos
// still runs every configured repository in configuration order.
func TestRunExecUsesAllReposWhenReposOmitted(t *testing.T) {
	base := t.TempDir()
	firstDir := makeTestDir(t, base, "first")
	secondDir := makeTestDir(t, base, "second")
	configFile := writeRepoConfigFile(t, []Repo{
		{Name: "first", Location: firstDir},
		{Name: "second", Location: secondDir},
	})

	var stdout, stderr bytes.Buffer
	code := runExec([]string{"--dry-run", "--json", configFile, "echo", "hello"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runExec() code = %d, stderr = %q", code, stderr.String())
	}

	var results []struct {
		Name   string `json:"name"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal exec results %q: %v", stdout.String(), err)
	}
	if len(results) != 2 {
		t.Fatalf("runExec() produced %d results, want 2: %q", len(results), stdout.String())
	}
	if results[0].Name != "first" || results[1].Name != "second" {
		t.Fatalf("runExec() names = %q, %q, want first, second", results[0].Name, results[1].Name)
	}
	if !strings.Contains(results[0].Output, firstDir) || !strings.Contains(results[1].Output, secondDir) {
		t.Fatalf("runExec() dry-run output = %q, %q, want both repository paths", results[0].Output, results[1].Output)
	}
}

// TestRunExecRejectsExplicitEmptySelectorBeforeExecution verifies that an
// explicitly supplied empty or whitespace selector is an error, rather than
// silently falling back to all repositories, and that no command starts.
func TestRunExecRejectsExplicitEmptySelectorBeforeExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}

	selectors := []string{"", "   ", ","}
	for _, selector := range selectors {
		t.Run(fmt.Sprintf("selector_%q", selector), func(t *testing.T) {
			base := t.TempDir()
			repoDir := makeTestDir(t, base, "target")
			configFile := writeRepoConfigFile(t, []Repo{{Name: "target", Location: repoDir}})
			markerPath := filepath.Join(t.TempDir(), "executed.marker")
			t.Setenv("REPO_STATUS_MARKER_PATH", markerPath)

			var stdout, stderr bytes.Buffer
			code := runExec([]string{
				"--repos", selector,
				configFile,
				executable, "-test.run=^TestHelperExecWritesMarker$",
			}, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("runExec() succeeded with an explicitly empty selector %q", selector)
			}
			if _, err := os.Stat(markerPath); err == nil {
				t.Fatalf("runExec() executed a command for invalid selector %q", selector)
			} else if !os.IsNotExist(err) {
				t.Fatalf("stat marker for selector %q: %v", selector, err)
			}
			if stderr.Len() == 0 {
				t.Fatalf("runExec() returned %d for selector %q without an error message", code, selector)
			}
		})
	}
}

// TestRunExecRejectsInvalidSelectorBeforeExecution verifies that an unknown
// selector cancels the whole run before any repository command starts.
func TestRunExecRejectsInvalidSelectorBeforeExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}

	selectors := []string{"missing", "1,missing"}
	for _, selector := range selectors {
		t.Run(selector, func(t *testing.T) {
			base := t.TempDir()
			repoDir := makeTestDir(t, base, "target")
			configFile := writeRepoConfigFile(t, []Repo{{Name: "target", Location: repoDir}})
			markerPath := filepath.Join(t.TempDir(), "executed.marker")
			t.Setenv("REPO_STATUS_MARKER_PATH", markerPath)

			var stdout, stderr bytes.Buffer
			code := runExec([]string{
				"--repos", selector,
				configFile,
				executable, "-test.run=^TestHelperExecWritesMarker$",
			}, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("runExec() succeeded with invalid selector %q", selector)
			}
			if _, err := os.Stat(markerPath); err == nil {
				t.Fatalf("runExec() executed a command before rejecting selector %q", selector)
			} else if !os.IsNotExist(err) {
				t.Fatalf("stat marker for selector %q: %v", selector, err)
			}
			if !strings.Contains(stderr.String(), "missing") {
				t.Fatalf("runExec() stderr = %q, want the invalid selector named", stderr.String())
			}
		})
	}
}

// TestRunPathNameOptionForcesExactName verifies that bare selectors keep index
// precedence while --name forces an exact repository label.
func TestRunPathNameOptionForcesExactName(t *testing.T) {
	base := t.TempDir()
	numericDir := makeTestDir(t, base, "numeric-label")
	indexDir := makeTestDir(t, base, "indexed")
	apiDir := makeTestDir(t, base, "api-case")
	configFile := writeRepoConfigFile(t, []Repo{
		{Name: "2", Location: numericDir},
		{Name: "beta", Location: indexDir},
		{Name: "API", Location: apiDir},
	})

	cases := []struct {
		name string
		args []string
		want string
	}{
		{name: "bare index", args: []string{"2", configFile}, want: indexDir},
		{name: "numeric label", args: []string{"--name", "2", configFile}, want: numericDir},
		{name: "uppercase label", args: []string{"--name", "API", configFile}, want: apiDir},
		{name: "text label", args: []string{"--name", "beta", configFile}, want: indexDir},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runPath(testCase.args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("runPath(%v) code = %d, stderr = %q", testCase.args, code, stderr.String())
			}
			if stdout.String() != testCase.want {
				t.Fatalf("runPath(%v) = %q, want %q", testCase.args, stdout.String(), testCase.want)
			}
		})
	}

	// --name is exact and case-sensitive, and never falls back to an index.
	failures := [][]string{
		{"--name", "missing", configFile},
		{"--name", "api", configFile},
		{"--name", "", configFile},
		{"--name", "1", configFile},
	}
	for _, args := range failures {
		var stdout, stderr bytes.Buffer
		if code := runPath(args, &stdout, &stderr); code == 0 {
			t.Fatalf("runPath(%v) succeeded, want an invalid --name error", args)
		}
	}
}

// TestRunExecNamesOptionForcesExactNames verifies that --names selects exact
// labels, while bare --repos selectors keep index precedence.
func TestRunExecNamesOptionForcesExactNames(t *testing.T) {
	base := t.TempDir()
	numericDir := makeTestDir(t, base, "numeric-label")
	indexDir := makeTestDir(t, base, "indexed")
	apiDir := makeTestDir(t, base, "api-case")
	configFile := writeRepoConfigFile(t, []Repo{
		{Name: "2", Location: numericDir},
		{Name: "beta", Location: indexDir},
		{Name: "API", Location: apiDir},
	})

	run := func(flags ...string) (int, []struct {
		Name   string `json:"name"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}, string) {
		args := append(append([]string{}, flags...), "--dry-run", "--json", configFile, "echo", "hello")
		var stdout, stderr bytes.Buffer
		code := runExec(args, &stdout, &stderr)
		var results []struct {
			Name   string `json:"name"`
			Output string `json:"output"`
			Error  string `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &results); err != nil && stdout.Len() != 0 {
			t.Fatalf("unmarshal exec results %q: %v", stdout.String(), err)
		}
		return code, results, stderr.String()
	}

	code, results, stderrText := run("--names", "2")
	if code != 0 {
		t.Fatalf("runExec(--names 2) code = %d, stderr = %q", code, stderrText)
	}
	if len(results) != 1 || results[0].Name != "2" {
		t.Fatalf("runExec(--names 2) results = %+v, want only the repository named 2", results)
	}
	if !strings.Contains(results[0].Output, numericDir) {
		t.Fatalf("runExec(--names 2) output = %q, want %q", results[0].Output, numericDir)
	}

	code, results, stderrText = run("--names", "API,2")
	if code != 0 {
		t.Fatalf("runExec(--names API,2) code = %d, stderr = %q", code, stderrText)
	}
	if len(results) != 2 || results[0].Name != "API" || results[1].Name != "2" {
		t.Fatalf("runExec(--names API,2) results = %+v, want API then 2", results)
	}
	if !strings.Contains(results[0].Output, apiDir) || !strings.Contains(results[1].Output, numericDir) {
		t.Fatalf("runExec(--names API,2) outputs = %q, %q, want selected paths", results[0].Output, results[1].Output)
	}

	code, results, stderrText = run("--repos", "2")
	if code != 0 {
		t.Fatalf("runExec(--repos 2) code = %d, stderr = %q", code, stderrText)
	}
	if len(results) != 1 || results[0].Name != "beta" {
		t.Fatalf("runExec(--repos 2) results = %+v, want only index 2 (beta)", results)
	}
	if !strings.Contains(results[0].Output, indexDir) {
		t.Fatalf("runExec(--repos 2) output = %q, want %q", results[0].Output, indexDir)
	}
}

// TestRunExecRejectsInvalidNamesBeforeExecution verifies that conflicting
// selector options and empty or invalid --names values fail before any command
// starts.
func TestRunExecRejectsInvalidNamesBeforeExecution(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}

	cases := []struct {
		name  string
		flags []string
	}{
		{name: "both selectors", flags: []string{"--repos", "target", "--names", "target"}},
		{name: "empty names", flags: []string{"--names", ""}},
		{name: "whitespace names", flags: []string{"--names", "   "}},
		{name: "empty entry", flags: []string{"--names", "target,,missing"}},
		{name: "missing exact name", flags: []string{"--names", "target,missing"}},
		{name: "numeric exact name", flags: []string{"--names", "1"}},
		{name: "legacy prefix is literal", flags: []string{"--names", "name:target"}},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			base := t.TempDir()
			repoDir := makeTestDir(t, base, "target")
			configFile := writeRepoConfigFile(t, []Repo{{Name: "target", Location: repoDir}})
			markerPath := filepath.Join(t.TempDir(), "executed.marker")
			t.Setenv("REPO_STATUS_MARKER_PATH", markerPath)

			args := append(append([]string{}, testCase.flags...), configFile, executable, "-test.run=^TestHelperExecWritesMarker$")
			var stdout, stderr bytes.Buffer
			code := runExec(args, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("runExec(%v) succeeded with invalid selector options", testCase.flags)
			}
			if _, err := os.Stat(markerPath); err == nil {
				t.Fatalf("runExec(%v) executed a command before rejecting selectors", testCase.flags)
			} else if !os.IsNotExist(err) {
				t.Fatalf("stat marker: %v", err)
			}
			if stderr.Len() == 0 {
				t.Fatalf("runExec(%v) reported no error", testCase.flags)
			}
		})
	}
}

// TestRunPathPreservesInteriorTabOnUnix verifies that a non-terminal path
// output preserves a legal interior tab byte-for-byte and adds no newline.
func TestRunPathPreservesInteriorTabOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("interior tabs are not legal in Windows paths")
	}

	base := t.TempDir()
	location := filepath.Join(base, "repo\tname")
	if err := os.MkdirAll(location, 0o755); err != nil {
		t.Fatalf("create tab path: %v", err)
	}
	configFile := writeRepoConfigFile(t, []Repo{{Name: "tabbed", Location: location}})

	var stdout, stderr bytes.Buffer
	code := runPath([]string{"1", configFile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runPath() code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != location {
		t.Fatalf("runPath() = %q, want byte-for-byte %q", stdout.String(), location)
	}
	if strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("runPath() output has a trailing newline: %q", stdout.String())
	}
}

// TestRunStatusRefusesToOverwriteExistingOutput verifies that -o never
// truncates a destination file that already exists.
func TestRunStatusRefusesToOverwriteExistingOutput(t *testing.T) {
	installFakeGit(t)
	base := t.TempDir()
	repoDir := makeTestDir(t, base, "clean-repo")
	configFile := writeRepoConfigFile(t, []Repo{{Name: "clean", Location: repoDir}})

	outputPath := filepath.Join(t.TempDir(), "report.txt")
	original := []byte("existing report contents\n")
	if err := os.WriteFile(outputPath, original, 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"-o", outputPath, configFile}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("runStatus() succeeded while overwriting an existing output file")
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read existing output after run: %v", err)
	}
	if !bytes.Equal(contents, original) {
		t.Fatalf("existing output changed to %q, want %q", contents, original)
	}
	if stdout.Len() != 0 {
		t.Fatalf("runStatus() wrote %q to stdout after refusing -o", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("runStatus() reported no error after refusing to overwrite")
	}
}

// TestRunStatusRefusesToOverwriteHardLinkAliasOfConfig verifies that an -o
// destination which is a hard link to the configuration file is treated as an
// existing destination and neither name is modified.
func TestRunStatusRefusesToOverwriteHardLinkAliasOfConfig(t *testing.T) {
	installFakeGit(t)
	base := t.TempDir()
	repoDir := makeTestDir(t, base, "clean-repo")
	configFile := writeRepoConfigFile(t, []Repo{{Name: "clean", Location: repoDir}})
	original, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}

	outputPath := filepath.Join(filepath.Dir(configFile), "linked-report.txt")
	if err := os.Link(configFile, outputPath); err != nil {
		t.Skipf("hard links are not supported here: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"-o", outputPath, configFile}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("runStatus() succeeded with an -o hard-link alias of the config file")
	}
	for _, path := range []string{configFile, outputPath} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s after run: %v", path, err)
		}
		if !bytes.Equal(contents, original) {
			t.Fatalf("%s changed to %q, want %q", path, contents, original)
		}
	}
	if stdout.Len() != 0 {
		t.Fatalf("runStatus() wrote %q to stdout after refusing a hard-link alias", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("runStatus() reported no error after refusing a hard-link alias")
	}
}

// TestRunStatusDoesNotOverwriteDestinationCreatedDuringCollection verifies
// that a destination which appears after the preflight check is still never
// clobbered when the collected report is published.
func TestRunStatusDoesNotOverwriteDestinationCreatedDuringCollection(t *testing.T) {
	logPath := installFakeGit(t)
	releasePath := filepath.Join(t.TempDir(), "release")
	t.Setenv("REPO_STATUS_FAKE_GIT_RELEASE", releasePath)

	base := t.TempDir()
	repoDir := makeTestDir(t, base, "held-repo")
	configFile := writeRepoConfigFile(t, []Repo{{Name: "held", Location: repoDir}})
	outputPath := filepath.Join(t.TempDir(), "report.txt")

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runStatus([]string{"-o", outputPath, configFile}, &stdout, &stderr)
	}()

	// Wait until the held status query has started, which is after the
	// destination preflight check but before the report is published.
	deadline := time.Now().Add(10 * time.Second)
	for len(readGitCalls(t, logPath)) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("fake Git status did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}

	sentinel := []byte("created during status collection\n")
	if err := os.WriteFile(outputPath, sentinel, 0o600); err != nil {
		t.Fatalf("create destination during collection: %v", err)
	}
	if err := os.WriteFile(releasePath, []byte("release"), 0o600); err != nil {
		t.Fatalf("release fake Git status: %v", err)
	}

	select {
	case code := <-done:
		if code == 0 {
			t.Fatal("runStatus() succeeded after the destination appeared during collection")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runStatus() did not finish after releasing the status query")
	}

	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read destination after run: %v", err)
	}
	if !bytes.Equal(contents, sentinel) {
		t.Fatalf("destination changed to %q, want %q", contents, sentinel)
	}
	if stderr.Len() == 0 {
		t.Fatal("runStatus() reported no error after refusing a destination created during collection")
	}
}

// TestRunStatusRefusesToOverwriteConfigThroughAlias verifies that an -o path
// that resolves to the configuration file is rejected even when spelled with
// dot segments or different letter case on Windows.
func TestRunStatusRefusesToOverwriteConfigThroughAlias(t *testing.T) {
	installFakeGit(t)
	base := t.TempDir()
	repoDir := makeTestDir(t, base, "clean-repo")
	configFile := writeRepoConfigFile(t, []Repo{{Name: "clean", Location: repoDir}})
	original, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	configDir := filepath.Dir(configFile)
	nestedDir := filepath.Join(configDir, "nested")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}

	aliases := map[string]string{
		"config path":  configFile,
		"dot segments": filepath.Join(nestedDir, "..", filepath.Base(configFile)),
	}
	if runtime.GOOS == "windows" {
		aliases["case variant"] = filepath.Join(configDir, strings.ToUpper(filepath.Base(configFile)))
	}

	for name, alias := range aliases {
		t.Run(name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runStatus([]string{"-o", alias, configFile}, &stdout, &stderr)
			if code == 0 {
				t.Fatalf("runStatus() succeeded with -o alias %q", alias)
			}
			contents, err := os.ReadFile(configFile)
			if err != nil {
				t.Fatalf("read config after run: %v", err)
			}
			if !bytes.Equal(contents, original) {
				t.Fatal("runStatus() modified the configuration file through an -o alias")
			}
			if stderr.Len() == 0 {
				t.Fatalf("runStatus() returned %d for alias %q without an error", code, alias)
			}
		})
	}
}

// TestRunStatusLeavesExistingOutputUntouchedForInvalidConfig verifies that an
// invalid configuration fails before an existing -o destination is touched.
func TestRunStatusLeavesExistingOutputUntouchedForInvalidConfig(t *testing.T) {
	invalidConfig := filepath.Join(t.TempDir(), "invalid.yaml")
	if err := os.WriteFile(invalidConfig, []byte("repositories: []\n"), 0o600); err != nil {
		t.Fatalf("write invalid config: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "report.txt")
	original := []byte("keep this report\n")
	if err := os.WriteFile(outputPath, original, 0o600); err != nil {
		t.Fatalf("write existing output: %v", err)
	}

	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"-o", outputPath, invalidConfig}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("runStatus() succeeded with an invalid configuration")
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output after invalid config: %v", err)
	}
	if !bytes.Equal(contents, original) {
		t.Fatalf("existing output changed to %q, want %q", contents, original)
	}
	if stdout.Len() != 0 {
		t.Fatalf("runStatus() wrote %q to stdout for an invalid configuration", stdout.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("runStatus() reported no error for an invalid configuration")
	}
}

// TestRunStatusCollectsStatusBeforeCreatingOutput verifies that a new -o file
// inside a repository is written only after status is collected, so the report
// file itself cannot appear as an untracked change.
func TestRunStatusCollectsStatusBeforeCreatingOutput(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	// Ignore machine-specific Git configuration so the status result is predictable.
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	repoDir := t.TempDir()
	initCommand := exec.Command("git", "init", repoDir)
	if output, err := initCommand.CombinedOutput(); err != nil {
		t.Fatalf("initialize repository: %v\n%s", err, output)
	}
	configFile := writeRepoConfigFile(t, []Repo{{Name: "repo", Location: repoDir}})
	outputPath := filepath.Join(repoDir, "report.txt")

	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"-o", outputPath, configFile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runStatus() code = %d, stderr = %q", code, stderr.String())
	}
	contents, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read generated report: %v", err)
	}
	if strings.Contains(string(contents), "report.txt") {
		t.Fatalf("report included its own output file as status: %q", contents)
	}
	if strings.Contains(string(contents), "??") {
		t.Fatalf("report showed an untracked change before the output file existed: %q", contents)
	}
	if stdout.Len() != 0 {
		t.Fatalf("runStatus() wrote %q to stdout while using -o", stdout.String())
	}
}

// TestRunStatusSeparatesGitStatusStreams verifies that status text comes only
// from Git's stdout, so stderr diagnostics do not make a clean repository dirty
// or contaminate the displayed porcelain output.
func TestRunStatusSeparatesGitStatusStreams(t *testing.T) {
	logPath := installFakeGit(t)
	base := t.TempDir()
	mixedDir := makeTestDir(t, base, "mixed-repo")
	diagnosticDir := makeTestDir(t, base, "diagnostic-repo")
	configFile := writeRepoConfigFile(t, []Repo{
		{Name: "mixed", Location: mixedDir},
		{Name: "diagnostic", Location: diagnosticDir},
	})

	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"--dirty", "--json", configFile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runStatus() code = %d, stderr = %q", code, stderr.String())
	}

	var statuses []struct {
		Name        string `json:"name"`
		Status      string `json:"status"`
		Diagnostics string `json:"diagnostics"`
		Error       string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil {
		t.Fatalf("unmarshal statuses %q: %v", stdout.String(), err)
	}
	if len(statuses) != 1 || statuses[0].Name != "mixed" {
		t.Fatalf("runStatus() statuses = %+v, want only the dirty repository", statuses)
	}
	if strings.TrimSpace(statuses[0].Status) != "M mixed.txt" {
		t.Fatalf("status = %q, want only Git stdout", statuses[0].Status)
	}
	if strings.Contains(statuses[0].Status, "warning") || strings.Contains(statuses[0].Status, "diagnostic") {
		t.Fatalf("status text mixed in Git stderr: %q", statuses[0].Status)
	}
	if !strings.Contains(statuses[0].Diagnostics, "fake mixed diagnostic") {
		t.Fatalf("diagnostics = %q, want Git stderr kept separate", statuses[0].Diagnostics)
	}
	if statuses[0].Error != "" {
		t.Fatalf("status error = %q, want none for successful diagnostics", statuses[0].Error)
	}
	if stderr.Len() != 0 {
		t.Fatalf("runStatus() wrote unexpected stderr: %q", stderr.String())
	}

	calls := readGitCalls(t, logPath)
	if len(calls) != 2 {
		t.Fatalf("Git status ran %d times, want once per repository: %v", len(calls), calls)
	}
	for _, call := range calls {
		if !strings.Contains(call, "status --porcelain") {
			t.Fatalf("unexpected fake Git invocation: %q", call)
		}
	}
}

// TestRunStatusDisplayFieldsStaySingleLine verifies that multiline Git
// diagnostics are escaped into one prefixed text line and Unicode separators
// are escaped, while status records and JSON diagnostics keep real line breaks.
func TestRunStatusDisplayFieldsStaySingleLine(t *testing.T) {
	installFakeGit(t)
	base := t.TempDir()
	repoDir := makeTestDir(t, base, "multiline-diagnostic-repo")
	configFile := writeRepoConfigFile(t, []Repo{{Name: "multiline", Location: repoDir}})

	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runStatus([]string{configFile}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("runStatus() code = %d, stderr = %q", code, stderr.String())
		}

		text := stdout.String()
		if !strings.Contains(text, " M first.txt\n M second.txt") {
			t.Fatalf("status records lost their LF separator: %q", text)
		}
		if strings.Contains(text, `M first.txt\x0a`) {
			t.Fatalf("status record LF was escaped: %q", text)
		}

		diagnosticLine := ""
		for _, line := range strings.Split(text, "\n") {
			if strings.HasPrefix(line, "Git diagnostics: ") {
				diagnosticLine = line
			}
		}
		want := `Git diagnostics: warning: first line\x0awarning: second line\u2028third\u2029fourth`
		if diagnosticLine != want {
			t.Fatalf("diagnostics line = %q, want one escaped prefixed line %q", diagnosticLine, want)
		}
	})

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runStatus([]string{"--json", configFile}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("runStatus() code = %d, stderr = %q", code, stderr.String())
		}

		var statuses []struct {
			Status      string `json:"status"`
			Diagnostics string `json:"diagnostics"`
			Error       string `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil {
			t.Fatalf("unmarshal statuses %q: %v", stdout.String(), err)
		}
		if len(statuses) != 1 {
			t.Fatalf("runStatus() produced %d statuses, want 1", len(statuses))
		}
		if statuses[0].Status != " M first.txt\n M second.txt\n" {
			t.Fatalf("JSON status = %q, want real LF separators", statuses[0].Status)
		}
		wantDiagnostics := "warning: first line\nwarning: second line\u2028third\u2029fourth"
		if statuses[0].Diagnostics != wantDiagnostics {
			t.Fatalf("JSON diagnostics = %q, want %q", statuses[0].Diagnostics, wantDiagnostics)
		}
		if statuses[0].Error != "" {
			t.Fatalf("JSON error = %q, want none", statuses[0].Error)
		}
	})
}

// TestRunStatusChecksGitOncePerRepo verifies that a successful status run uses
// exactly one Git process per configured repository.
func TestRunStatusChecksGitOncePerRepo(t *testing.T) {
	logPath := installFakeGit(t)
	base := t.TempDir()
	firstDir := makeTestDir(t, base, "clean-first")
	secondDir := makeTestDir(t, base, "clean-second")
	thirdDir := makeTestDir(t, base, "clean-third")
	configFile := writeRepoConfigFile(t, []Repo{
		{Name: "first", Location: firstDir},
		{Name: "second", Location: secondDir},
		{Name: "third", Location: thirdDir},
	})

	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"--json", configFile}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runStatus() code = %d, stderr = %q", code, stderr.String())
	}

	calls := readGitCalls(t, logPath)
	if len(calls) != 3 {
		t.Fatalf("Git status ran %d times, want 3: %v", len(calls), calls)
	}
	for _, call := range calls {
		if !strings.Contains(call, "status --porcelain") {
			t.Fatalf("unexpected fake Git invocation: %q", call)
		}
	}
}

// TestRunStatusDirtyRetainsStatusFailures verifies that --dirty keeps failed
// repositories in both text and JSON output while omitting clean ones.
func TestRunStatusDirtyRetainsStatusFailures(t *testing.T) {
	installFakeGit(t)
	base := t.TempDir()
	dirtyDir := makeTestDir(t, base, "dirty-repo")
	cleanDir := makeTestDir(t, base, "clean-repo")
	failDir := makeTestDir(t, base, "fail-repo")
	configFile := writeRepoConfigFile(t, []Repo{
		{Name: "dirty", Location: dirtyDir},
		{Name: "clean", Location: cleanDir},
		{Name: "fail", Location: failDir},
	})

	t.Run("json", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runStatus([]string{"--dirty", "--json", configFile}, &stdout, &stderr)
		if code == 0 {
			t.Fatal("runStatus() succeeded despite a failed status check")
		}

		var statuses []struct {
			Name        string `json:"name"`
			Status      string `json:"status"`
			Diagnostics string `json:"diagnostics"`
			Error       string `json:"error"`
		}
		if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil {
			t.Fatalf("unmarshal statuses %q: %v", stdout.String(), err)
		}
		if len(statuses) != 2 {
			t.Fatalf("runStatus() statuses = %+v, want dirty and failed repositories", statuses)
		}
		if statuses[0].Name != "dirty" || statuses[1].Name != "fail" {
			t.Fatalf("status names = %q, %q, want dirty, fail", statuses[0].Name, statuses[1].Name)
		}
		if statuses[1].Error == "" {
			t.Fatal("failed status has no error field")
		}
		if !strings.Contains(statuses[1].Diagnostics, "fake git failure") {
			t.Fatalf("failed status diagnostics = %q, want Git stderr kept separate", statuses[1].Diagnostics)
		}
		if statuses[0].Error != "" {
			t.Fatalf("dirty status error = %q, want none", statuses[0].Error)
		}
	})

	t.Run("text", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		code := runStatus([]string{"--dirty", configFile}, &stdout, &stderr)
		if code == 0 {
			t.Fatal("runStatus() succeeded despite a failed status check")
		}
		text := stdout.String()
		if !strings.Contains(text, "--- Git status for dirty ---") {
			t.Fatalf("text output missing dirty repository heading: %q", text)
		}
		if !strings.Contains(text, "--- Git status for fail ---") {
			t.Fatalf("text output missing failed repository heading: %q", text)
		}
		if strings.Contains(text, "--- Git status for clean ---") {
			t.Fatalf("text output included a clean repository under --dirty: %q", text)
		}
		if count := strings.Count(text, "fake git failure"); count != 1 {
			t.Fatalf("Git failure appeared %d times in text output, want once: %q", count, text)
		}
	})
}

// TestRunListReportsWriterFailure verifies that list returns a failure when
// its output stream cannot be written in either text or JSON mode.
func TestRunListReportsWriterFailure(t *testing.T) {
	configFile := writeRepoConfigFile(t, []Repo{{Name: "first", Location: filepath.Join(t.TempDir(), "repo")}})

	cases := map[string][]string{
		"text": {configFile},
		"json": {"--json", configFile},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runList(args, failingWriter{}, &stderr)
			if code == 0 {
				t.Fatal("runList() succeeded despite a failing output stream")
			}
			if stderr.Len() == 0 {
				t.Fatal("runList() reported no error for a failing output stream")
			}
		})
	}
}

// TestRunPathReportsWriterFailure verifies that path returns a failure when
// its output stream cannot be written in either text or JSON mode.
func TestRunPathReportsWriterFailure(t *testing.T) {
	configFile := writeRepoConfigFile(t, []Repo{{Name: "first", Location: filepath.Join(t.TempDir(), "repo")}})

	cases := map[string][]string{
		"text": {"1", configFile},
		"json": {"--json", "1", configFile},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runPath(args, failingWriter{}, &stderr)
			if code == 0 {
				t.Fatal("runPath() succeeded despite a failing output stream")
			}
			if stderr.Len() == 0 {
				t.Fatal("runPath() reported no error for a failing output stream")
			}
		})
	}
}

// TestRunExecReportsWriterFailure verifies that exec returns a failure when
// its dry-run report cannot be written in either text or JSON mode.
func TestRunExecReportsWriterFailure(t *testing.T) {
	configFile := writeRepoConfigFile(t, []Repo{{Name: "first", Location: filepath.Join(t.TempDir(), "repo")}})

	cases := map[string][]string{
		"text": {"--dry-run", configFile, "echo", "hello"},
		"json": {"--dry-run", "--json", configFile, "echo", "hello"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := runExec(args, failingWriter{}, &stderr)
			if code == 0 {
				t.Fatal("runExec() succeeded despite a failing output stream")
			}
			if stderr.Len() == 0 {
				t.Fatal("runExec() reported no error for a failing output stream")
			}
		})
	}
}

// TestRunExecAsyncCapsConcurrencyAndKeepsResults runs more than eight targets
// with --async, proves no more than eight commands start at once, and checks
// that JSON results stay in selection order even when one child fails.
func TestRunExecAsyncCapsConcurrencyAndKeepsResults(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}

	base := t.TempDir()
	coordinationDir := t.TempDir()
	repos := make([]Repo, 0, 10)
	for i := 0; i < 10; i++ {
		name := fmt.Sprintf("target-%02d", i)
		if i == 9 {
			name += "-fail"
		}
		repos = append(repos, Repo{Name: name, Location: makeTestDir(t, base, name)})
	}
	configFile := writeRepoConfigFile(t, repos)
	t.Setenv("REPO_STATUS_ASYNC_DIR", coordinationDir)

	var stdout, stderr bytes.Buffer
	done := make(chan int, 1)
	go func() {
		done <- runExec([]string{
			"--async", "--json",
			configFile,
			executable, "-test.run=^TestHelperAsyncTarget$",
		}, &stdout, &stderr)
	}()

	released := false
	release := func() {
		if released {
			return
		}
		released = true
		if err := os.WriteFile(filepath.Join(coordinationDir, "release"), []byte("release"), 0o600); err != nil {
			t.Errorf("release helper processes: %v", err)
		}
	}
	defer release()

	// Wait for the bound to be reached, then give any over-limit process time
	// to start before writing the release file.
	deadline := time.Now().Add(10 * time.Second)
	for countStartMarkers(coordinationDir) < 8 {
		if time.Now().After(deadline) {
			t.Fatalf("helper processes did not start; markers = %d", countStartMarkers(coordinationDir))
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(250 * time.Millisecond)
	if started := countStartMarkers(coordinationDir); started > maxConcurrentExec {
		t.Fatalf("%d commands started at once, want at most %d", started, maxConcurrentExec)
	}

	release()
	select {
	case code := <-done:
		if code == 0 {
			t.Fatal("runExec() succeeded even though one child command failed")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("runExec() did not finish after releasing helper processes")
	}

	var results []struct {
		Name   string `json:"name"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal exec results %q: %v", stdout.String(), err)
	}
	if len(results) != len(repos) {
		t.Fatalf("runExec() produced %d results, want %d; a failure suppressed results: %q", len(results), len(repos), stdout.String())
	}
	for i, repo := range repos {
		if results[i].Name != repo.Name {
			t.Fatalf("result %d name = %q, want %q (ordering must follow selection order)", i, results[i].Name, repo.Name)
		}
		if i == len(repos)-1 {
			if results[i].Error == "" {
				t.Fatalf("failed child %q has no error field", repo.Name)
			}
		} else if results[i].Error != "" {
			t.Fatalf("result %d (%q) error = %q, want none", i, repo.Name, results[i].Error)
		}
	}
	if started := countStartMarkers(coordinationDir); started != len(repos) {
		t.Fatalf("%d helper processes ran, want %d", started, len(repos))
	}
}
