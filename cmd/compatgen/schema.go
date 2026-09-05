//go:build dev

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// JSON Schema validation of the model corpus.
//
// The schemas under compat/model, plus the two registry schemas under
// compat/suites, are the contract the interpreters and the suite loaders are
// written against, so the generator validates its own inputs against them on
// load and every output against them before it writes — the same documents,
// not a Go-side paraphrase that could drift.

const (
	schemaRecipe   = "recipe.schema.json"
	schemaValues   = "values.schema.json"
	schemaScenario = "scenario.schema.json"
	schemaGaps     = "gaps.schema.json"
	// The soak ledger is a curated input like the recipes and the values
	// table, not generator output — see promotions.go.
	schemaPromotions = "promotions.schema.json"
	// The registry schemas live with the loaders under compat/suites; the
	// generated one $refs the hand-written one, so both are compiled.
	schemaRegistry          = "registry.schema.json"
	schemaGeneratedRegistry = "registry.generated.schema.json"
)

type schemaSet struct {
	compiled map[string]*jsonschema.Schema
}

// loadSchemas compiles the model schemas from dir (compat/model).
func loadSchemas(dir string) (*schemaSet, error) {
	return compileSchemas(dir, schemaRecipe, schemaValues, schemaScenario, schemaGaps, schemaPromotions)
}

// loadRegistrySchemas compiles the registry schemas from dir (compat/suites).
func loadRegistrySchemas(dir string) (*schemaSet, error) {
	return compileSchemas(dir, schemaRegistry, schemaGeneratedRegistry)
}

// compileSchemas compiles a set of sibling draft-07 schemas that may refer to
// one another by relative reference.
func compileSchemas(dir string, names ...string) (*schemaSet, error) {
	set := &schemaSet{compiled: make(map[string]*jsonschema.Schema)}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft7)
	// Every schema is registered under its own $id before any is compiled:
	// recipe.schema.json refers to scenario.schema.json by a relative
	// reference, which resolves against the $id, not the file name.
	ids := make(map[string]string, len(names))
	for _, name := range names {
		path := filepath.Join(dir, name)
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read schema %s: %w", path, err)
		}
		document, err := jsonschema.UnmarshalJSON(bytes.NewReader(contents))
		if err != nil {
			return nil, fmt.Errorf("parse schema %s: %w", path, err)
		}
		id, _ := document.(map[string]any)["$id"].(string)
		if id == "" {
			return nil, fmt.Errorf("schema %s declares no $id", path)
		}
		if err := compiler.AddResource(id, document); err != nil {
			return nil, fmt.Errorf("register schema %s: %w", path, err)
		}
		ids[name] = id
	}
	for _, name := range names {
		compiled, err := compiler.Compile(ids[name])
		if err != nil {
			return nil, fmt.Errorf("compile schema %s: %w", filepath.Join(dir, name), err)
		}
		set.compiled[name] = compiled
	}
	return set, nil
}

// validate checks a JSON document against one of the schemas, reporting every
// violation rather than the first.
func (s *schemaSet) validate(name string, contents []byte) error {
	document, err := jsonschema.UnmarshalJSON(bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("not valid JSON: %w", err)
	}
	if err := s.compiled[name].Validate(document); err != nil {
		var ve *jsonschema.ValidationError
		if ok := asValidationError(err, &ve); ok {
			return fmt.Errorf("does not satisfy %s:\n  %s", name, strings.Join(flattenValidation(ve), "\n  "))
		}
		return fmt.Errorf("does not satisfy %s: %w", name, err)
	}
	return nil
}

func asValidationError(err error, target **jsonschema.ValidationError) bool {
	ve, ok := err.(*jsonschema.ValidationError)
	if ok {
		*target = ve
	}
	return ok
}

// flattenValidation renders the leaf causes of a validation error as
// "<instance pointer>: <message>" lines, sorted for a stable report.
func flattenValidation(ve *jsonschema.ValidationError) []string {
	var lines []string
	var walk func(unit *jsonschema.OutputUnit)
	walk = func(unit *jsonschema.OutputUnit) {
		if len(unit.Errors) == 0 {
			if unit.Error != nil {
				lines = append(lines, fmt.Sprintf("%s: %s", orRoot(unit.InstanceLocation), unit.Error.String()))
			}
			return
		}
		for i := range unit.Errors {
			walk(&unit.Errors[i])
		}
	}
	walk(ve.BasicOutput())
	sort.Strings(lines)
	return lines
}

func orRoot(location string) string {
	if location == "" {
		return "(document root)"
	}
	return location
}
