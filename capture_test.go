package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// floodPattern returns the repeating test pattern used by the oversized-output
// helper, truncated or extended to exactly size bytes.
func floodPattern(size int) string {
	const pattern = "0123456789abcdef"
	if size <= 0 {
		return ""
	}
	return strings.Repeat(pattern, size/len(pattern)) + pattern[:size%len(pattern)]
}

// TestHelperFloodStdout is a helper process that writes the requested number of
// pattern bytes to standard output, used to exercise exec capture limits.
func TestHelperFloodStdout(t *testing.T) {
	sizeText := os.Getenv("REPO_STATUS_FLOOD_BYTES")
	if sizeText == "" {
		t.Skip("helper process only")
	}
	size, err := strconv.Atoi(sizeText)
	if err != nil || size < 0 {
		t.Fatalf("invalid REPO_STATUS_FLOOD_BYTES %q", sizeText)
	}

	pattern := floodPattern(size)
	if _, err := os.Stdout.WriteString(pattern); err != nil {
		t.Fatalf("write flood output: %v", err)
	}
}

// TestCaptureLimitForTargets verifies the per-target cap and the per-run budget
// division, including the configured repository maximum and the one-byte floor.
func TestCaptureLimitForTargets(t *testing.T) {
	perTargetCap := maxCapturedBytesPerTarget
	perRunBudget := maxCapturedBytesPerRun
	boundaryTargets := perRunBudget / perTargetCap

	cases := []struct {
		name    string
		targets int
		want    int
	}{
		{name: "no targets", targets: 0, want: perTargetCap},
		{name: "one target", targets: 1, want: perTargetCap},
		{name: "two targets", targets: 2, want: perTargetCap},
		{name: "budget boundary", targets: boundaryTargets, want: perTargetCap},
		{name: "just over budget boundary", targets: boundaryTargets + 1, want: perRunBudget / (boundaryTargets + 1)},
		{name: "configured maximum", targets: maxConfiguredRepos, want: perRunBudget / maxConfiguredRepos},
		{name: "floor of one byte", targets: perRunBudget + 1, want: 1},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := captureLimitForTargets(testCase.targets); got != testCase.want {
				t.Fatalf("captureLimitForTargets(%d) = %d, want %d", testCase.targets, got, testCase.want)
			}
		})
	}

	// At the configured repository maximum the sum of per-target limits must fit
	// inside the per-run budget.
	limit := captureLimitForTargets(maxConfiguredRepos)
	if total := int64(limit) * int64(maxConfiguredRepos); total > int64(perRunBudget) {
		t.Fatalf("configured maximum retains %d bytes across targets, want at most %d", total, perRunBudget)
	}
}

// TestBoundedStreamsShareOneLimit verifies that stdout and stderr draw from the
// same byte budget and that whichever stream writes last is marked truncated.
func TestBoundedStreamsShareOneLimit(t *testing.T) {
	const limit = 10

	t.Run("stdout first", func(t *testing.T) {
		streams := newBoundedStreams(limit)
		if _, err := streams.StdoutWriter().Write([]byte("abcdef")); err != nil {
			t.Fatalf("write stdout: %v", err)
		}
		if _, err := streams.StderrWriter().Write([]byte("ghijkl")); err != nil {
			t.Fatalf("write stderr: %v", err)
		}

		stdout, stderr, stdoutTruncated, stderrTruncated := streams.Snapshot()
		if stdout != "abcdef" || stderr != "ghij" {
			t.Fatalf("snapshot = %q, %q, want abcdef, ghij", stdout, stderr)
		}
		if stdoutTruncated || !stderrTruncated {
			t.Fatalf("truncation = stdout:%t stderr:%t, want stdout:false stderr:true", stdoutTruncated, stderrTruncated)
		}
		if retained := len(stdout) + len(stderr); retained != limit {
			t.Fatalf("retained %d bytes across streams, want %d", retained, limit)
		}
	})

	t.Run("stderr first", func(t *testing.T) {
		streams := newBoundedStreams(limit)
		if _, err := streams.StderrWriter().Write([]byte("ghijkl")); err != nil {
			t.Fatalf("write stderr: %v", err)
		}
		if _, err := streams.StdoutWriter().Write([]byte("abcdef")); err != nil {
			t.Fatalf("write stdout: %v", err)
		}

		stdout, stderr, stdoutTruncated, stderrTruncated := streams.Snapshot()
		if stdout != "abcd" || stderr != "ghijkl" {
			t.Fatalf("snapshot = %q, %q, want abcd, ghijkl", stdout, stderr)
		}
		if !stdoutTruncated || stderrTruncated {
			t.Fatalf("truncation = stdout:%t stderr:%t, want stdout:true stderr:false", stdoutTruncated, stderrTruncated)
		}
		if retained := len(stdout) + len(stderr); retained != limit {
			t.Fatalf("retained %d bytes across streams, want %d", retained, limit)
		}
	})
}

