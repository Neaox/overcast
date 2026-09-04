//go:build dev

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The committed corpus — compat/model and compat/suites/registry.generated.json.
//
// These are the offline analogue of the shape snapshot's sha gate: an
// ordinary pull request cannot run the AWS models, but it can prove the
// committed scenarios, gaps and registry are byte-for-byte what the generator
// produces from the committed recipes, values and shapes.

var repoRoot = filepath.Join("..", "..")

func TestCommittedCorpus_isInSyncWithTheGenerator(t *testing.T) {
	c, err := loadCorpus(repoRoot)
	if err != nil {
		t.Fatalf("load corpus: %v", err)
	}
	_, outputs, err := generateAll(repoRoot, c)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if err := outputs.check(repoRoot); err != nil {
		t.Fatal(err)
	}
	if err := checkStaleScenarios(repoRoot, outputs, true); err != nil {
		t.Fatal(err)
	}
}

func TestCommittedCorpus_validatesAgainstItsSchemas(t *testing.T) {
	model, err := loadSchemas(filepath.Join(repoRoot, filepath.FromSlash(modelDir)))
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(repoRoot, filepath.FromSlash(scenarioDir)))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		contents := readFile(t, filepath.Join(repoRoot, filepath.FromSlash(scenarioDir), entry.Name()))
		if err := model.validate(schemaScenario, contents); err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
		}
		var s scenario
		if err := decodeStrict(contents, &s); err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
		} else if err := validateScenario(&s); err != nil {
			t.Errorf("%s: %v", entry.Name(), err)
		}
	}
	if err := model.validate(schemaGaps, readFile(t, filepath.Join(repoRoot, filepath.FromSlash(gapsPath)))); err != nil {
		t.Errorf("gaps.json: %v", err)
	}

	// The registry validates against the compat suite schemas, which are the
	// loaders' contract, not this package's.
	suites, err := compileSchemas(filepath.Join(repoRoot, "compat", "suites"), "registry.schema.json", "registry.generated.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := suites.validate("registry.generated.schema.json", readFile(t, filepath.Join(repoRoot, filepath.FromSlash(registryPath)))); err != nil {
		t.Errorf("registry.generated.json: %v", err)
	}
}

// TestRegistryIsEmptyExactlyWhileNoBackendExists pins the tie between the
// backend table and the registry, not the file's current emptiness: G0's
// "an empty generated registry behaves as today" gate holds until a suite
// gains a scenario backend, and the moment one does every group is
// registered with exactly that suite list.
func TestRegistryIsEmptyExactlyWhileNoBackendExists(t *testing.T) {
	_, gen := generateFixture(t)
	scenarios := []*scenario{gen.scenario}

	empty := buildRegistry(scenarios, nil)
	if len(empty.Groups) != 0 {
		t.Fatalf("with no backend the registry must be empty, got %d groups", len(empty.Groups))
	}

	full := buildRegistry(scenarios, []string{"python-sdk", "cli"})
	if len(full.Groups) != len(gen.scenario.Groups) {
		t.Fatalf("with a backend every group is registered: got %d, want %d", len(full.Groups), len(gen.scenario.Groups))
	}
	for _, g := range full.Groups {
		if strings.Join(g.Suites, ",") != "cli,python-sdk" || !g.Generated || g.State != generatedStateCandidate || g.Scenario != scenarioPath("widgets") {
			t.Errorf("registered group %+v", g)
		}
		for _, tc := range g.Tests {
			if tc.Name == "" {
				t.Errorf("group %s has a nameless test", g.Name)
			}
		}
	}

	// The committed table decides the committed file.
	committed := buildRegistry(scenarios, scenarioBackends)
	if (len(committed.Groups) == 0) != (len(scenarioBackends) == 0) {
		t.Fatalf("scenarioBackends=%v but the registry has %d groups", scenarioBackends, len(committed.Groups))
	}
}

func TestCheck_detectsAHandEdit(t *testing.T) {
	// Given: a copy of the corpus with one byte changed in a generated file.
	root := copyCorpus(t)
	path := filepath.Join(root, filepath.FromSlash(scenarioPath("sqs")))
	contents := readFile(t, path)
	edited := bytes.Replace(contents, []byte(`"maxAttempts": 10`), []byte(`"maxAttempts": 11`), 1)
	if bytes.Equal(edited, contents) {
		t.Fatal("the fixture edit did not apply")
	}
	writeFile(t, path, string(edited))

	// When: -check runs against it.
	var stdout, stderr bytes.Buffer
	code := run([]string{"-root", root, "-check"}, &stdout, &stderr)

	// Then: it fails naming the file, and writes nothing.
	if code != 1 || !strings.Contains(stderr.String(), scenarioPath("sqs")) {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Equal(readFile(t, path), edited) {
		t.Fatal("-check overwrote the file")
	}

	// And: a plain run repairs it.
	if code := run([]string{"-root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("regenerate: code=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Equal(readFile(t, path), contents) {
		t.Fatal("regeneration did not restore the committed bytes")
	}
}

func TestRun_removesAScenarioWhoseRecipeIsGone(t *testing.T) {
	root := copyCorpus(t)
	stale := filepath.Join(root, filepath.FromSlash(scenarioDir), "gone.json")
	writeFile(t, stale, "{}")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", root, "-check"}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "gone.json") {
		t.Fatalf("-check accepted a stale scenario: code=%d stderr=%s", code, stderr.String())
	}
	if code := run([]string{"-root", root}, &stdout, &stderr); code != 0 {
		t.Fatalf("regenerate: %s", stderr.String())
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale scenario survived regeneration")
	}
}

func TestRun_refusesAServiceOutsideTheSnapshot(t *testing.T) {
	root := copyCorpus(t)
	writeFile(t, filepath.Join(root, filepath.FromSlash(recipesDir), "kinesis.json"),
		`{"service":"kinesis","resources":[{"id":"stream","create":{"op":"CreateStream"}}]}`)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", root}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "shapes-services.txt") {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
}

// copyCorpus copies the inputs and outputs the generator reads and writes
// into a temporary root, so a test can edit them.
func copyCorpus(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range []string{modelDir, "compat/suites", shapesDir} {
		src := filepath.Join(repoRoot, filepath.FromSlash(rel))
		if err := filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".json") && !strings.HasSuffix(path, ".txt") {
				return nil
			}
			relPath, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			dest := filepath.Join(root, relPath)
			if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
				return err
			}
			return os.WriteFile(dest, readFile(t, path), 0o644)
		}); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return contents
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}
