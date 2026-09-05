//go:build dev

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Deterministic output.
//
// Every generated file is rendered the same way: two-space indentation, map
// keys sorted (encoding/json does that), struct fields in declaration order,
// no HTML escaping (so a pattern like ^[a-z<]+$ survives unmangled), LF line
// endings and one trailing newline. Regenerating twice produces identical
// bytes, which is what `-check` and the sync test rely on.

func encodeDocument(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

// outputSet is every file a generation run produces, keyed by path relative
// to the repository root.
type outputSet map[string][]byte

// write puts every file in place, creating directories as needed.
func (o outputSet) write(root string) error {
	for rel, contents := range o {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
	}
	return nil
}

// check proves the committed files are byte-for-byte what was just generated,
// writing nothing. Every stale file is named, not just the first.
func (o outputSet) check(root string) error {
	var stale []string
	for rel, contents := range o {
		path := filepath.Join(root, filepath.FromSlash(rel))
		current, err := os.ReadFile(path)
		if err != nil {
			stale = append(stale, fmt.Sprintf("%s: %v", rel, err))
			continue
		}
		if !bytes.Equal(current, contents) {
			stale = append(stale, rel+": differs from the generator's output")
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("generated compat model is stale; run `make generate-compat-model`:\n  %s", joinSorted(stale, "\n  "))
	}
	return nil
}
