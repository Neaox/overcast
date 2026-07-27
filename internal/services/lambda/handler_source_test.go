package lambda

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"
)

func zipWith(t *testing.T, name, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create(name)
	if err != nil {
		t.Fatalf("create zip entry: %v", err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		t.Fatalf("write zip entry: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

func TestResolveSourcePrefersRealCode(t *testing.T) {
	t.Run("stored plain-text source wins", func(t *testing.T) {
		fn := &Function{SourceCode: "print('mine')", SourceFilename: "index.py", Runtime: "python3.13"}
		source, filename, placeholder := resolveSource(fn, nil)
		if source != "print('mine')" || filename != "index.py" {
			t.Errorf("got (%q, %q), want the stored source", source, filename)
		}
		if placeholder {
			t.Error("stored source reported as a placeholder")
		}
	})

	t.Run("handler file is read out of the deployment zip", func(t *testing.T) {
		code := zipWith(t, "index.py", "print('packaged')")
		fn := &Function{CodeZip: code, Handler: "index.handler", Runtime: "python3.13"}
		source, filename, placeholder := resolveSource(fn, listZipFiles(code))
		if source != "print('packaged')" || filename != "index.py" {
			t.Errorf("got (%q, %q), want the packaged source", source, filename)
		}
		if placeholder {
			t.Error("packaged source reported as a placeholder")
		}
	})
}

// A function deployed by CloudFormation before inline code was packaged
// correctly holds raw source where an archive was expected. Showing an invented
// "Hello from Lambda!" stub for it made a broken deployment look healthy — the
// console displayed code the function did not have, which is worse than
// admitting the package is unreadable.
func TestResolveSourceShowsUnpackagedCodeRatherThanInventingIt(t *testing.T) {
	const real = "import boto3\n\ndef handler(event, context):\n    return 1\n"
	fn := &Function{CodeZip: []byte(real), Handler: "index.handler", Runtime: "python3.13"}

	source, filename, placeholder := resolveSource(fn, nil)

	if strings.Contains(source, "Hello from Lambda!") {
		t.Fatalf("fabricated a stub for a function that has real code:\n%s", source)
	}
	if source != real {
		t.Errorf("source = %q, want the stored code %q", source, real)
	}
	if filename != "index.py" {
		t.Errorf("filename = %q, want %q", filename, "index.py")
	}
	if placeholder {
		t.Error("real code reported as a placeholder")
	}
}

func TestResolveSourceFlagsTheStubAsAPlaceholder(t *testing.T) {
	fn := &Function{Runtime: "python3.13", Handler: "index.handler"}

	source, _, placeholder := resolveSource(fn, nil)

	if !placeholder {
		t.Error("stub not reported as a placeholder, so the console cannot label it")
	}
	if !strings.Contains(source, "Hello from Lambda!") {
		t.Errorf("expected the example stub when there is no code at all, got %q", source)
	}
}

func TestExplainUnreadablePackage(t *testing.T) {
	base := errors.New("open zip: zip: not a valid zip file")

	t.Run("names the cause and the cure for unpackaged source", func(t *testing.T) {
		fn := &Function{Name: "Stack-Function", CodeZip: []byte("import json\n")}

		got := explainUnreadablePackage(fn, base).Error()

		for _, want := range []string{"Stack-Function", "source text", "Redeploy the stack"} {
			if !strings.Contains(got, want) {
				t.Errorf("error %q does not mention %q", got, want)
			}
		}
		if !errors.Is(explainUnreadablePackage(fn, base), base) {
			t.Error("wrapped error no longer unwraps to the original")
		}
	})

	t.Run("leaves genuinely corrupt archives alone", func(t *testing.T) {
		fn := &Function{Name: "Stack-Function", CodeZip: []byte{0x50, 0x4b, 0x00, 0xff, 0xfe}}

		if got := explainUnreadablePackage(fn, base); got.Error() != base.Error() {
			t.Errorf("added a misleading explanation to a corrupt archive: %v", got)
		}
	})
}

func TestResolveSourceKeepsBinaryPackagesOutOfTheEditor(t *testing.T) {
	// An unreadable *binary* package is not source text — showing its bytes
	// would be noise, so the stub is the honest answer as long as it is labelled.
	fn := &Function{CodeZip: []byte{0x00, 0x01, 0x02, 0xff, 0xfe}, Runtime: "python3.13", Handler: "index.handler"}

	_, _, placeholder := resolveSource(fn, nil)

	if !placeholder {
		t.Error("binary package should fall back to a labelled placeholder")
	}
}
