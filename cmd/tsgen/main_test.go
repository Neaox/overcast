package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRender_committedOutputIsCurrent is the same gate as `tsgen --check`,
// run by `go test ./...` so a Go struct edit that forgets `make generate-ts`
// fails the pre-push test run as well as CI's docs-check job.
func TestRender_committedOutputIsCurrent(t *testing.T) {
	// Given: the workspace this package is checked out in.
	root, err := findWorkspaceRoot(".")
	if err != nil {
		t.Fatalf("findWorkspaceRoot: %v", err)
	}
	committed, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(outputPath)))
	if err != nil {
		t.Fatalf("read %s: %v (run: make generate-ts)", outputPath, err)
	}

	// When: the manifest is rendered from the current Go source.
	rendered, err := render(root, manifest)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Then: it is byte-identical to the checked-in file.
	if string(rendered) != string(committed) {
		t.Errorf("%s is stale (%s); run: make generate-ts", outputPath, firstDifference(committed, rendered))
	}
}

// writeFixtureModule lays down a two-package module exercising every rendering
// rule, and returns its root.
func writeFixtureModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.24\n",
		"a/a.go": `// Package a holds fixture types.
package a

import (
	"encoding/json"
	"time"

	"example.com/fixture/b"
)

// Shape exercises every rendering rule.
type Shape struct {
	// Name is the display name.
	Name     string            ` + "`json:\"name\"`" + `
	Count    int64             ` + "`json:\"count,omitempty\"`" + `
	Ratio    float32           ` + "`json:\"ratio\"`" + ` // trailing note
	When     time.Time         ` + "`json:\"when\"`" + `
	Maybe    *b.Leaf           ` + "`json:\"maybe,omitempty\"`" + `
	Always   *b.Leaf           ` + "`json:\"always\"`" + `
	Leaves   []b.Leaf          ` + "`json:\"leaves\"`" + `
	Pointers []*b.Leaf         ` + "`json:\"pointers,omitempty\"`" + `
	Raw      json.RawMessage   ` + "`json:\"raw\"`" + `
	Blob     []byte            ` + "`json:\"blob\"`" + `
	Labels   map[string]string ` + "`json:\"labels,omitempty\"`" + `
	Kind     Kind              ` + "`json:\"kind\"`" + `
	ByKind   map[Kind]int      ` + "`json:\"byKind\"`" + `
	Anything any               ` + "`json:\"anything\"`" + `
	Empty    interface{}       ` + "`json:\"empty\"`" + `
	Skipped  string            ` + "`json:\"-\"`" + `
	Untagged bool
	AsString int ` + "`json:\"asString,string\"`" + `
	Zero     int ` + "`json:\"zero,omitzero\"`" + `
	Dashed   string ` + "`json:\"content-type\"`" + `
	hidden   string
}

// Kind is a small enum.
type Kind = string

const (
	KindA     Kind = "a"
	KindB     Kind = "b"
	KindAlsoB Kind = "b" // a second name for one wire value
)

// Plain has no typed constants.
type Plain string

// Long has members that overflow one line.
type Long = string

const (
	Long1 Long = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	Long2 Long = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	Long3 Long = "ccccccccccccccccccccccccccccccccccccccccccc"
)

// Empty has nothing to render.
type Empty struct{}

// Embeds is rejected: encoding/json flattens embedded fields.
type Embeds struct {
	Empty
	Extra string ` + "`json:\"extra\"`" + `
}
`,
		"b/b.go": `package b

type Leaf struct {
	ID string ` + "`json:\"id\"`" + `
}
`,
	}
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRender_appliesTheRenderingRules(t *testing.T) {
	// Given: a fixture module and a manifest naming its types in a chosen order.
	root := writeFixtureModule(t)
	targets := []target{
		{"a", "Shape", "Shape"},
		{"a", "Kind", "Kind"},
		{"a", "Plain", "Plain"},
		{"a", "Long", "Long"},
		{"a", "Empty", "Empty"},
		{"b", "Leaf", "Leaf"},
	}

	// When: it is rendered.
	got, err := render(root, targets)
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	// Then: json names, optionality, nullability, externals, unions, doc
	// comments and quoting all come out as documented in the package comment.
	want := header + `
/**
 * Shape exercises every rendering rule.
 *
 * Generated from Go ` + "`a.Shape`" + ` (a/a.go).
 */
export interface Shape {
  /** Name is the display name. */
  name: string
  count?: number
  /** trailing note */
  ratio: number
  when: string
  maybe?: Leaf
  always: Leaf | null
  leaves: Leaf[]
  pointers?: (Leaf | null)[]
  raw: unknown
  blob: string
  labels?: Record<string, string>
  kind: Kind
  byKind: Record<string, number>
  anything: unknown
  empty: unknown
  Untagged: boolean
  asString: string
  zero?: number
  "content-type": string
}

/**
 * Kind is a small enum.
 *
 * Generated from Go ` + "`a.Kind`" + ` (a/a.go).
 */
export type Kind = "a" | "b"

/**
 * Plain has no typed constants.
 *
 * Generated from Go ` + "`a.Plain`" + ` (a/a.go).
 */
export type Plain = string

/**
 * Long has members that overflow one line.
 *
 * Generated from Go ` + "`a.Long`" + ` (a/a.go).
 */
export type Long =
  | "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
  | "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  | "ccccccccccccccccccccccccccccccccccccccccccc"

/**
 * Empty has nothing to render.
 *
 * Generated from Go ` + "`a.Empty`" + ` (a/a.go).
 */
export interface Empty {}

/** Generated from Go ` + "`b.Leaf`" + ` (b/b.go). */
export interface Leaf {
  id: string
}
`
	if string(got) != want {
		t.Errorf("rendered output differs from expected\n--- got ---\n%s\n--- want ---\n%s\n(%s)", got, want, firstDifference([]byte(want), got))
	}
}

