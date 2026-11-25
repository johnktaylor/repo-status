package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestEscapeDisplayEscapesLineSeparators verifies that LF and the Unicode
// line/paragraph separators become visible escape sequences in display fields.
func TestEscapeDisplayEscapesLineSeparators(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "LF", input: "first\nsecond", want: `first\x0asecond`},
		{name: "line separator", input: "first\u2028second", want: `first\u2028second`},
		{name: "paragraph separator", input: "first\u2029second", want: `first\u2029second`},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := escapeDisplay(testCase.input); got != testCase.want {
				t.Fatalf("escapeDisplay(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// TestWriteDisplayErrorKeepsOnePrefixedLine verifies that a multiline error is
// rendered as one prefixed line with LF and Unicode separators escaped.
func TestWriteDisplayErrorKeepsOnePrefixedLine(t *testing.T) {
	var destination bytes.Buffer
	writeDisplayError(&destination, "Error: ", errors.New("first line\nsecond \u2028line\u2029"))

	want := "Error: first line\\x0asecond \\u2028line\\u2029\n"
	if destination.String() != want {
		t.Fatalf("writeDisplayError() = %q, want %q", destination.String(), want)
	}
	if strings.Count(destination.String(), "\n") != 1 {
		t.Fatalf("writeDisplayError() produced %d lines, want one prefixed line", strings.Count(destination.String(), "\n"))
	}
}

// TestRenderLimitWriterRejectsOverBudgetWithoutPartialWrite verifies that a
// rendered write exceeding the remaining budget fails without forwarding any
// of that chunk, while a chunk that exactly fits succeeds.
func TestRenderLimitWriterRejectsOverBudgetWithoutPartialWrite(t *testing.T) {
	var destination bytes.Buffer
	writer := &renderLimitWriter{writer: &destination, remaining: 4}

	written, err := writer.Write([]byte("abcd"))
	if err != nil {
		t.Fatalf("write exact budget: %v", err)
	}
	if written != 4 {
		t.Fatalf("write exact budget reported %d bytes, want 4", written)
	}

	if _, err := writer.Write([]byte("e")); !errors.Is(err, errRenderedOutputTooLarge) {
		t.Fatalf("over-budget write error = %v, want errRenderedOutputTooLarge", err)
	}
	if destination.String() != "abcd" {
		t.Fatalf("destination = %q, want no partial over-budget write", destination.String())
	}
}
