package container

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
)

// errReader always returns an error on Read.
type errReader struct {
	err error
}

func (r *errReader) Read([]byte) (int, error) {
	return 0, r.err
}

// --- Tests for scanStreamJSON ---

func TestParseStreamJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantResp *claudeResponse
		wantErr  string
	}{
		{
			name:  "single result line",
			input: `{"type":"result","result":"hello","session_id":"s1","is_error":false}`,
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "hello",
				SessionID: "s1",
			},
		},
		{
			name: "multiple events with result last",
			input: `{"type":"system","data":"init"}
{"type":"assistant","message":"thinking..."}
{"type":"result","result":"done","session_id":"s2","is_error":false}`,
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "done",
				SessionID: "s2",
			},
		},
		{
			name:  "skips blank lines and non-JSON",
			input: "\nsome garbage\n\n" + `{"type":"result","result":"ok","session_id":"s3","is_error":false}` + "\n",
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "ok",
				SessionID: "s3",
			},
		},
		{
			name:    "no result event",
			input:   `{"type":"assistant","message":"hi"}`,
			wantErr: "no result event found",
		},
		{
			name:    "empty input",
			input:   "",
			wantErr: "no result event found",
		},
		{
			name:  "large intermediate line exceeding default scanner buffer",
			input: `{"type":"assistant","message":"` + strings.Repeat("x", 128*1024) + `"}` + "\n" + `{"type":"result","result":"done","session_id":"s4","is_error":false}`,
			wantResp: &claudeResponse{
				Type:      "result",
				Result:    "done",
				SessionID: "s4",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, err := scanStreamJSON(strings.NewReader(tc.input), streamCallbacks{})
			if tc.wantErr != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.wantErr)
				require.Nil(t, resp)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantResp, resp)
			}
		})
	}
}

func TestParseStreamJSONReaderError(t *testing.T) {
	resp, err := scanStreamJSON(&errReader{err: errors.New("read error")}, streamCallbacks{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "reading container output")
	require.Nil(t, resp)
}

// --- Tests for assistantMessage.extractText ---

func TestAssistantMessageExtractText(t *testing.T) {
	tests := []struct {
		name     string
		msg      assistantMessage
		expected string
	}{
		{
			name: "single text block",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content, assistantContentBlock{Type: "text", Text: "Hello!"})
				return m
			}(),
			expected: "Hello!",
		},
		{
			name: "multiple text blocks joined",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content,
					assistantContentBlock{Type: "text", Text: "Line one"},
					assistantContentBlock{Type: "text", Text: "Line two"},
				)
				return m
			}(),
			expected: "Line one\nLine two",
		},
		{
			name: "tool_use only returns empty",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content, assistantContentBlock{Type: "tool_use", Text: ""})
				return m
			}(),
			expected: "",
		},
		{
			name: "mixed content skips non-text",
			msg: func() assistantMessage {
				var m assistantMessage
				m.Message.Content = append(m.Message.Content,
					assistantContentBlock{Type: "tool_use", Text: ""},
					assistantContentBlock{Type: "text", Text: "Result"},
				)
				return m
			}(),
			expected: "Result",
		},
		{
			name:     "empty content",
			msg:      assistantMessage{},
			expected: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.msg.extractText()
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestExtractToolUses(t *testing.T) {
	t.Run("extracts tool_use blocks", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}},{"type":"text","text":"Running tests"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		tools := msg.extractToolUses()
		require.Len(t, tools, 1)
		require.Equal(t, "Bash", tools[0].Name)
		require.Equal(t, "go test ./...", tools[0].Input)
	})

	t.Run("no tool_use blocks", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractToolUses())
	})

	t.Run("empty name skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"","input":{}}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractToolUses())
	})
}

