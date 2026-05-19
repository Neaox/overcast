package lambda

// handler_source.go — emulator-only source code storage for the web UI editor.
//
// These endpoints are NOT part of the AWS Lambda API. They exist solely to
// support the built-in code editor in the Overcast web UI.
//
//   GET /2015-03-31/functions/{name}/source
//       Returns {"source": "...", "filename": "...", "language": "..."}
//
//   PUT /2015-03-31/functions/{name}/source
//       Body: {"source": "...", "filename": "..."}
//       Stores the source text, packages it into an in-memory zip, updates
//       CodeZip/CodeSize, generates a new RevisionId.

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
)

// sourceRequest is the body for PUT .../source.
type sourceRequest struct {
	Source   string `json:"source"`
	Filename string `json:"filename"`
}

// sourceResponse is the body for GET .../source.
type sourceResponse struct {
	Source   string       `json:"source"`
	Filename string       `json:"filename"`
	Language string       `json:"language"`
	Files    []sourceFile `json:"files"` // all files in the deployment zip
}

// sourceFile describes a single file inside the deployment zip.
type sourceFile struct {
	Name string `json:"name"` // path inside the zip (e.g. "src/index.js")
	Size int64  `json:"size"` // uncompressed size in bytes
}

// defaultSourceForRuntime returns a minimal handler stub for a given runtime.
func defaultSourceForRuntime(runtime, handler string) (source, filename string) {
	switch {
	case strings.HasPrefix(runtime, "nodejs"):
		filename = handlerFilename(handler, ".js")
		source = `exports.handler = async (event, context) => {
  console.log("Event:", JSON.stringify(event, null, 2));
  return {
    statusCode: 200,
    body: JSON.stringify({ message: "Hello from Lambda!" }),
  };
};
`
	case strings.HasPrefix(runtime, "python"):
		filename = handlerFilename(handler, ".py")
		source = `import json

def handler(event, context):
    print("Event:", json.dumps(event, indent=2))
    return {
        "statusCode": 200,
        "body": json.dumps({"message": "Hello from Lambda!"}),
    }
`
	case strings.HasPrefix(runtime, "java"):
		filename = "Handler.java"
		source = `import com.amazonaws.services.lambda.runtime.Context;
import com.amazonaws.services.lambda.runtime.RequestHandler;

public class Handler implements RequestHandler<Object, String> {
    @Override
    public String handleRequest(Object event, Context context) {
        return "Hello from Lambda!";
    }
}
`
	case strings.HasPrefix(runtime, "dotnet"):
		filename = "Function.cs"
		source = `using Amazon.Lambda.Core;

[assembly: LambdaSerializer(typeof(Amazon.Lambda.Serialization.SystemTextJson.DefaultLambdaJsonSerializer))]

public class Function {
    public string FunctionHandler(object input, ILambdaContext context) {
        return "Hello from Lambda!";
    }
}
`
	default:
		filename = "handler.sh"
		source = `#!/bin/bash
echo '{"statusCode": 200, "body": "Hello from Lambda!"}'
`
	}
	return source, filename
}

// handlerFilename derives a filename from the Lambda handler string.
// E.g. "index.handler" → "index.js" (for Node) or "index.py" (for Python).
func handlerFilename(handler, ext string) string {
	parts := strings.SplitN(handler, ".", 2)
	if len(parts) > 0 && parts[0] != "" {
		return parts[0] + ext
	}
	return "index" + ext
}

// runtimeLanguage maps a Lambda runtime to a Monaco editor language ID.
func runtimeLanguage(runtime string) string {
	switch {
	case strings.HasPrefix(runtime, "nodejs"):
		return "javascript"
	case strings.HasPrefix(runtime, "python"):
		return "python"
	case strings.HasPrefix(runtime, "java"):
		return "java"
	case strings.HasPrefix(runtime, "dotnet"):
		return "csharp"
	default:
		return "shell"
	}
}