// TestRunExecTruncatesOversizedOutput verifies that exec retains only the
// per-target prefix, reports the truncation as an error, and exits non-zero
// even though the child succeeded.
func TestRunExecTruncatesOversizedOutput(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}

	limit := maxCapturedBytesPerTarget
	t.Setenv("REPO_STATUS_FLOOD_BYTES", strconv.Itoa(limit+4096))

	base := t.TempDir()
	repoDir := makeTestDir(t, base, "flood-target")
	configFile := writeRepoConfigFile(t, []Repo{{Name: "flood", Location: repoDir}})

	var stdout, stderr bytes.Buffer
	code := runExec([]string{
		"--json",
		configFile,
		executable, "-test.run=^TestHelperFloodStdout$",
	}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("runExec() succeeded despite truncated command output")
	}

	var results []struct {
		Name   string `json:"name"`
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
		t.Fatalf("unmarshal exec results %q: %v", stdout.String(), err)
	}
	if len(results) != 1 {
		t.Fatalf("runExec() produced %d results, want 1", len(results))
	}
	if len(results[0].Output) != limit {
		t.Fatalf("retained output is %d bytes, want the %d-byte per-target cap", len(results[0].Output), limit)
	}
	if results[0].Output != floodPattern(limit) {
		t.Fatal("retained output is not the prefix of the command output")
	}
	if !strings.Contains(results[0].Error, "truncated") || !strings.Contains(results[0].Error, strconv.Itoa(limit)) {
		t.Fatalf("result error = %q, want truncation note with the %d-byte limit", results[0].Error, limit)
	}
}

// TestFetchRepoStatusTruncationKeepsPrefix verifies that Git stdout and stderr
// share one capture limit, retained text is a prefix, truncation is an error,
// and stderr diagnostics remain separate from status text.
func TestFetchRepoStatusTruncationKeepsPrefix(t *testing.T) {
	installFakeGit(t)
	const limit = 8

	t.Run("stdout only", func(t *testing.T) {
		repoDir := makeTestDir(t, t.TempDir(), "flood-stdout-repo")
		status, diagnostics, err := fetchRepoStatusDetailsLimited(repoDir, limit)
		if err == nil {
			t.Fatal("fetchRepoStatusDetailsLimited() succeeded despite truncated stdout")
		}
		if status != strings.Repeat("O", limit) {
			t.Fatalf("status = %q, want the retained stdout prefix", status)
		}
		if diagnostics != "" {
			t.Fatalf("diagnostics = %q, want none", diagnostics)
		}
		if !strings.Contains(err.Error(), "truncated") || !strings.Contains(err.Error(), "stdout") {
			t.Fatalf("error = %q, want stdout truncation note", err)
		}
	})

	t.Run("stderr only", func(t *testing.T) {
		repoDir := makeTestDir(t, t.TempDir(), "flood-stderr-repo")
		status, diagnostics, err := fetchRepoStatusDetailsLimited(repoDir, limit)
		if err == nil {
			t.Fatal("fetchRepoStatusDetailsLimited() succeeded despite truncated stderr")
		}
		if status != "" {
			t.Fatalf("status = %q, want no stdout text", status)
		}
		if diagnostics != strings.Repeat("E", limit) {
			t.Fatalf("diagnostics = %q, want the retained stderr prefix", diagnostics)
		}
		if !strings.Contains(err.Error(), "truncated") || !strings.Contains(err.Error(), "stderr") {
			t.Fatalf("error = %q, want stderr truncation note", err)
		}
	})

	t.Run("shared budget", func(t *testing.T) {
		repoDir := makeTestDir(t, t.TempDir(), "flood-both-repo")
		status, diagnostics, err := fetchRepoStatusDetailsLimited(repoDir, limit)
		if err == nil {
			t.Fatal("fetchRepoStatusDetailsLimited() succeeded despite truncated output")
		}
		if retained := len(status) + len(diagnostics); retained != limit {
			t.Fatalf("retained %d bytes across Git streams, want %d", retained, limit)
		}
		if status != strings.Repeat("O", len(status)) {
			t.Fatalf("status = %q, want only stdout bytes", status)
		}
		if diagnostics != strings.Repeat("E", len(diagnostics)) {
			t.Fatalf("diagnostics = %q, want only stderr bytes", diagnostics)
		}
		if !strings.Contains(err.Error(), "stdout and stderr") {
			t.Fatalf("error = %q, want both-stream truncation note", err)
		}
	})
}