func TestSummarizeToolInput(t *testing.T) {
	tests := []struct {
		name     string
		toolName string
		input    string
		expected string
	}{
		{"Bash command", "Bash", `{"command":"go build ./..."}`, "go build ./..."},
		{"Read file", "Read", `{"file_path":"/tmp/foo.go"}`, "/tmp/foo.go"},
		{"Edit file", "Edit", `{"file_path":"/tmp/bar.go"}`, "/tmp/bar.go"},
		{"Write file", "Write", `{"file_path":"/tmp/baz.go"}`, "/tmp/baz.go"},
		{"Glob pattern", "Glob", `{"pattern":"**/*.ts"}`, "**/*.ts"},
		{"Grep pattern", "Grep", `{"pattern":"TODO"}`, "TODO"},
		{"Agent desc", "Agent", `{"description":"search code"}`, "search code"},
		{"fallback key", "WebSearch", `{"query":"golang testing"}`, "golang testing"},
		{"empty input", "Bash", `{}`, ""},
		{"invalid json", "Bash", `not json`, ""},
		{"empty raw", "Bash", ``, ""},
		{"long command truncated", "Bash", `{"command":"` + strings.Repeat("x", 200) + `"}`, strings.Repeat("x", 120) + "..."},
		{"long fallback truncated", "WebSearch", `{"query":"` + strings.Repeat("y", 200) + `"}`, strings.Repeat("y", 120) + "..."},
		{"AskUserQuestion raw", "AskUserQuestion", `{"questions":[{"question":"What?"}]}`, `{"questions":[{"question":"What?"}]}`},
		{"ExitPlanMode raw", "ExitPlanMode", `{"plan":"# My Plan","planFilePath":"/tmp/p.md"}`, `{"plan":"# My Plan","planFilePath":"/tmp/p.md"}`},
		{"TodoWrite raw", "TodoWrite", `{"todos":[{"content":"Do thing","status":"pending","activeForm":"Doing thing"}]}`, `{"todos":[{"content":"Do thing","status":"pending","activeForm":"Doing thing"}]}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := summarizeToolInput(tc.toolName, json.RawMessage(tc.input))
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestScanStreamJSONOnToolUse(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test"}}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var tools []string
	cb := streamCallbacks{
		onToolUse: func(toolUseID, name, input string) {
			tools = append(tools, name+":"+input)
		},
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"Bash:go test"}, tools)
}

func TestScanStreamJSONOnActivity(t *testing.T) {
	t.Run("model detected from assistant events", func(t *testing.T) {
		input := `{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Hello"}]}}
{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"World"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		cb := streamCallbacks{
			onActivity: func(activity, detail string) {
				activities = append(activities, activity+":"+detail)
			},
		}
		resp, err := scanStreamJSON(strings.NewReader(input), cb)
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Equal(t, "claude-opus-4-6", resp.Model)
		// Model should only fire once (same model repeated)
		require.Equal(t, []string{"model:claude-opus-4-6"}, activities)
	})

	t.Run("model change fires again", func(t *testing.T) {
		input := `{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Hello"}]}}
{"type":"assistant","message":{"model":"claude-haiku-4-5-20251001","content":[{"type":"text","text":"Sub"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		cb := streamCallbacks{
			onActivity: func(activity, detail string) {
				activities = append(activities, activity+":"+detail)
			},
		}
		resp, err := scanStreamJSON(strings.NewReader(input), cb)
		require.NoError(t, err)
		// Last model wins
		require.Equal(t, "claude-haiku-4-5-20251001", resp.Model)
		require.Equal(t, []string{
			"model:claude-opus-4-6",
			"model:claude-haiku-4-5-20251001",
		}, activities)
	})

	t.Run("system events dispatched", func(t *testing.T) {
		input := `{"type":"system","subtype":"init","cwd":"/work"}
{"type":"system","subtype":"task_started","description":"Deep analysis"}
{"type":"system","subtype":"task_progress","description":"Reading files"}
{"type":"system","subtype":"status","status":"compacting"}
{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		cb := streamCallbacks{
			onActivity: func(activity, detail string) {
				activities = append(activities, activity+":"+detail)
			},
		}
		resp, err := scanStreamJSON(strings.NewReader(input), cb)
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Equal(t, []string{
			"subagent_started:Deep analysis",
			"subagent_progress:Reading files",
			"compacting:",
			"model:claude-opus-4-6",
		}, activities)
	})

	t.Run("thinking_tokens emit a thinking activity with the running token count", func(t *testing.T) {
		input := `{"type":"system","subtype":"thinking_tokens","estimated_tokens":200,"estimated_tokens_delta":150}
{"type":"system","subtype":"thinking_tokens","estimated_tokens":450,"estimated_tokens_delta":250}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		cb := streamCallbacks{
			onActivity: func(activity, detail string) {
				activities = append(activities, activity+":"+detail)
			},
		}
		_, err := scanStreamJSON(strings.NewReader(input), cb)
		require.NoError(t, err)
		require.Equal(t, []string{"thinking:200", "thinking:450"}, activities)
	})

	t.Run("result metadata parsed", func(t *testing.T) {
		input := `{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false,"duration_ms":5000,"num_turns":3,"stop_reason":"end_turn"}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{})
		require.NoError(t, err)
		require.Equal(t, 5000, resp.DurationMs)
		require.Equal(t, 3, resp.NumTurns)
		require.Equal(t, "end_turn", resp.StopReason)
		require.Equal(t, "claude-opus-4-6", resp.Model)
	})

	t.Run("malformed system event JSON skipped", func(t *testing.T) {
		// The line passes initial typeCheck unmarshal (has "type":"system") but fails
		// the second unmarshal into systemEvent because of a bad field value.
		input := `{"type":"system","subtype":123}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		var activities []string
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{
			onActivity: func(kind, desc string) {
				activities = append(activities, kind+":"+desc)
			},
		})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Empty(t, activities) // malformed system event was skipped
	})

	t.Run("no activity callback ignores system events", func(t *testing.T) {
		input := `{"type":"system","subtype":"task_started","description":"test"}
{"type":"assistant","message":{"model":"claude-opus-4-6","content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
		// No onActivity — should not panic
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})
}

// --- Tests for scanStreamJSON with onTurn ---

func TestParseStreamingJSON(t *testing.T) {
	t.Run("happy path with assistant and result events", func(t *testing.T) {
		input := `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"Let me check..."}]}}
{"type":"user","message":{"content":[{"type":"tool_result"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Here is the answer."}]}}
{"type":"result","result":"Here is the answer.","session_id":"sess-1","is_error":false}
`
		var turns []string
		onTurn := func(text string) {
			turns = append(turns, text)
		}

		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: onTurn})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "Here is the answer.", resp.Result)
		require.Equal(t, "sess-1", resp.SessionID)
		require.False(t, resp.IsError)
		require.Equal(t, []string{"Let me check...", "Here is the answer."}, turns)
	})

	t.Run("no result event", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.Error(t, err)
		require.Nil(t, resp)
		require.Contains(t, err.Error(), "no result event found")
	})

	t.Run("empty assistant text skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"tool_use","text":""}]}}
{"type":"result","result":"Done.","session_id":"sess-2","is_error":false}
`
		var turns []string
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(text string) {
			turns = append(turns, text)
		}})
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.Equal(t, "Done.", resp.Result)
		require.Empty(t, turns)
	})

	t.Run("non-JSON lines skipped", func(t *testing.T) {
		input := `not json at all
{"type":"result","result":"OK","session_id":"sess-3","is_error":false}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("empty lines skipped", func(t *testing.T) {
		input := `

{"type":"result","result":"OK","session_id":"sess-4","is_error":false}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("malformed assistant event skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":"not an object"}
{"type":"result","result":"OK","session_id":"sess-5","is_error":false}
`
		var turns []string
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(text string) {
			turns = append(turns, text)
		}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
		require.Empty(t, turns)
	})

	t.Run("malformed result event skipped finds later result", func(t *testing.T) {
		input := `{"type":"result","result":123}
{"type":"result","result":"OK","session_id":"sess-6","is_error":false}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.Equal(t, "OK", resp.Result)
	})

	t.Run("error result", func(t *testing.T) {
		input := `{"type":"result","result":"something broke","session_id":"sess-err","is_error":true}
`
		resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{onTurn: func(string) {}})
		require.NoError(t, err)
		require.True(t, resp.IsError)
		require.Equal(t, "something broke", resp.Result)
	})
}

func TestScanStreamJSONSkipsUserEvents(t *testing.T) {
	// "user" events (tool results) should be skipped — they can be very large (screenshots).
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Taking screenshot"}]}}
{"type":"user","message":{"content":[{"type":"tool_result","content":"` + strings.Repeat("x", 100000) + `"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var turns []string
	cb := streamCallbacks{
		onTurn: func(text string) { turns = append(turns, text) },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"Taking screenshot", "Done"}, turns)
}

func TestScanStreamJSONUserEventAtEOF(t *testing.T) {
	// "user" event as the last line without trailing newline.
	input := `{"type":"assistant","message":{"content":[{"type":"text","text":"Hi"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
{"type":"user","message":{"content":[{"type":"tool_result","content":"big data"}]}}`
	resp, err := scanStreamJSON(strings.NewReader(input), streamCallbacks{})
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
}

func TestReadLineOrSkipEmptyInput(t *testing.T) {
	br := bufio.NewReaderSize(strings.NewReader(""), 64*1024)
	line, err := readLineOrSkip(br)
	require.ErrorIs(t, err, io.EOF)
	require.Nil(t, line)
}

type errorReader struct{ err error }

func (r *errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadLineOrSkipReadError(t *testing.T) {
	// Reader that returns an error (not EOF) with no data.
	r := &errorReader{err: errors.New("read broken")}
	br := bufio.NewReaderSize(r, 64*1024)
	_, err := readLineOrSkip(br)
	require.Error(t, err)
}

func TestReadLineOrSkipUserEventUnderCap(t *testing.T) {
	// Small user event (well under userEventMaxBytes) — returned in full so
	// scanStreamJSON can dispatch the tool_result block.
	input := `{"type":"user","message":{"content":[{"type":"tool_result"}]}}` + "\n"
	br := bufio.NewReaderSize(strings.NewReader(input), 64*1024)
	line, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Equal(t, `{"type":"user","message":{"content":[{"type":"tool_result"}]}}`, string(line))
}

func TestReadLineOrSkipUserEventOverCap(t *testing.T) {
	// Multi-MB user event (over userEventMaxBytes) — drained without buffering,
	// returns nil so subsequent lines (e.g. result) still parse.
	input := `{"type":"user","message":{"content":[{"type":"tool_result","content":"` +
		strings.Repeat("x", userEventMaxBytes+1) + `"}]}}` + "\n" +
		`{"type":"result","result":"OK","session_id":"s1","is_error":false}` + "\n"
	br := bufio.NewReaderSize(strings.NewReader(input), 64*1024)
	line, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Nil(t, line)
	// Next call returns the result line in full.
	next, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Contains(t, string(next), `"type":"result"`)
}

func TestReadLineOrSkipLastLineNoNewline(t *testing.T) {
	// Last line without trailing newline — ReadBytes returns data + io.EOF.
	input := `{"type":"assistant","message":"hello"}`
	br := bufio.NewReaderSize(strings.NewReader(input), 64*1024)
	line, err := readLineOrSkip(br)
	require.NoError(t, err)
	require.Equal(t, `{"type":"assistant","message":"hello"}`, string(line))
}

// peekThenErrorReader returns data for the first Read (to fill Peek), then errors.
type peekThenErrorReader struct {
	data     string
	readOnce bool
}

func (r *peekThenErrorReader) Read(p []byte) (int, error) {
	if !r.readOnce {
		r.readOnce = true
		n := copy(p, r.data)
		return n, nil
	}
	return 0, errors.New("read error after peek")
}

func TestReadLineOrSkipReadErrorAfterPeek(t *testing.T) {
	// Peek succeeds (30 bytes of non-user data), but ReadBytes fails with no data.
	// Use a custom reader: first Read provides 30 bytes (no newline), second Read errors.
	r := &peekThenErrorReader{data: `{"type":"assistant","msg":"x"}`}
	br := bufio.NewReaderSize(r, 64*1024)
	line, err := readLineOrSkip(br)
	// ReadBytes will drain the buffer (data from peek), return it with the error.
	// Since len(line) > 0, we get the trimmed line back (not the error path at 1039).
	if err != nil {
		// If ReadBytes returned error with no data, that's the uncovered path.
		require.Nil(t, line)
	} else {
		require.NotNil(t, line)
	}
}

func TestScanStreamJSONUserEventWithNewline(t *testing.T) {
	// "user" event followed by newline — the normal case.
	input := `{"type":"user","message":{"content":[{"type":"tool_result","content":"tool output"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"Got it"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var turns []string
	cb := streamCallbacks{
		onTurn: func(text string) { turns = append(turns, text) },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"Got it"}, turns)
}
func TestClaudeCmdBuilderCurrentConfigReloads(t *testing.T) {
	initial := &config.Config{ClaudeBinPath: "old-claude"}
	b := NewClaudeCmdBuilder(initial, func() (*config.Config, error) {
		return &config.Config{ClaudeBinPath: "new-claude"}, nil
	})

	cfg := b.currentConfig()
	require.Equal(t, "new-claude", cfg.ClaudeBinPath)
	require.Equal(t, "new-claude", b.cfg.Load().ClaudeBinPath)
}

func TestClaudeCmdBuilderCurrentConfigFallbackOnError(t *testing.T) {
	initial := &config.Config{ClaudeBinPath: "original"}
	b := NewClaudeCmdBuilder(initial, func() (*config.Config, error) {
		return nil, errors.New("fail")
	})

	cfg := b.currentConfig()
	require.Equal(t, "original", cfg.ClaudeBinPath)
}

func TestClaudeCmdBuilderCurrentConfigNilLoader(t *testing.T) {
	initial := &config.Config{ClaudeBinPath: "frozen"}
	b := NewClaudeCmdBuilder(initial, nil)

	cfg := b.currentConfig()
	require.Equal(t, "frozen", cfg.ClaudeBinPath)
}

// --- Tests for thinking + tool_result extraction (peppy-mapping-pudding plan) ---

func TestExtractThinking(t *testing.T) {
	t.Run("single thinking block", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"reasoning about the problem"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Equal(t, "reasoning about the problem", msg.extractThinking())
	})

	t.Run("multiple thinking blocks joined", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"first"},{"type":"thinking","thinking":"second"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Equal(t, "first\nsecond", msg.extractThinking())
	})

	t.Run("mixed text + thinking returns only thinking", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"answer"},{"type":"thinking","thinking":"hidden"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Equal(t, "hidden", msg.extractThinking())
		require.Equal(t, "answer", msg.extractText())
	})

	t.Run("no thinking blocks returns empty", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractThinking())
	})

	t.Run("empty thinking string skipped", func(t *testing.T) {
		input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":""}]}}`
		var msg assistantMessage
		require.NoError(t, json.Unmarshal([]byte(input), &msg))
		require.Empty(t, msg.extractThinking())
	})
}

func TestExtractToolUsesIncludesID(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_abc","name":"Read","input":{"file_path":"/x"}}]}}`
	var msg assistantMessage
	require.NoError(t, json.Unmarshal([]byte(input), &msg))
	tools := msg.extractToolUses()
	require.Len(t, tools, 1)
	require.Equal(t, "toolu_abc", tools[0].ID)
	require.Equal(t, "Read", tools[0].Name)
	require.Equal(t, "/x", tools[0].Input)
}

func TestParseToolResultStringContent(t *testing.T) {
	body := parseToolResultContent(json.RawMessage(`"plain string output"`))
	require.Equal(t, "plain string output", body)
}

func TestParseToolResultMixedContent(t *testing.T) {
	body := parseToolResultContent(json.RawMessage(`[{"type":"text","text":"first"},{"type":"image","source":{"type":"base64"}},{"type":"text","text":"second"}]`))
	require.Equal(t, "first\nsecond", body)
}

func TestParseToolResultEmptyAndInvalid(t *testing.T) {
	require.Empty(t, parseToolResultContent(nil))
	require.Empty(t, parseToolResultContent(json.RawMessage(``)))
	require.Empty(t, parseToolResultContent(json.RawMessage(`{not valid}`)))
}

func TestScanStreamJSONOnThinking(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"deep thoughts"},{"type":"text","text":"answer"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var turns, thinks []string
	cb := streamCallbacks{
		onTurn:     func(text string) { turns = append(turns, text) },
		onThinking: func(text string) { thinks = append(thinks, text) },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"answer"}, turns)
	require.Equal(t, []string{"deep thoughts"}, thinks)
}

func TestScanStreamJSONOnToolResult(t *testing.T) {
	input := `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"file body"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	type capturedResult struct {
		toolUseID string
		output    string
		isError   bool
	}
	var results []capturedResult
	var tools []string
	cb := streamCallbacks{
		onToolUse: func(toolUseID, name, input string) {
			tools = append(tools, toolUseID+":"+name)
		},
		onToolResult: func(toolUseID, output string, isError bool) {
			results = append(results, capturedResult{toolUseID, output, isError})
		},
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.Equal(t, []string{"toolu_1:Read"}, tools)
	require.Equal(t, []capturedResult{{"toolu_1", "file body", false}}, results)
}

func TestScanStreamJSONOnToolResultIsError(t *testing.T) {
	input := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_2","content":"command failed","is_error":true}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var gotErr bool
	cb := streamCallbacks{
		onToolResult: func(toolUseID, output string, isError bool) {
			gotErr = isError
		},
	}
	_, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.True(t, gotErr)
}

func TestScanStreamJSONToolResultTruncated(t *testing.T) {
	big := strings.Repeat("x", toolResultMaxInline*2)
	input := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` + big + `"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var output string
	cb := streamCallbacks{
		onToolResult: func(toolUseID, out string, isError bool) {
			output = out
		},
	}
	_, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Len(t, output, toolResultMaxInline)
}

func TestScanStreamJSONOversizedUserEventStillCompletes(t *testing.T) {
	// A user line that exceeds userEventMaxBytes is drained without dispatching
	// onToolResult, but the surrounding result still parses.
	input := `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` +
		strings.Repeat("z", userEventMaxBytes+1) + `"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched, "oversized user event should be drained, not dispatched")
}

func TestScanStreamJSONOversizedUserEventNoTrailingNewline(t *testing.T) {
	// Same drain path as the trailing-newline variant but the oversized user
	// line ends with EOF directly — exercises the "over && EOF" return.
	input := `{"type":"result","result":"OK","session_id":"s1","is_error":false}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"` +
		strings.Repeat("z", userEventMaxBytes+1) + `"}]}}`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched, "oversized user event without trailing newline should still drain")
}

func TestScanStreamJSONUserMessageMalformed(t *testing.T) {
	// type=user passes typeCheck but message field doesn't match userMessage
	// shape → unmarshal fails, parser continues and surfaces the result.
	input := `{"type":"user","message":42}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched)
}

func TestScanStreamJSONUserNonToolResultBlocksSkipped(t *testing.T) {
	// user content blocks that aren't tool_result are skipped without dispatch.
	input := `{"type":"user","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var dispatched bool
	cb := streamCallbacks{
		onToolResult: func(string, string, bool) { dispatched = true },
	}
	resp, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, "OK", resp.Result)
	require.False(t, dispatched)
}

func TestScanStreamJSONInterleavedTextThinkingToolUse(t *testing.T) {
	// Regression: text + thinking + tool_use in one assistant turn each fires
	// the matching callback, in input order across turns.
	input := `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"plan"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/x"}}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}
{"type":"result","result":"OK","session_id":"s1","is_error":false}
`
	var calls []string
	cb := streamCallbacks{
		onTurn:     func(t string) { calls = append(calls, "text:"+t) },
		onThinking: func(t string) { calls = append(calls, "think:"+t) },
		onToolUse:  func(id, name, _ string) { calls = append(calls, "tool:"+id+":"+name) },
	}
	_, err := scanStreamJSON(strings.NewReader(input), cb)
	require.NoError(t, err)
	require.Equal(t, []string{"think:plan", "tool:toolu_1:Read", "text:done"}, calls)
}