// languageForFilename returns a Monaco editor language ID based on file extension,
// falling back to the runtime-based language when the extension is unrecognised.
func languageForFilename(filename, runtime string) string {
	switch {
	case strings.HasSuffix(filename, ".js"), strings.HasSuffix(filename, ".mjs"), strings.HasSuffix(filename, ".cjs"):
		return "javascript"
	case strings.HasSuffix(filename, ".ts"), strings.HasSuffix(filename, ".mts"):
		return "typescript"
	case strings.HasSuffix(filename, ".py"):
		return "python"
	case strings.HasSuffix(filename, ".java"):
		return "java"
	case strings.HasSuffix(filename, ".cs"):
		return "csharp"
	case strings.HasSuffix(filename, ".json"):
		return "json"
	case strings.HasSuffix(filename, ".yaml"), strings.HasSuffix(filename, ".yml"):
		return "yaml"
	case strings.HasSuffix(filename, ".md"):
		return "markdown"
	case strings.HasSuffix(filename, ".sh"), strings.HasSuffix(filename, ".bash"):
		return "shell"
	case strings.HasSuffix(filename, ".xml"):
		return "xml"
	case strings.HasSuffix(filename, ".html"):
		return "html"
	case strings.HasSuffix(filename, ".css"):
		return "css"
	default:
		return runtimeLanguage(runtime)
	}
}

// packSourceAsZip wraps source text into a minimal zip archive in memory.
func packSourceAsZip(filename, source string) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(filename)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write([]byte(source)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// patchZipFile replaces (or adds) a single file inside an existing zip archive.
// All other files are preserved as-is.
func patchZipFile(existingZip []byte, filename, content string) ([]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(existingZip), int64(len(existingZip)))
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// Copy all existing files except the one being replaced.
	for _, f := range zr.File {
		if f.Name == filename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		fw, err := zw.Create(f.Name)
		if err != nil {
			rc.Close()
			return nil, err
		}
		if _, err := io.Copy(fw, rc); err != nil {
			rc.Close()
			return nil, err
		}
		rc.Close()
	}

	// Write the new/updated file.
	fw, err := zw.Create(filename)
	if err != nil {
		return nil, err
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		return nil, err
	}

	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// listZipFiles returns metadata for every file in a zip archive.
func listZipFiles(zipBytes []byte) []sourceFile {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil
	}
	files := make([]sourceFile, 0, len(zr.File))
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		files = append(files, sourceFile{
			Name: f.Name,
			Size: int64(f.UncompressedSize64),
		})
	}
	return files
}

// readZipFile extracts a single file's content from a zip archive.
func readZipFile(zipBytes []byte, name string) (string, bool) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return "", false
	}
	for _, f := range zr.File {
		if f.Name == name {
			rc, err := f.Open()
			if err != nil {
				return "", false
			}
			defer rc.Close()
			var buf bytes.Buffer
			if _, err := buf.ReadFrom(rc); err != nil {
				return "", false
			}
			return buf.String(), true
		}
	}
	return "", false
}

// guessEntryFile picks the best file to show by default based on the handler
// string and runtime. E.g. handler "index.handler" with runtime "nodejs22.x"
// tries "index.js", "index.mjs", "index.cjs" then falls back to the first
// text-looking file.
func guessEntryFile(files []sourceFile, handler, runtime string) string {
	if len(files) == 0 {
		return ""
	}
	module := strings.SplitN(handler, ".", 2)[0]
	var exts []string
	switch {
	case strings.HasPrefix(runtime, "nodejs"):
		exts = []string{".js", ".mjs", ".cjs", ".ts"}
	case strings.HasPrefix(runtime, "python"):
		exts = []string{".py"}
	case strings.HasPrefix(runtime, "java"):
		exts = []string{".java"}
	case strings.HasPrefix(runtime, "dotnet"):
		exts = []string{".cs"}
	default:
		exts = []string{".sh"}
	}
	// Try module + ext (e.g. "index.js").
	for _, ext := range exts {
		candidate := module + ext
		for _, f := range files {
			if f.Name == candidate {
				return f.Name
			}
		}
	}
	// Fallback: first file with a known extension.
	for _, f := range files {
		for _, ext := range exts {
			if strings.HasSuffix(f.Name, ext) {
				return f.Name
			}
		}
	}
	// Last resort: first file.
	return files[0].Name
}

