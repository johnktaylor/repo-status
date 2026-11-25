package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// reportTempPattern creates a short, fixed-shape private temporary name in the destination
// directory. It deliberately avoids the destination basename so a long or stream-shaped name
// cannot exceed path limits or be reinterpreted by the filesystem.
const reportTempPattern = ".repo-status-report-*.tmp"

// escapeDisplay makes a configuration-derived value or free-form diagnostic safe for single-line
// terminal output by spelling control, bidi formatting, line-separator, and paragraph-separator
// characters as visible escape sequences. It accepts a string and returns printable text; ordinary
// characters, including Windows path separators, are returned unchanged.
func escapeDisplay(value string) string {
	var builder strings.Builder
	for _, char := range value {
		if !unicode.IsControl(char) && !unicode.Is(unicode.Bidi_Control, char) &&
			char != '\u2028' && char != '\u2029' {
			builder.WriteRune(char)
			continue
		}
		if char <= 0xFF {
			fmt.Fprintf(&builder, `\x%02x`, char)
			continue
		}
		fmt.Fprintf(&builder, `\u%04x`, char)
	}
	return builder.String()
}

// escapeDisplayLines escapes Git porcelain status one line at a time while preserving LF
// separators. It accepts a possibly multi-line status string and returns a terminal-safe copy.
func escapeDisplayLines(value string) string {
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = escapeDisplay(lines[index])
	}
	return strings.Join(lines, "\n")
}

// ensureNewOutputPath verifies that destination is a supported new path, so a later publish can
// refuse to overwrite a file, directory, symbolic link, hard link, or the configuration file.
// It accepts a destination path and returns an error for alternate data stream syntax, an
// existing path, or a path that cannot be checked.
func ensureNewOutputPath(destination string) error {
	if err := rejectWindowsAlternateDataStream(destination); err != nil {
		return err
	}
	_, err := os.Lstat(destination)
	if err == nil {
		return fmt.Errorf("output file already exists; refusing to overwrite: %s", destination)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("checking output destination: %w", err)
	}
	return nil
}

// publishReportFile writes completed report bytes to a new destination without overwriting it.
// It delegates to publishRenderedFile so the same no-clobber and rendered-budget rules apply.
func publishReportFile(destination string, report []byte) error {
	return publishRenderedFile(destination, func(writer io.Writer) error {
		_, err := writer.Write(report)
		return err
	})
}

// publishRenderedFile renders a report into a private temporary file in the destination
// directory, then publishes it with a same-directory hard link that fails if the destination
// appeared in the meantime. It accepts the destination and a render callback, and returns an
// error for alternate data stream syntax, a failed or over-budget render, or any later failure;
// the temporary file is always removed and a failed render is never published. The destination
// parent is assumed to be trusted.
func publishRenderedFile(destination string, render func(io.Writer) error) error {
	if err := rejectWindowsAlternateDataStream(destination); err != nil {
		return err
	}
	directory := filepath.Dir(destination)
	tempFile, err := os.CreateTemp(directory, reportTempPattern)
	if err != nil {
		return fmt.Errorf("creating temporary output file: %w", err)
	}
	tempPath := tempFile.Name()
	// Remove the private temporary name whether publishing succeeds or fails. A successful hard
	// link leaves the destination referring to the same completed file independently.
	defer os.Remove(tempPath)

	if err := render(newRenderLimitWriter(tempFile)); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("rendering report: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return fmt.Errorf("flushing temporary output file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("closing temporary output file: %w", err)
	}

	// Linking is atomic and refuses to replace an existing destination, unlike a rename on all
	// platforms. Filesystems without hard links report a publish error instead of clobbering.
	if err := os.Link(tempPath, destination); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fmt.Errorf("output file already exists; refusing to overwrite: %s", destination)
		}
		return fmt.Errorf("publishing output file: %w", err)
	}
	return nil
}
