package cloudformation

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
)

// inlineCodeZip packages a CloudFormation template's inline function source
// into a real Lambda deployment archive.
//
// The two `Code.ZipFile` fields mean different things, and the difference is
// easy to miss because the names match. In a CloudFormation template,
// AWS::Lambda::Function `Code.ZipFile` is the function's *source text* and AWS
// packages it for you. In the Lambda API, `Code.ZipFile` is base64 of an actual
// zip archive. Base64-encoding the template's source satisfies the wire format
// while leaving the payload something no zip reader can open, so the function
// created cleanly and only failed later, at invoke time, with
// "build code tar: open zip: zip: not a valid zip file".
func inlineCodeZip(source, runtime, handler string) ([]byte, error) {
	name := inlineCodeFilename(runtime, handler)

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", name, err)
	}
	if _, err := w.Write([]byte(source)); err != nil {
		return nil, fmt.Errorf("write %s: %w", name, err)
	}
	if err := zw.Close(); err != nil {
		return nil, fmt.Errorf("finalise archive: %w", err)
	}
	return buf.Bytes(), nil
}

// inlineCodeFilename names the packaged file the way AWS does: after the
// handler's module part, with the runtime's extension. A handler of
// "index.handler" yields "index.py" on Python and "index.js" on Node, which is
// what the runtime will import.
//
// AWS only supports inline code for Node and Python. Other runtimes still get
// packaged rather than rejected — an archive the runtime cannot use is a better
// failure than one no zip reader can open, and it keeps this function total.
func inlineCodeFilename(runtime, handler string) string {
	module := "index"
	if parts := strings.SplitN(handler, ".", 2); parts[0] != "" {
		module = parts[0]
	}

	switch {
	case strings.HasPrefix(runtime, "python"):
		return module + ".py"
	case strings.HasPrefix(runtime, "nodejs"):
		return module + ".js"
	default:
		return module
	}
}