// GetFunctionSource handles GET /2015-03-31/functions/{name}/source.
// Returns the stored plain-text source (or a default stub if none stored yet).
//
// Query parameters:
//
//	?file=path — return content of a specific file inside the deployment zip.
func (h *Handler) GetFunctionSource(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	fn, aerr := h.ls.getFunction(r.Context(), name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	// Build the file list from the deployment zip (if any).
	var files []sourceFile
	if len(fn.CodeZip) > 0 {
		files = listZipFiles(fn.CodeZip)
	}

	// If a specific file was requested, read it from the zip.
	if reqFile := r.URL.Query().Get("file"); reqFile != "" {
		if len(fn.CodeZip) == 0 {
			protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("no deployment package"))
			return
		}
		content, ok := readZipFile(fn.CodeZip, reqFile)
		if !ok {
			protocol.WriteJSONError(w, r, &protocol.AWSError{
				Code:       "ResourceNotFoundException",
				Message:    "File not found in deployment package: " + reqFile,
				HTTPStatus: http.StatusNotFound,
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(sourceResponse{
			Source:   content,
			Filename: reqFile,
			Language: languageForFilename(reqFile, fn.Runtime),
			Files:    files,
		})
		return
	}

	source := fn.SourceCode
	filename := fn.SourceFilename

	// When SourceCode is empty (function created via CLI/SDK), try to
	// extract the handler file from the deployment zip.
	if source == "" && len(fn.CodeZip) > 0 {
		entry := guessEntryFile(files, fn.Handler, fn.Runtime)
		if entry != "" {
			if content, ok := readZipFile(fn.CodeZip, entry); ok {
				source = content
				filename = entry
			}
		}
	}

	// Final fallback: show a default stub.
	if source == "" {
		source, filename = defaultSourceForRuntime(fn.Runtime, fn.Handler)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(sourceResponse{
		Source:   source,
		Filename: filename,
		Language: runtimeLanguage(fn.Runtime),
		Files:    files,
	})
}

// PutFunctionSource handles PUT /2015-03-31/functions/{name}/source.
// Stores the source text, packs it into a zip, and updates the function.
func (h *Handler) PutFunctionSource(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ctx := r.Context()

	fn, aerr := h.ls.getFunction(ctx, name)
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	if fn == nil {
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Function not found: " + name,
			HTTPStatus: http.StatusNotFound,
		})
		return
	}

	var req sourceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		protocol.WriteJSONError(w, r, protocol.ErrInvalidArgument("invalid request body"))
		return
	}
	if req.Source == "" {
		protocol.WriteJSONError(w, r, protocol.ErrMissingParameter("source"))
		return
	}
	if req.Filename == "" {
		req.Filename = fn.SourceFilename
		if req.Filename == "" {
			_, req.Filename = defaultSourceForRuntime(fn.Runtime, fn.Handler)
		}
	}

	zipBytes, err := func() ([]byte, error) {
		// If the function already has a multi-file zip, patch the single file
		// instead of replacing the entire archive.
		if len(fn.CodeZip) > 0 {
			existing := listZipFiles(fn.CodeZip)
			if len(existing) > 1 {
				return patchZipFile(fn.CodeZip, req.Filename, req.Source)
			}
		}
		return packSourceAsZip(req.Filename, req.Source)
	}()
	if err != nil {
		h.log.Error("put function source: zip packaging failed",
			zap.String("function", name), zap.Error(err))
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	fn.SourceCode = req.Source
	fn.SourceFilename = req.Filename
	fn.CodeZip = zipBytes
	fn.CodeSize = int64(len(zipBytes))
	fn.RevisionId = uuid.NewString()
	fn.LastModified = h.clk.Now().UTC().Format(time.RFC3339)

	if aerr := h.ls.putFunction(ctx, fn); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}

	if h.bus != nil {
		h.bus.Publish(ctx, events.Event{
			Type:    events.LambdaFunctionUpdated,
			Time:    h.clk.Now(),
			Source:  "lambda",
			Payload: events.LambdaFunctionPayload{Name: fn.Name, ARN: fn.ARN},
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(sourceResponse{
		Source:   fn.SourceCode,
		Filename: fn.SourceFilename,
		Language: runtimeLanguage(fn.Runtime),
		Files:    listZipFiles(fn.CodeZip),
	})
}
