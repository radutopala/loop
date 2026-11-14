package mcpbrowser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// defaultUploadMaxBytes caps what one upload_file call sends: the files go
// to the page base64-encoded inside a single script.
const defaultUploadMaxBytes = 20 << 20

// uploadFile is one file as the page script receives it.
type uploadFile struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Data string `json:"data"`
}

type uploadFileInput struct {
	Ref      refArg   `json:"ref,omitempty" jsonschema:"Element ref from read_page or find: the file input, or the button or label that opens it"`
	Selector string   `json:"selector,omitempty" jsonschema:"CSS selector of the file input, or of an element that holds it, for an input read_page doesn't list (inputs are often hidden behind a styled button). Use instead of ref."`
	Paths    []string `json:"paths" jsonschema:"Paths of the files to upload, read from this machine"`
}

func (s *Server) registerUploadTools() {
	mcp.AddTool(s.mcpServer, &mcp.Tool{
		Name:        "upload_file",
		InputSchema: inputSchemaFor[uploadFileInput](),
		Description: "Upload local files to a file input on the page, as if picked in its file chooser. Give ref (the input or the button or label that opens the chooser) or selector (for a hidden input); the page gets the usual input and change events. Clicking such a button instead opens a chooser that waits for a person at the browser pane.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input uploadFileInput) (*mcp.CallToolResult, any, error) {
		return s.handleUploadFile(ctx, input)
	})
}

func (s *Server) handleUploadFile(ctx context.Context, input uploadFileInput) (*mcp.CallToolResult, any, error) {
	ref := int(input.Ref)
	var start, target string
	switch {
	case input.Selector != "" && ref != 0:
		return errorResult("give ref or selector, not both"), nil, nil
	case input.Selector == "" && ref == 0:
		return errorResult("ref or selector is required"), nil, nil
	case input.Selector != "":
		sel, _ := json.Marshal(input.Selector) // a string: never fails
		start, target = fmt.Sprintf("document.querySelector(%s)", sel), input.Selector
	case ref < 1 || ref > len(s.refs):
		return errorResult(fmt.Sprintf("ref %d out of range (1-%d)", ref, len(s.refs))), nil, nil
	default:
		r := s.refs[ref-1]
		start, target = fmt.Sprintf("document.elementFromPoint(%f, %f)", r.X+r.Width/2, r.Y+r.Height/2), fmt.Sprintf("ref_%d", ref)
	}
	if len(input.Paths) == 0 {
		return errorResult("paths is required"), nil, nil
	}
	files, err := s.readUploadFiles(input.Paths)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	resp, err := s.callAction(ctx, "evaluate_js", map[string]any{"expression": uploadJS(start, files)})
	if err != nil {
		return errorResult(fmt.Sprintf("upload failed: %v", err)), nil, nil
	}
	if resp.Result != "ok" {
		return errorResult(fmt.Sprintf("upload failed: %s", resp.Result)), nil, nil
	}
	names := make([]string, len(files))
	for i, f := range files {
		names[i] = f.Name
	}
	return textResult(fmt.Sprintf("Uploaded %s to %s", strings.Join(names, ", "), target)), nil, nil
}

// readUploadFiles reads and encodes the files, refusing them once together
// they pass the server's cap.
func (s *Server) readUploadFiles(paths []string) ([]uploadFile, error) {
	files := make([]uploadFile, 0, len(paths))
	var total int64
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", p, err)
		}
		total += int64(len(data))
		if total > s.uploadMaxBytes {
			return nil, fmt.Errorf("files are over the %d MB upload limit", s.uploadMaxBytes>>20)
		}
		// A browser's File.type is the bare media type, without "; charset=".
		typ, _, _ := strings.Cut(mime.TypeByExtension(filepath.Ext(p)), ";")
		files = append(files, uploadFile{
			Name: filepath.Base(p),
			Type: typ,
			Data: base64.StdEncoding.EncodeToString(data),
		})
	}
	return files, nil
}

// uploadJSTemplate finds the file input from a start element (the element
// itself, the input a label points at, or the nearest one inside an
// ancestor), sets its files and fires the events a real pick fires. It
// answers "ok" or why not.
const uploadJSTemplate = `(() => {
  const files = %s;
  let el;
  try { el = %s; } catch (e) { return "bad selector: " + e.message; }
  const isFile = e => e instanceof HTMLInputElement && e.type === "file";
  let input = null;
  if (isFile(el)) input = el;
  else if (el instanceof HTMLLabelElement && isFile(el.control)) input = el.control;
  for (let a = el; !input && a; a = a.parentElement) input = a.querySelector('input[type=file]');
  if (!input) return "no file input there";
  if (files.length > 1 && !input.multiple) return "that input takes one file";
  const dt = new DataTransfer();
  for (const f of files) {
    const bin = atob(f.data);
    const buf = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) buf[i] = bin.charCodeAt(i);
    dt.items.add(new File([buf], f.name, { type: f.type }));
  }
  input.files = dt.files;
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
  return "ok";
})()`

// uploadJS is the upload script for files, starting the search for the
// input at the element the start expression yields.
func uploadJS(start string, files []uploadFile) string {
	data, _ := json.Marshal(files) // plain strings: never fails
	return fmt.Sprintf(uploadJSTemplate, data, start)
}
