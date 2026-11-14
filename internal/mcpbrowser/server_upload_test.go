package mcpbrowser

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/browser"
)

func (s *ServerSuite) TestUploadFile() {
	dir := s.T().TempDir()
	report := filepath.Join(dir, "report.pdf")
	notes := filepath.Join(dir, "notes.txt")
	require.NoError(s.T(), os.WriteFile(report, []byte("%PDF-1"), 0o644))
	require.NoError(s.T(), os.WriteFile(notes, []byte("hello"), 0o644))

	tests := []struct {
		name     string
		args     map[string]any
		maxBytes int64
		resp     actionResponse
		called   bool
		isError  bool
		text     string
		start    string
	}{
		{name: "uploads the files", args: map[string]any{"ref": 1, "paths": []string{report, notes}}, resp: actionResponse{Result: "ok"}, called: true, text: "Uploaded report.pdf, notes.txt to ref_1", start: "document.elementFromPoint(60.000000, 35.000000)"},
		{name: "uploads by selector", args: map[string]any{"selector": `input[name="cv"]`, "paths": []string{report}}, resp: actionResponse{Result: "ok"}, called: true, text: `Uploaded report.pdf to input[name="cv"]`, start: `document.querySelector("input[name=\"cv\"]")`},
		{name: "ref and selector", args: map[string]any{"ref": 1, "selector": "#f", "paths": []string{report}}, isError: true, text: "give ref or selector, not both"},
		{name: "neither ref nor selector", args: map[string]any{"paths": []string{report}}, isError: true, text: "ref or selector is required"},
		{name: "ref out of range", args: map[string]any{"ref": 2, "paths": []string{report}}, isError: true, text: "ref 2 out of range (1-1)"},
		{name: "no paths", args: map[string]any{"ref": 1, "paths": []string{}}, isError: true, text: "paths is required"},
		{name: "missing file", args: map[string]any{"ref": 1, "paths": []string{filepath.Join(dir, "gone.png")}}, isError: true, text: "reading " + filepath.Join(dir, "gone.png")},
		{name: "over the limit", args: map[string]any{"ref": 1, "paths": []string{report, notes}}, maxBytes: 8, isError: true, text: "files are over the 0 MB upload limit"},
		{name: "page refuses", args: map[string]any{"ref": 1, "paths": []string{report, notes}}, resp: actionResponse{Result: "that input takes one file"}, called: true, isError: true, text: "upload failed: that input takes one file", start: "document.elementFromPoint("},
		{name: "action error", args: map[string]any{"ref": 1, "paths": []string{report}}, resp: actionResponse{Error: "tab crashed"}, called: true, isError: true, text: "upload failed: tab crashed", start: "document.elementFromPoint("},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			var expr string
			called := false
			srv, session := setupTest(s.T(), func(w http.ResponseWriter, r *http.Request) {
				_, action, params := decodeActionRequest(s.T(), r)
				require.Equal(s.T(), "evaluate_js", action)
				expr, _ = params["expression"].(string)
				called = true
				writeJSON(w, tt.resp)
			})
			srv.refs = []browser.ElementRef{{RefID: "ref_1", Role: "button", X: 10, Y: 20, Width: 100, Height: 30}}
			if tt.maxBytes > 0 {
				srv.uploadMaxBytes = tt.maxBytes
			}
			res := callTool(s.T(), session, "upload_file", tt.args)
			require.Equal(s.T(), tt.isError, res.IsError)
			require.Contains(s.T(), getText(s.T(), res), tt.text)
			require.Equal(s.T(), tt.called, called)
			if tt.called {
				require.Contains(s.T(), expr, "try { el = "+tt.start)
			}
		})
	}
}

func (s *ServerSuite) TestUploadJSCarriesTheFiles() {
	files := []uploadFile{{Name: `a "b".png`, Type: "image/png", Data: base64.StdEncoding.EncodeToString([]byte{0, 1, 2})}}
	js := uploadJS("document.body", files)

	start := strings.Index(js, "const files = ") + len("const files = ")
	end := strings.Index(js[start:], ";\n")
	var got []uploadFile
	require.NoError(s.T(), json.Unmarshal([]byte(js[start:start+end]), &got))
	require.Equal(s.T(), files, got)
	require.Contains(s.T(), js, "try { el = document.body; }")
}

func (s *ServerSuite) TestReadUploadFilesTypesByExtension() {
	dir := s.T().TempDir()
	png := filepath.Join(dir, "shot.png")
	txt := filepath.Join(dir, "notes.txt")
	bare := filepath.Join(dir, "README")
	require.NoError(s.T(), os.WriteFile(txt, []byte("y"), 0o644))
	require.NoError(s.T(), os.WriteFile(png, []byte{0x89}, 0o644))
	require.NoError(s.T(), os.WriteFile(bare, []byte("x"), 0o644))

	srv := New("http://x", "ch", nil)
	files, err := srv.readUploadFiles([]string{png, txt, bare})
	require.NoError(s.T(), err)
	require.Equal(s.T(), []uploadFile{
		{Name: "shot.png", Type: "image/png", Data: "iQ=="},
		{Name: "notes.txt", Type: "text/plain", Data: "eQ=="},
		{Name: "README", Type: "", Data: "eA=="},
	}, files)
}
