package container

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// failAfterReader serves data, then fails with err.
type failAfterReader struct {
	data io.Reader
	err  error
}

func (r *failAfterReader) Read(p []byte) (int, error) {
	n, err := r.data.Read(p)
	if err == io.EOF {
		return n, r.err
	}
	return n, err
}

func TestParseStreamJSONReaderErrorCarriesTail(t *testing.T) {
	resp, err := scanStreamJSON(&failAfterReader{data: strings.NewReader("npm ERR! write failed\n"), err: errors.New("read error")}, streamCallbacks{})
	require.Nil(t, resp)
	require.EqualError(t, err, "reading container output: read error; last output:\nnpm ERR! write failed")
}

func TestOutputTail(t *testing.T) {
	numbered := func(from, to int) []string {
		var l []string
		for i := from; i < to; i++ {
			l = append(l, fmt.Sprintf("line %d", i))
		}
		return l
	}
	tests := []struct {
		name  string
		lines []string
		want  string
	}{
		{
			name: "empty",
			want: "",
		},
		{
			name:  "keeps the last lines",
			lines: numbered(0, outputTailLines+5),
			want:  strings.Join(numbered(5, outputTailLines+5), "\n"),
		},
		{
			name:  "trims bytes from the front",
			lines: []string{strings.Repeat("a", outputTailMaxBytes), "tail"},
			want:  "…" + strings.Repeat("a", outputTailMaxBytes-5) + "\ntail",
		},
		{
			name:  "does not split a character",
			lines: []string{"é" + strings.Repeat("b", outputTailMaxBytes-1)},
			want:  "…" + strings.Repeat("b", outputTailMaxBytes-1),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tail outputTail
			for _, l := range tc.lines {
				tail.add(l)
			}
			require.Equal(t, tc.want, tail.String())
		})
	}
}

func TestClaudeResponseErrorText(t *testing.T) {
	tests := []struct {
		name string
		resp claudeResponse
		want string
	}{
		{"result text wins", claudeResponse{Result: "Prompt is too long", Subtype: "error_during_execution", Output: "noise"}, "Prompt is too long"},
		{"subtype, errors and output", claudeResponse{Subtype: "error_during_execution", Errors: []string{"ENOSPC: no space left on device"}, Output: "Error: write failed"}, "error_during_execution; ENOSPC: no space left on device; last output:\nError: write failed"},
		{"subtype only", claudeResponse{Subtype: "error_max_turns"}, "error_max_turns"},
		{"nothing", claudeResponse{}, "no error details"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.resp.errorText())
		})
	}
}

func TestExitError(t *testing.T) {
	parseErr := errors.New("parsing claude response: no result event found")
	tests := []struct {
		name string
		code int64
		want string
	}{
		{"clean exit", 0, "parsing claude response: no result event found"},
		{"failure", 1, "container exited with code 1: parsing claude response: no result event found"},
		{"killed", 137, "container exited with code 137 (killed — out of memory or out of disk space): parsing claude response: no result event found"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := exitError(tc.code, parseErr)
			require.EqualError(t, err, tc.want)
			require.ErrorIs(t, err, parseErr)
		})
	}
}
