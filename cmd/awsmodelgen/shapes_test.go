package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// shapeFixture is a small official-style Smithy AST covering everything the
// pruner has to decide: a resource with lifecycle bindings, an out-of-scope
// sibling service in the same corpus, documentation and example traits that
// must be dropped, an unreachable shape, and a reference into another namespace.
const shapeFixture = `{
  "smithy":"2.0",
  "metadata":{"suppressions":[{"id":"ignored"}]},
  "shapes":{
    "example.widget#WidgetService":{
      "type":"service","version":"2026-01-01",
      "resources":[{"target":"example.widget#Widget"}],
      "operations":[{"target":"example.widget#ListWidgets"}],
      "traits":{
        "aws.api#service":{"sdkId":"Widget Service"},
        "aws.auth#sigv4":{"name":"widget"},
        "aws.protocols#restJson1":{},
        "smithy.api#documentation":"<p>Dropped.</p>",
        "smithy.api#title":"Widget Service",
        "smithy.rules#endpointRuleSet":{"version":"1.0"}
      }
    },
    "example.widget#Widget":{
      "type":"resource",
      "identifiers":{"id":{"target":"example.widget#WidgetId"}},
      "create":{"target":"example.widget#CreateWidget"},
      "read":{"target":"example.widget#GetWidget"}
    },
    "example.widget#CreateWidget":{
      "type":"operation",
      "input":{"target":"example.widget#CreateWidgetRequest"},
      "output":{"target":"example.widget#CreateWidgetResponse"},
      "errors":[{"target":"example.widget#WidgetExists"}],
      "traits":{"smithy.api#http":{"method":"POST","uri":"/widgets","code":201},"smithy.api#examples":[{"title":"dropped"}]}
    },
    "example.widget#GetWidget":{"type":"operation","output":{"target":"example.widget#CreateWidgetResponse"}},
    "example.widget#ListWidgets":{"type":"operation","traits":{"smithy.api#paginated":{"inputToken":"NextToken"}}},
    "example.widget#CreateWidgetRequest":{
      "type":"structure",
      "members":{
        "Name":{"target":"smithy.api#String","traits":{"smithy.api#required":{},"smithy.api#documentation":"dropped","smithy.api#length":{"min":1,"max":64}}},
        "Kind":{"target":"example.widget#WidgetKind"},
        "Owner":{"target":"example.other#Principal"}
      },
      "traits":{"smithy.api#input":{}}
    },
    "example.widget#CreateWidgetResponse":{"type":"structure","members":{"Id":{"target":"example.widget#WidgetId"}},"traits":{"smithy.api#output":{}}},
    "example.widget#WidgetExists":{"type":"structure","traits":{"smithy.api#error":"client","smithy.api#httpError":409}},
    "example.widget#WidgetId":{"type":"string"},
    "example.widget#WidgetKind":{"type":"enum","members":{"BASIC":{"target":"smithy.api#Unit","traits":{"smithy.api#enumValue":"basic"}}}},
    "example.other#Principal":{"type":"string"},
    "example.widget#Unreachable":{"type":"structure","members":{"Gone":{"target":"smithy.api#String"}}}
  }
}`

const otherServiceFixture = `{
  "smithy":"2.0",
  "shapes":{
    "example.gadget#GadgetService":{"type":"service","version":"2026-02-02","operations":[{"target":"example.gadget#GetGadget"}],"traits":{"aws.api#service":{"sdkId":"Gadget"},"aws.protocols#awsJson1_1":{}}},
    "example.gadget#GetGadget":{"type":"operation"}
  }
}`

func shapeFixtureDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeModel(t, filepath.Join(dir, "widget.json"), shapeFixture)
	writeModel(t, filepath.Join(dir, "gadget.json"), otherServiceFixture)
	return dir
}

func TestBuildShapeSnapshots_keepsReachableShapesAndAllowlistedTraits(t *testing.T) {
	// Given: a corpus with one in-scope service and one out-of-scope sibling.
	modelsDir := shapeFixtureDir(t)

	// When: the in-scope service is pruned.
	rendered, err := buildShapeSnapshots(modelsDir, []string{"widget-service"})
	if err != nil {
		t.Fatalf("build shape snapshots: %v", err)
	}

	// Then: only the in-scope service is emitted.
	if len(rendered) != 1 {
		t.Fatalf("expected one snapshot, got %d", len(rendered))
	}
	document := string(rendered["widget-service"])

	// ... reachable shapes are present, including through the resource lifecycle
	// and across a namespace boundary.
	for _, want := range []string{
		`"WidgetService":`, `"CreateWidget":`, `"GetWidget":`, `"ListWidgets":`,
		`"CreateWidgetRequest":`, `"WidgetExists":`, `"WidgetKind":`,
		`"example.other#Principal":`,
	} {
		if !strings.Contains(document, want) {
			t.Errorf("snapshot is missing %s:\n%s", want, document)
		}
	}

	// ... and unreachable shapes, prelude targets, and non-allowlisted traits are
	// gone.
	for _, unwanted := range []string{
		"Unreachable", "smithy.api#documentation", "smithy.api#examples",
		"smithy.api#title", "smithy.rules#endpointRuleSet", "suppressions",
		`"smithy.api#String":`,
	} {
		if strings.Contains(document, unwanted) {
			t.Errorf("snapshot still carries %s:\n%s", unwanted, document)
		}
	}

	// ... the header records the identity a consumer needs, and shape references
	// inside the service's own namespace are relative.
	for _, want := range []string{
		`"service": "widget-service"`,
		`"namespace": "example.widget"`,
		`"serviceShape": "WidgetService"`,
		`"sdkId": "Widget Service"`,
		`"apiVersion": "2026-01-01"`,
		`"protocols": ["RESTJSON"]`,
		`"create":"CreateWidget"`,
		`"identifiers":{"id":"WidgetId"}`,
	} {
		if !strings.Contains(document, want) {
			t.Errorf("snapshot is missing %s:\n%s", want, document)
		}
	}
	if strings.Contains(document, "example.widget#") {
		t.Errorf("snapshot did not relativise own-namespace references:\n%s", document)
	}
}

