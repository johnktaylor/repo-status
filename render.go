package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// maxRenderedBytesPerRun bounds status text and result JSON output (list/status/exec JSON). It is
// intentionally larger than the child-output capture budget so normal JSON escaping and report
// structure still fit while a pathological expansion fails cleanly. It is a variable so tests can
// shorten it.
var maxRenderedBytesPerRun = 128 << 20 // 128 MiB

// maxExecTextBytesPerRun bounds the streamed exec text path, where dry-run command text is prepared
// once and raw trusted child output is passed through unchanged. It is a variable so tests can
// shorten it.
var maxExecTextBytesPerRun = 128 << 20 // 128 MiB

// errRenderedOutputTooLarge marks a render that would exceed its configured output limit.
var errRenderedOutputTooLarge = errors.New("rendered output exceeds the per-run limit")

// renderLimitWriter writes at most its configured limit and fails before emitting a partial
// over-budget write. It accepts the destination and returns write or limit errors.
type renderLimitWriter struct {
	writer    io.Writer
	limit     int
	remaining int
}

// newRenderLimitWriter returns a writer that enforces maxRenderedBytesPerRun on its destination.
func newRenderLimitWriter(writer io.Writer) *renderLimitWriter {
	return newRenderLimitWriterWithLimit(writer, maxRenderedBytesPerRun)
}

// newRenderLimitWriterWithLimit returns a writer that enforces an explicit byte limit. A negative
// limit is treated as zero so all non-empty writes fail cleanly.
func newRenderLimitWriterWithLimit(writer io.Writer, limit int) *renderLimitWriter {
	if limit < 0 {
		limit = 0
	}
	return &renderLimitWriter{writer: writer, limit: limit, remaining: limit}
}

// Write forwards data only when it fits the remaining rendered budget, otherwise it returns a
// limit error without writing a partial chunk.
func (writer *renderLimitWriter) Write(data []byte) (int, error) {
	if len(data) > writer.remaining {
		return 0, fmt.Errorf("%w (%d bytes)", errRenderedOutputTooLarge, writer.limit)
	}
	written, err := writer.writer.Write(data)
	writer.remaining -= written
	if err != nil {
		return written, err
	}
	if written != len(data) {
		return written, io.ErrShortWrite
	}
	return written, nil
}

// writeJSONArray writes an indented JSON array one record at a time, preserving record order.
// It accepts the destination, record count, and a callback that marshals one record, and returns
// an encoding or write error. The caller is responsible for limiting the destination.
func writeJSONArray(writer io.Writer, count int, record func(int) ([]byte, error)) error {
	if count <= 0 {
		_, err := io.WriteString(writer, "[]")
		return err
	}
	if _, err := io.WriteString(writer, "["); err != nil {
		return err
	}
	for index := 0; index < count; index++ {
		encoded, err := record(index)
		if err != nil {
			return err
		}
		if index > 0 {
			if _, err := io.WriteString(writer, ","); err != nil {
				return err
			}
		}
		if _, err := io.WriteString(writer, "\n  "); err != nil {
			return err
		}
		written, err := writer.Write(encoded)
		if err != nil {
			return err
		}
		if written != len(encoded) {
			return io.ErrShortWrite
		}
	}
	_, err := io.WriteString(writer, "\n]")
	return err
}

// marshalIndentedRecord encodes one record with the indentation used inside a two-space array.
func marshalIndentedRecord(value any) ([]byte, error) {
	return json.MarshalIndent(value, "  ", "  ")
}

// writeDisplayError writes a single-line error message with display-unsafe characters escaped.
// It accepts the destination and a static prefix and has no return value.
func writeDisplayError(writer io.Writer, prefix string, err error) {
	fmt.Fprintf(writer, "%s%s\n", prefix, escapeDisplay(err.Error()))
}
