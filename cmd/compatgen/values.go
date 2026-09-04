//go:build dev

package main

import (
	"fmt"
	"os"
)

// The curated literal table, compat/model/values.json.
//
// Binding rule 3 (§3.3): when neither an explicit param nor a bind nor an
// export name supplies a required member, a curated literal may. Lookup is
// (service, operation, member), then (service, target shape), then (service,
// member name) — narrowest first, so a literal for one operation never leaks
// into another by accident, while a shape-level entry (AccountId → a
// twelve-digit string) covers every operation that takes that shape.
//
// Entries are plain JSON literals, never expressions: a value that has to
// refer to another resource is a `binds` entry on that resource, not a value.

type valuesTable struct {
	Comment  string                   `json:"$comment,omitempty"`
	Version  int                      `json:"version"`
	Services map[string]serviceValues `json:"services"`
}

type serviceValues struct {
	Comment    string                    `json:"$comment,omitempty"`
	Operations map[string]map[string]any `json:"operations,omitempty"`
	Shapes     map[string]any            `json:"shapes,omitempty"`
	Members    map[string]any            `json:"members,omitempty"`
}

// valueSource says which tier supplied a literal, for the review report.
type valueSource string

const (
	valueFromOperation valueSource = "values.operations"
	valueFromShape     valueSource = "values.shapes"
	valueFromMember    valueSource = "values.members"
)

func loadValues(path string, schema *schemaSet) (*valuesTable, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read values %s: %w", path, err)
	}
	if err := schema.validate(schemaValues, contents); err != nil {
		return nil, fmt.Errorf("values %s: %w", path, err)
	}
	var table valuesTable
	if err := decodeStrict(contents, &table); err != nil {
		return nil, fmt.Errorf("values %s: %w", path, err)
	}
	for service, entries := range table.Services {
		for op, members := range entries.Operations {
			for member, value := range members {
				if _, _, isExpr := exprOf(value); isExpr || hasExpression(value) {
					return nil, fmt.Errorf("values %s: %s.operations.%s.%s: values are literals, not expressions", path, service, op, member)
				}
			}
		}
		for shape, value := range entries.Shapes {
			if hasExpression(value) {
				return nil, fmt.Errorf("values %s: %s.shapes.%s: values are literals, not expressions", path, service, shape)
			}
		}
		for member, value := range entries.Members {
			if hasExpression(value) {
				return nil, fmt.Errorf("values %s: %s.members.%s: values are literals, not expressions", path, service, member)
			}
		}
	}
	return &table, nil
}

func hasExpression(v any) bool {
	found := false
	walkValue(v, func(string, any) { found = true })
	if found {
		return true
	}
	// walkValue only visits well-formed expressions; an object with a $-key
	// that is not one is still not a literal.
	switch value := v.(type) {
	case map[string]any:
		for k, child := range value {
			if len(k) > 0 && k[0] == '$' {
				return true
			}
			if hasExpression(child) {
				return true
			}
		}
	case []any:
		for _, child := range value {
			if hasExpression(child) {
				return true
			}
		}
	}
	return false
}

// lookup applies the three tiers in order.
func (t *valuesTable) lookup(service, op, member, shape string) (any, valueSource, bool) {
	if t == nil {
		return nil, "", false
	}
	entries, ok := t.Services[service]
	if !ok {
		return nil, "", false
	}
	if members, ok := entries.Operations[op]; ok {
		if value, ok := members[member]; ok {
			return cloneValue(value), valueFromOperation, true
		}
	}
	if value, ok := entries.Shapes[shape]; ok {
		return cloneValue(value), valueFromShape, true
	}
	if value, ok := entries.Members[member]; ok {
		return cloneValue(value), valueFromMember, true
	}
	return nil, "", false
}