func TestRender_refusesTypesOutsideTheManifest(t *testing.T) {
	// Given: a manifest that lists Shape but not the Leaf and Kind it refers to.
	root := writeFixtureModule(t)

	// When: it is rendered.
	_, err := render(root, []target{{"a", "Shape", "Shape"}})

	// Then: the first unlisted reference is refused, naming the field and the
	// fix — so the generated set grows only by an explicit manifest edit.
	if err == nil {
		t.Fatal("render() succeeded; want an error for the unlisted b.Leaf reference")
	}
	for _, want := range []string{"a.Shape.Maybe", "b.Leaf", "manifest"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestRender_refusesEmbeddedFields(t *testing.T) {
	root := writeFixtureModule(t)

	_, err := render(root, []target{{"a", "Embeds", "Embeds"}, {"a", "Empty", "Empty"}})

	if err == nil || !strings.Contains(err.Error(), "embedded field Empty") {
		t.Errorf("render() error = %v; want a refusal naming the embedded field", err)
	}
}

func TestRender_namesAMissingType(t *testing.T) {
	// Given: a manifest entry for a type that was renamed or removed.
	root := writeFixtureModule(t)

	// When: it is rendered.
	_, err := render(root, []target{{"a", "Gone", "Gone"}})

	// Then: the error says which entry is stale, rather than a nil dereference.
	if err == nil || !strings.Contains(err.Error(), "no type named Gone") {
		t.Errorf("render() error = %v; want 'no type named Gone'", err)
	}
}

func TestRender_rejectsDuplicateManifestEntries(t *testing.T) {
	root := writeFixtureModule(t)

	_, err := render(root, []target{{"a", "Empty", "Empty"}, {"a", "Plain", "Empty"}})

	if err == nil || !strings.Contains(err.Error(), "two types as Empty") {
		t.Errorf("render() error = %v; want a duplicate-name refusal", err)
	}
}

func TestTsPropertyName(t *testing.T) {
	cases := map[string]string{
		"name":         "name",
		"start_ms":     "start_ms",
		"$ref":         "$ref",
		"content-type": `"content-type"`,
		"1st":          `"1st"`,
		"":             `""`,
	}
	for in, want := range cases {
		if got := tsPropertyName(in); got != want {
			t.Errorf("tsPropertyName(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestFirstDifference_reportsTheLine(t *testing.T) {
	got := firstDifference([]byte("a\nb\nc\n"), []byte("a\nB\nc\n"))
	if !strings.Contains(got, "line 2") || !strings.Contains(got, `"b"`) || !strings.Contains(got, `"B"`) {
		t.Errorf("firstDifference = %q; want line 2 with both versions", got)
	}
}
