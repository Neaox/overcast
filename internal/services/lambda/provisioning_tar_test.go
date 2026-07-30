package lambda

// provisioning_tar_test.go — pins the single provisioning archive's shape:
// component entries land under their container paths, directories the base
// image owns are never emitted as headers (extraction must not reset their
// modes), and layer order is preserved so later layers overwrite earlier
// ones exactly as AWS merges them.

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"io"
	"testing"
)

func testZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func tarEntryNames(t *testing.T, data []byte) []string {
	t.Helper()
	var names []string
	tr := tar.NewReader(bytes.NewReader(data))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		names = append(names, hdr.Name)
	}
	return names
}

func TestZipToTarPrefixed_prefixesEntriesWithoutRootHeader(t *testing.T) {
	// Given: a deployment zip with a "./"-relative entry, as some zip tools emit.
	zipData := testZip(t, map[string]string{
		"index.js":      "exports.handler = 1",
		"./lib/util.js": "x",
	})

	// When: it is converted for extraction at the container root.
	tarData, err := zipToTarPrefixed(zipData, "var/task/")
	if err != nil {
		t.Fatalf("zipToTarPrefixed: %v", err)
	}

	// Then: entries carry the prefix, "./" is normalized away, and no header
	// names a directory the base image owns.
	names := tarEntryNames(t, tarData)
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		seen[n] = true
		switch n {
		case "var/", "var/task/", "opt/", "etc/":
			t.Fatalf("provisioning tar emits image-owned directory header %q — extraction would reset its mode", n)
		}
	}
	if !seen["var/task/index.js"] || !seen["var/task/lib/util.js"] {
		t.Fatalf("entries = %v, want var/task/index.js and var/task/lib/util.js", names)
	}
}

func TestAppendTarEntries_mergePreservesOrderForLayerOverwrites(t *testing.T) {
	// Given: two layers shipping the same path with different contents.
	first, err := zipToTarPrefixed(testZip(t, map[string]string{"shared.txt": "from-first"}), "opt/")
	if err != nil {
		t.Fatal(err)
	}
	second, err := zipToTarPrefixed(testZip(t, map[string]string{"shared.txt": "from-second"}), "opt/")
	if err != nil {
		t.Fatal(err)
	}

	// When: they are merged in layer order into one provisioning archive.
	var merged bytes.Buffer
	tw := tar.NewWriter(&merged)
	if err := appendTarEntries(tw, first); err != nil {
		t.Fatalf("append first: %v", err)
	}
	if err := appendTarEntries(tw, second); err != nil {
		t.Fatalf("append second: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	// Then: both entries survive with the later layer's content last — which
	// is what wins at extraction, matching AWS layer merge order (and the
	// previous per-layer sequential copies).
	var contents []string
	tr := tar.NewReader(bytes.NewReader(merged.Bytes()))
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Name != "opt/shared.txt" {
			continue
		}
		body, _ := io.ReadAll(tr)
		contents = append(contents, string(body))
	}
	if len(contents) != 2 || contents[0] != "from-first" || contents[1] != "from-second" {
		t.Fatalf("merged shared.txt contents = %v, want [from-first from-second] in that order", contents)
	}
}
