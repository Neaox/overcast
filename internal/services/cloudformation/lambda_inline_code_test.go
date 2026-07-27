package cloudformation

import (
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

// A CloudFormation template's Code.ZipFile is source text, but the Lambda API's
// Code.ZipFile is base64 of a real archive. Handing the raw source over as if it
// were an archive produced functions that could not be unpacked at invoke time:
// "build code tar: open zip: zip: not a valid zip file".
func TestInlineCodeZipProducesAReadableArchive(t *testing.T) {
	const source = "import json\n\ndef handler(event, context):\n    return {\"ok\": True}\n"

	got, err := inlineCodeZip(source, "python3.13", "index.handler")
	if err != nil {
		t.Fatalf("inlineCodeZip: %v", err)
	}

	zr, err := zip.NewReader(bytes.NewReader(got), int64(len(got)))
	if err != nil {
		t.Fatalf("result is not a readable zip: %v", err)
	}
	if len(zr.File) != 1 {
		t.Fatalf("got %d files in the archive, want 1", len(zr.File))
	}
	if zr.File[0].Name != "index.py" {
		t.Errorf("archived file name = %q, want %q", zr.File[0].Name, "index.py")
	}

	rc, err := zr.File[0].Open()
	if err != nil {
		t.Fatalf("open archived file: %v", err)
	}
	defer rc.Close()
	content, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read archived file: %v", err)
	}
	if string(content) != source {
		t.Errorf("archived content = %q, want %q", content, source)
	}
}

func TestInlineCodeFilename(t *testing.T) {
	tests := []struct {
		name    string
		runtime string
		handler string
		want    string
	}{
		{"python module from handler", "python3.13", "index.handler", "index.py"},
		{"node module from handler", "nodejs22.x", "index.handler", "index.js"},
		{"non-default module name", "python3.12", "app.main", "app.py"},
		{"handler may carry a path", "nodejs20.x", "src/entry.handler", "src/entry.js"},
		{"missing handler falls back to index", "python3.13", "", "index.py"},
		{"unknown runtime still packages", "provided.al2023", "bootstrap.handler", "bootstrap"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := inlineCodeFilename(tc.runtime, tc.handler); got != tc.want {
				t.Errorf("inlineCodeFilename(%q, %q) = %q, want %q", tc.runtime, tc.handler, got, tc.want)
			}
		})
	}
}