// TestGitStatusDeadlineFailsWithoutWaiting verifies that a short overridden Git
// status timeout ends the query promptly with a timeout error instead of waiting
// for the default deadline.
func TestGitStatusDeadlineFailsWithoutWaiting(t *testing.T) {
	installFakeGit(t)
	base := t.TempDir()
	repoDir := makeTestDir(t, base, "sleep-repo")
	configFile := writeRepoConfigFile(t, []Repo{{Name: "sleep", Location: repoDir}})

	previousTimeout, previousWaitDelay := gitStatusTimeout, gitStatusWaitDelay
	gitStatusTimeout = 200 * time.Millisecond
	gitStatusWaitDelay = 200 * time.Millisecond
	t.Cleanup(func() {
		gitStatusTimeout = previousTimeout
		gitStatusWaitDelay = previousWaitDelay
	})

	start := time.Now()
	var stdout, stderr bytes.Buffer
	code := runStatus([]string{"--json", configFile}, &stdout, &stderr)
	elapsed := time.Since(start)
	if code == 0 {
		t.Fatal("runStatus() succeeded despite a Git status timeout")
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Git status timeout took %s, want a prompt failure", elapsed)
	}

	var statuses []struct {
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &statuses); err != nil {
		t.Fatalf("unmarshal statuses %q: %v", stdout.String(), err)
	}
	if len(statuses) != 1 {
		t.Fatalf("runStatus() produced %d statuses, want 1", len(statuses))
	}
	if !strings.Contains(statuses[0].Error, "timed out") {
		t.Fatalf("status error = %q, want a timeout error", statuses[0].Error)
	}
}

// TestSelectorLiteralPrefixedNamesStayCompatible verifies that bare selectors
// keep treating a repository literally named name:api as that name, even when
// an api label also exists, while --name and --names always mean labels.
func TestSelectorLiteralPrefixedNamesStayCompatible(t *testing.T) {
	base := t.TempDir()
	literalDir := makeTestDir(t, base, "literal-prefixed")
	plainDir := makeTestDir(t, base, "plain-api")

	bothConfig := writeRepoConfigFile(t, []Repo{
		{Name: "name:api", Location: literalDir},
		{Name: "api", Location: plainDir},
	})

	var stdout, stderr bytes.Buffer
	if code := runPath([]string{"name:api", bothConfig}, &stdout, &stderr); code != 0 {
		t.Fatalf("runPath(name:api) code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != literalDir {
		t.Fatalf("runPath(name:api) = %q, want the literal-name path %q", stdout.String(), literalDir)
	}

	stdout.Reset()
	stderr.Reset()
	if code := runPath([]string{"--name", "api", bothConfig}, &stdout, &stderr); code != 0 {
		t.Fatalf("runPath(--name api) code = %d, stderr = %q", code, stderr.String())
	}
	if stdout.String() != plainDir {
		t.Fatalf("runPath(--name api) = %q, want the explicit label path %q", stdout.String(), plainDir)
	}

	code, names := runDryExecNames(t, bothConfig, "--repos", "name:api")
	if code != 0 || len(names) != 1 || names[0] != "name:api" {
		t.Fatalf("exec --repos name:api = code %d, names %v, want the literal name", code, names)
	}
	code, names = runDryExecNames(t, bothConfig, "--names", "api")
	if code != 0 || len(names) != 1 || names[0] != "api" {
		t.Fatalf("exec --names api = code %d, names %v, want the plain label", code, names)
	}

	literalOnlyConfig := writeRepoConfigFile(t, []Repo{
		{Name: "name:api", Location: literalDir},
	})
	stdout.Reset()
	stderr.Reset()
	if code := runPath([]string{"name:api", literalOnlyConfig}, &stdout, &stderr); code != 0 {
		t.Fatalf("runPath(name:api) code = %d for literal prefixed name, stderr = %q", code, stderr.String())
	}
	if stdout.String() != literalDir {
		t.Fatalf("runPath(name:api) = %q, want the literal-name path %q", stdout.String(), literalDir)
	}

	code, names = runDryExecNames(t, literalOnlyConfig, "--repos", "name:api")
	if code != 0 || len(names) != 1 || names[0] != "name:api" {
		t.Fatalf("exec --repos name:api = code %d, names %v, want the literal name", code, names)
	}
	if code, _ := runDryExecNames(t, literalOnlyConfig, "--names", "api"); code == 0 {
		t.Fatal("exec --names api succeeded without an api label")
	}
}

// runDryExecNames runs exec in dry-run JSON mode and returns the selected
// repository names in result order.
func runDryExecNames(t *testing.T, configFile string, flags ...string) (int, []string) {
	t.Helper()

	args := append(append([]string{}, flags...), "--dry-run", "--json", configFile, "echo", "hello")
	var stdout, stderr bytes.Buffer
	code := runExec(args, &stdout, &stderr)

	var results []struct {
		Name string `json:"name"`
	}
	if stdout.Len() > 0 {
		if err := json.Unmarshal(stdout.Bytes(), &results); err != nil {
			t.Fatalf("unmarshal exec results %q: %v", stdout.String(), err)
		}
	}
	names := make([]string, 0, len(results))
	for _, result := range results {
		names = append(names, result.Name)
	}
	return code, names
}