func TestBuildShapeSnapshots_isByteDeterministic(t *testing.T) {
	// Given: a corpus pruned twice. Map iteration order differs between runs, so
	// any leak of it into the output shows up here rather than as churn in a
	// committed file.
	modelsDir := shapeFixtureDir(t)

	// When: the snapshot is built repeatedly.
	first, err := buildShapeSnapshots(modelsDir, []string{"widget-service", "gadget"})
	if err != nil {
		t.Fatalf("build shape snapshots: %v", err)
	}
	for attempt := range 8 {
		next, err := buildShapeSnapshots(modelsDir, []string{"widget-service", "gadget"})
		if err != nil {
			t.Fatalf("build shape snapshots: %v", err)
		}

		// Then: every run is byte-identical.
		if len(next) != len(first) {
			t.Fatalf("attempt %d produced %d snapshots, want %d", attempt, len(next), len(first))
		}
		for service, contents := range first {
			if string(next[service]) != string(contents) {
				t.Fatalf("attempt %d produced different bytes for %s", attempt, service)
			}
		}
	}
}

func TestBuildShapeSnapshots_rejectsUnknownService(t *testing.T) {
	// Given: a reviewed list naming a service the corpus does not define.
	modelsDir := shapeFixtureDir(t)

	// When: the snapshot is built.
	_, err := buildShapeSnapshots(modelsDir, []string{"widget-service", "nonesuch"})

	// Then: it fails loudly rather than emitting a silently empty service.
	if err == nil || !strings.Contains(err.Error(), "nonesuch") {
		t.Fatalf("expected a failure naming the missing service, got %v", err)
	}
}

func TestReadShapeServices_parsesReviewedList(t *testing.T) {
	// Given: a list with comments, blanks and trailing whitespace.
	path := filepath.Join(t.TempDir(), "services.txt")
	writeModel(t, path, "# heading\n\nbatch   # rest-json\n  organizations\n\n")

	// When: it is read.
	services, err := readShapeServices(path)
	if err != nil {
		t.Fatalf("read shape services: %v", err)
	}

	// Then: the service keys come back sorted, with comments stripped.
	if len(services) != 2 || services[0] != "batch" || services[1] != "organizations" {
		t.Fatalf("unexpected services %q", services)
	}
}

func TestReadShapeServices_rejectsDuplicates(t *testing.T) {
	// Given: a list naming one service twice.
	path := filepath.Join(t.TempDir(), "services.txt")
	writeModel(t, path, "batch\nbatch\n")

	// When/Then: the duplicate is refused rather than silently collapsed.
	if _, err := readShapeServices(path); err == nil {
		t.Fatal("duplicate service unexpectedly accepted")
	}
}

func TestWriteOrCheckShapes_checksWithoutWriting(t *testing.T) {
	// Given: a committed snapshot directory.
	dir := t.TempDir()
	if err := writeOrCheckShapes(dir, map[string][]byte{"batch.json": []byte("current\n")}, false); err != nil {
		t.Fatalf("write shapes: %v", err)
	}

	// When: check mode sees matching content.
	if err := writeOrCheckShapes(dir, map[string][]byte{"batch.json": []byte("current\n")}, true); err != nil {
		t.Fatalf("check current shapes: %v", err)
	}

	// Then: differing content is rejected and nothing is overwritten.
	if err := writeOrCheckShapes(dir, map[string][]byte{"batch.json": []byte("new\n")}, true); err == nil {
		t.Fatal("stale shape snapshot check unexpectedly passed")
	}
	contents, err := os.ReadFile(filepath.Join(dir, "batch.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "current\n" {
		t.Fatalf("check mode overwrote the snapshot: %q", contents)
	}

	// ... and a file no reviewed service produces is a failure in check mode and
	// removed in write mode, so dropping a service cannot leave a stale file.
	if err := writeOrCheckShapes(dir, map[string][]byte{"other.json": []byte("x\n")}, true); err == nil {
		t.Fatal("extra shape snapshot unexpectedly accepted")
	}
	if err := writeOrCheckShapes(dir, map[string][]byte{"other.json": []byte("x\n")}, false); err != nil {
		t.Fatalf("write shapes: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "batch.json")); !os.IsNotExist(err) {
		t.Fatalf("stale snapshot was not removed: %v", err)
	}
}

func TestShapesDigest_coversContentAndNames(t *testing.T) {
	// Given: one snapshot directory.
	base := map[string][]byte{"batch.json": []byte("a"), "organizations.json": []byte("b")}

	// When/Then: the digest is stable, and changes when content or a name does.
	if ShapesDigest(base) != ShapesDigest(map[string][]byte{"organizations.json": []byte("b"), "batch.json": []byte("a")}) {
		t.Fatal("digest depends on map iteration order")
	}
	for name, other := range map[string]map[string][]byte{
		"changed content": {"batch.json": []byte("a"), "organizations.json": []byte("c")},
		"renamed file":    {"batch.json": []byte("a"), "orgs.json": []byte("b")},
		"removed file":    {"batch.json": []byte("a")},
	} {
		if ShapesDigest(base) == ShapesDigest(other) {
			t.Errorf("digest is blind to a %s", name)
		}
	}
}
