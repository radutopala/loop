package api

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// agentRequestBodies are the bodies of the agent routes whose handlers read a
// channel, a thread, a dir, a scope or a workflow from them.
func agentRequestBodies() []any {
	return []any{
		&sendMessageRequest{}, &createThreadRequest{}, &createChannelRequest{}, &createWorktreeRequest{},
		&createTaskRequest{}, &updateTaskRequest{}, &moveTaskRequest{},
		&shortcutModifyRequest{}, &bashShortcutModifyRequest{},
		&memorySearchRequest{}, &memoryIndexRequest{},
		&startWorkflowRunRequest{}, &workflowModifyRequest{}, &browserActionRequest{},
	}
}

// jsonName is a struct field's JSON key.
func jsonName(f reflect.StructField) string {
	name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
	if name == "" {
		return f.Name
	}
	return name
}

// stringFields returns v's string and *string fields by JSON key; a nil
// *string reads as "".
func stringFields(v any) map[string]string {
	rv := reflect.ValueOf(v).Elem()
	out := map[string]string{}
	for _, f := range reflect.VisibleFields(rv.Type()) {
		if f.Anonymous || !f.IsExported() {
			continue
		}
		fv := rv.FieldByIndex(f.Index)
		switch {
		case fv.Kind() == reflect.String:
			out[jsonName(f)] = fv.String()
		case fv.Kind() == reflect.Pointer && fv.Type().Elem().Kind() == reflect.String:
			if !fv.IsNil() {
				out[jsonName(f)] = fv.Elem().String()
			} else {
				out[jsonName(f)] = ""
			}
		}
	}
	return out
}

// requireRefsParity checks the guard reads from body the refs every handler
// that accepts body would use.
func requireRefsParity(t require.TestingT, body []byte) {
	refs := decodeRequestRefs(body)
	guard := stringFields(&refs)
	for _, dst := range agentRequestBodies() {
		if err := json.NewDecoder(strings.NewReader(string(body))).Decode(dst); err != nil {
			continue // decodeJSON answers 400; the handler reads nothing
		}
		for key, got := range stringFields(dst) {
			if want, ok := guard[key]; ok {
				require.Equal(t, got, want, "%T reads %s %q from %s, the guard %q", dst, key, got, body, want)
			}
		}
	}
}

var refsParityBodies = []string{
	`{"channel_id":"own"}`,
	`{"channel_id":"own","Channel_ID":"other"}`,
	`{"Channel_ID":"other","channel_id":"own"}`,
	`{"CHANNEL_ID":"other"}`,
	`{"channel_id":"own","channel_id":"other"}`,
	`{"channel_id":"other","channel_id":null}`,
	`{"channel_id":"other","channel_id":5}`,
	`{"channel_id":"other"}`,
	`{"channel_id":"own"}{"channel_id":"other"}`,
	`{"dir_path":"/proj","DIR_PATH":"/etc"}`,
	`{"dir_path":"/proj","Dir_Path":"/etc","dir_path":"/proj/x"}`,
	`{"thread_id":"own","THREAD_ID":"other"}`,
	`{"scope":"project","ſcope":"global"}`,
	`{"SCOPE":"global"}`,
	`{"workflow_name":"","wor` + "K" + `flow_name":"w"}`,
	`{"channel_id":"own","nested":{"channel_id":"other"}}`,
	`{"channel_id":["other"]}`,
	`not json`,
	``,
}

// The guard decodes a body the way the handlers do, encoding/json into a
// struct: keys match case-insensitively, Unicode folding included (ſ is s, K
// is k), and the last match wins, so a key spelled two ways can't show the
// guard one ref and the handler another.
func TestRequestRefsParity(t *testing.T) {
	for _, body := range refsParityBodies {
		t.Run(body, func(t *testing.T) {
			requireRefsParity(t, []byte(body))
		})
	}
}

func FuzzRequestRefsParity(f *testing.F) {
	for _, body := range refsParityBodies {
		f.Add([]byte(body))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		requireRefsParity(t, body)
	})
}

// Every field of an agent route's body that names a channel, a thread or a
// dir is one the guard reads.
func TestRequestRefsCoverHandlerBodies(t *testing.T) {
	guard := stringFields(&requestRefs{})
	for _, dst := range agentRequestBodies() {
		for key := range stringFields(dst) {
			if strings.HasSuffix(key, "channel_id") || strings.HasSuffix(key, "thread_id") || strings.HasSuffix(key, "dir_path") {
				require.Contains(t, guard, key, "%T's %s isn't read by the guard", dst, key)
			}
		}
	}
}

// A key spelled two ways reaches the handler as the guard read it.
func (s *AgentScopeSuite) TestKeySpellings() {
	WithWorkflowBashLocal(true)(s.srv)
	tests := []struct {
		name   string
		method string
		target string
		body   string
		want   int
	}{
		{"other project's channel last", "POST", "/api/threads", `{"channel_id":"ch1","Channel_ID":"ch3"}`, 403},
		{"own channel last", "POST", "/api/threads", `{"Channel_ID":"ch3","channel_id":"ch1"}`, 200},
		{"upper case channel", "POST", "/api/threads", `{"CHANNEL_ID":"ch3"}`, 403},
		{"escaped key", "POST", "/api/threads", `{"channel_id":"ch3"}`, 403},
		{"duplicate key", "POST", "/api/threads", `{"channel_id":"ch1","channel_id":"ch3"}`, 403},
		{"null after another project's", "POST", "/api/threads", `{"channel_id":"ch3","channel_id":null}`, 403},
		{"upper case dir", "POST", "/api/memory/search", `{"dir_path":"/proj","DIR_PATH":"/etc"}`, 403},
		{"long s scope", "POST", "/api/bash-shortcuts", `{"scope":"project","ſcope":"global"}`, 403},
		{"kelvin sign workflow", "POST", "/api/tasks", `{"channel_id":"ch1","wor` + "K" + `flow_name":"w"}`, 403},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			rec := s.do("agent", tc.method, tc.target, strings.NewReader(tc.body))
			require.Equal(s.T(), tc.want, rec.Code, rec.Body.String())
			if tc.want == 200 {
				var req createThreadRequest
				require.NoError(s.T(), json.Unmarshal([]byte(s.seenBody), &req))
				require.Equal(s.T(), "ch1", req.ChannelID)
			}
		})
	}
}
