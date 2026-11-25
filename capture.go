package main

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"sync"
)

// Capture limits bound how much child-process output one run retains in memory. Each target gets
// a deterministic share of the per-run budget, capped at 1 MiB, so a noisy command cannot exhaust
// memory or unfairly consume another target's allowance.
const (
	maxCapturedBytesPerTarget = 1 << 20  // 1 MiB
	maxCapturedBytesPerRun    = 64 << 20 // 64 MiB
)

// captureLimitForTargets returns the number of output bytes retained for each target in a run.
// It accepts the number of targets and returns min(1 MiB, 64 MiB/targets), with a floor of one
// byte, so the per-run retained total stays bounded regardless of target count.
func captureLimitForTargets(targetCount int) int {
	if targetCount <= 0 {
		return maxCapturedBytesPerTarget
	}
	limit := maxCapturedBytesPerRun / targetCount
	if limit > maxCapturedBytesPerTarget {
		limit = maxCapturedBytesPerTarget
	}
	if limit < 1 {
		limit = 1
	}
	return limit
}

// boundedBuffer collects combined child output up to a fixed byte limit and records whether
// any bytes were discarded. Its Write method never blocks or reports a short write, so a child
// process cannot stall on the capture limit.
type boundedBuffer struct {
	mutex     sync.Mutex
	limit     int
	buffer    bytes.Buffer
	truncated bool
}

// newBoundedBuffer returns a capture that retains at most limit bytes. A negative limit is
// treated as zero, which retains nothing and marks any non-empty write as truncated.
func newBoundedBuffer(limit int) *boundedBuffer {
	if limit < 0 {
		limit = 0
	}
	return &boundedBuffer{limit: limit}
}

// Write appends as much of data as the remaining budget allows, records truncation for the
// discarded remainder, and always reports the full write so the child process keeps running.
func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()

	remaining := buffer.limit - buffer.buffer.Len()
	if remaining < 0 {
		remaining = 0
	}
	writable := len(data)
	if writable > remaining {
		writable = remaining
	}
	if writable > 0 {
		_, _ = buffer.buffer.Write(data[:writable])
	}
	if writable < len(data) {
		buffer.truncated = true
	}
	return len(data), nil
}

// String returns the retained output collected so far.
func (buffer *boundedBuffer) String() string {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.buffer.String()
}

// Truncated reports whether any output was discarded because the capture limit was reached.
func (buffer *boundedBuffer) Truncated() bool {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.truncated
}

// boundedStreams collects stdout and stderr separately under one shared byte budget so a single
// Git process cannot retain more than its per-target allowance across both streams.
type boundedStreams struct {
	mutex           sync.Mutex
	remaining       int
	stdout          bytes.Buffer
	stderr          bytes.Buffer
	stdoutTruncated bool
	stderrTruncated bool
}

// newBoundedStreams returns a separate-stream capture sharing limit bytes between stdout and
// stderr. A negative limit is treated as zero, which marks any non-empty write as truncated.
func newBoundedStreams(limit int) *boundedStreams {
	if limit < 0 {
		limit = 0
	}
	return &boundedStreams{remaining: limit}
}

// StdoutWriter returns the bounded writer for a child process's standard output.
func (streams *boundedStreams) StdoutWriter() io.Writer {
	return boundStreamWriter{streams: streams}
}

// StderrWriter returns the bounded writer for a child process's standard error.
func (streams *boundedStreams) StderrWriter() io.Writer {
	return boundStreamWriter{streams: streams, stderr: true}
}

// boundStreamWriter adapts one side of boundedStreams to io.Writer without exposing the shared
// budget itself. It always reports the full write so the child process is never blocked.
type boundStreamWriter struct {
	streams *boundedStreams
	stderr  bool
}

// Write appends the portion of data that fits the shared budget and records truncation for the
// discarded remainder.
func (writer boundStreamWriter) Write(data []byte) (int, error) {
	streams := writer.streams
	streams.mutex.Lock()
	defer streams.mutex.Unlock()

	writable := len(data)
	if writable > streams.remaining {
		writable = streams.remaining
	}
	if writable > 0 {
		target := &streams.stdout
		if writer.stderr {
			target = &streams.stderr
		}
		_, _ = target.Write(data[:writable])
		streams.remaining -= writable
	}
	if writable < len(data) {
		if writer.stderr {
			streams.stderrTruncated = true
		} else {
			streams.stdoutTruncated = true
		}
	}
	return len(data), nil
}

// Snapshot returns the retained stdout and stderr text plus whether either stream was truncated.
func (streams *boundedStreams) Snapshot() (stdout string, stderr string, stdoutTruncated bool, stderrTruncated bool) {
	streams.mutex.Lock()
	defer streams.mutex.Unlock()
	return streams.stdout.String(), streams.stderr.String(), streams.stdoutTruncated, streams.stderrTruncated
}

// gitTruncationMessage describes a Git capture that exceeded its per-target limit, including
// which streams lost output, and returns an empty string when nothing was truncated.
func gitTruncationMessage(limit int, stdoutTruncated bool, stderrTruncated bool) string {
	switch {
	case stdoutTruncated && stderrTruncated:
		return fmt.Sprintf("Git output truncated after %d bytes (stdout and stderr)", limit)
	case stdoutTruncated:
		return fmt.Sprintf("Git output truncated after %d bytes (stdout)", limit)
	case stderrTruncated:
		return fmt.Sprintf("Git output truncated after %d bytes (stderr)", limit)
	default:
		return ""
	}
}

// commandTruncationMessage describes command output discarded at the per-target capture limit.
func commandTruncationMessage(limit int) string {
	return fmt.Sprintf("command output truncated after %d bytes", limit)
}

// execFailureText combines a command failure with a truncation note for JSON and text output.
// It accepts the command error, whether output was truncated, and the limit, and returns an empty
// string when neither condition applies.
func execFailureText(commandErr error, truncated bool, limit int) string {
	messages := make([]string, 0, 2)
	if commandErr != nil {
		messages = append(messages, commandErr.Error())
	}
	if truncated {
		messages = append(messages, commandTruncationMessage(limit))
	}
	return strings.Join(messages, "; ")
}
