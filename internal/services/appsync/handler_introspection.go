package appsync

// handler_introspection.go — GraphQL introspection support.
//
// Implements __schema, __type, and __typename meta-fields per the
// GraphQL spec (June 2018). Introspection is built on top of the
// parsed *ast.Schema already held in memory by the schema parser.
//
// Entry points used by handler_execute.go:
//   - resolveIntrospectionField  — resolves __schema / __type fields
//   - buildFullSchemaIntrospection — builds the complete schema for GetIntrospectionSchema?format=JSON

import (
	"encoding/json"
	"sort"

	"github.com/vektah/gqlparser/v2/ast"
)

// resolveIntrospectionField resolves a single introspection meta-field
// (__schema or __type) against the given schema.
// frags is the full document fragment list for expanding fragment spreads.
func resolveIntrospectionField(field *ast.Field, schema *ast.Schema, vars map[string]any, frags ast.FragmentDefinitionList) any {
	switch field.Name {
	case "__schema":
		return buildSchemaObject(schema, field.SelectionSet, vars, frags)
	case "__type":
		typeName, _ := introspArgString(field, "name", vars)
		if typeName == "" {
			return nil
		}
		def, ok := schema.Types[typeName]
		if !ok {
			return nil
		}
		return buildNamedTypeObject(def, schema, field.SelectionSet, vars, frags)
	}
	return nil
}

// buildFullSchemaIntrospection builds a complete standard GraphQL introspection
// result for GetIntrospectionSchema?format=JSON. Returns JSON bytes of
// {"__schema": {...}}.
func buildFullSchemaIntrospection(schema *ast.Schema) ([]byte, error) {
	result := map[string]any{
		"__schema": buildFullSchemaObject(schema),
	}
	return json.Marshal(result)
}

// ─── Fragment expansion ───────────────────────────────────────────────────────

// introspFields collects *ast.Field selections, expanding fragment spreads
// and inline fragments. typeName is the introspection meta-type (e.g. "__Schema",
// "__Type") used to match type conditions. Fragments without a type condition
// always match.
func introspFields(ss ast.SelectionSet, typeName string, frags ast.FragmentDefinitionList) []*ast.Field {
	var out []*ast.Field
	for _, sel := range ss {
		switch s := sel.(type) {
		case *ast.Field:
			out = append(out, s)
		case *ast.InlineFragment:
			if s.TypeCondition == "" || s.TypeCondition == typeName {
				out = append(out, introspFields(s.SelectionSet, typeName, frags)...)
			}
		case *ast.FragmentSpread:
			fd := frags.ForName(s.Name)
			if fd != nil && (fd.TypeCondition == "" || fd.TypeCondition == typeName) {
				out = append(out, introspFields(fd.SelectionSet, typeName, frags)...)
			}
		}
	}
	return out
}

// ─── Argument helpers ─────────────────────────────────────────────────────────

func introspArgString(field *ast.Field, name string, vars map[string]any) (string, bool) {
	for _, arg := range field.Arguments {
		if arg.Name == name {
			v := astValueToGo(arg.Value, vars)
			s, ok := v.(string)
			return s, ok
		}
	}
	return "", false
}

func introspArgBool(field *ast.Field, name string, defaultVal bool, vars map[string]any) bool {
	for _, arg := range field.Arguments {
		if arg.Name == name {
			v := astValueToGo(arg.Value, vars)
			if b, ok := v.(bool); ok {
				return b
			}
		}
	}
	return defaultVal
}

// ─── __Schema ─────────────────────────────────────────────────────────────────

// buildSchemaObject builds the __schema introspection response, selecting only
// the requested fields from the selection set.
func buildSchemaObject(schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) map[string]any {
	result := map[string]any{}
	for _, f := range introspFields(ss, "__Schema", frags) {
		key := f.Alias
		if key == "" {
			key = f.Name
		}
		switch f.Name {
		case "description":
			result[key] = nil
		case "queryType":
			if schema.Query != nil {
				result[key] = buildNamedTypeObject(schema.Query, schema, f.SelectionSet, vars, frags)
			} else {
				result[key] = nil
			}
		case "mutationType":
			if schema.Mutation != nil {
				result[key] = buildNamedTypeObject(schema.Mutation, schema, f.SelectionSet, vars, frags)
			} else {
				result[key] = nil
			}
		case "subscriptionType":
			if schema.Subscription != nil {
				result[key] = buildNamedTypeObject(schema.Subscription, schema, f.SelectionSet, vars, frags)
			} else {
				result[key] = nil
			}
		case "types":
			result[key] = buildAllTypes(schema, f.SelectionSet, vars, frags)
		case "directives":
			result[key] = buildAllDirectives(schema, f.SelectionSet, frags)
		}
	}
	return result
}

// buildAllTypes returns all types in the schema as __Type objects, sorted by name.
func buildAllTypes(schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) []any {
	names := sortedTypeNames(schema)
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, buildNamedTypeObject(schema.Types[name], schema, ss, vars, frags))
	}
	return out
}

// buildAllDirectives returns all directive definitions as __Directive objects.
func buildAllDirectives(schema *ast.Schema, ss ast.SelectionSet, frags ast.FragmentDefinitionList) []any {
	names := make([]string, 0, len(schema.Directives))
	for name := range schema.Directives {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]any, 0, len(names))
	for _, name := range names {
		out = append(out, buildDirectiveObject(schema.Directives[name], schema, ss, frags))
	}
	return out
}

// ─── __Type (named type) ──────────────────────────────────────────────────────

// buildNamedTypeObject builds a __Type object for a named (non-wrapper) type.
func buildNamedTypeObject(def *ast.Definition, schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) map[string]any {
	if def == nil {
		return nil
	}
	result := map[string]any{}
	for _, f := range introspFields(ss, "__Type", frags) {
		key := f.Alias
		if key == "" {
			key = f.Name
		}
		switch f.Name {
		case "kind":
			result[key] = string(def.Kind)
		case "name":
			result[key] = def.Name
		case "description":
			result[key] = nilIfEmpty(def.Description)
		case "specifiedByURL":
			result[key] = nil
		case "isOneOf":
			result[key] = false
		case "fields":
			if def.Kind == ast.Object || def.Kind == ast.Interface {
				inclDeprecated := introspArgBool(f, "includeDeprecated", false, vars)
				var fields []any
				for _, fd := range def.Fields {
					if len(fd.Name) >= 2 && fd.Name[:2] == "__" {
						continue // skip built-in meta-fields
					}
					if isDeprecated(fd.Directives) && !inclDeprecated {
						continue
					}
					fields = append(fields, buildFieldDefObject(fd, schema, f.SelectionSet, vars, frags))
				}
				result[key] = fields
			} else {
				result[key] = nil
			}
		case "interfaces":
			if def.Kind == ast.Object {
				ifaces := make([]any, 0, len(def.Interfaces))
				for _, iname := range def.Interfaces {
					if idef, ok := schema.Types[iname]; ok {
						ifaces = append(ifaces, buildNamedTypeObject(idef, schema, f.SelectionSet, vars, frags))
					}
				}
				result[key] = ifaces
			} else {
				result[key] = nil
			}
		case "possibleTypes":
			if def.Kind == ast.Interface || def.Kind == ast.Union {
				result[key] = buildPossibleTypes(def, schema, f.SelectionSet, vars, frags)
			} else {
				result[key] = nil
			}
		case "enumValues":
			if def.Kind == ast.Enum {
				inclDeprecated := introspArgBool(f, "includeDeprecated", false, vars)
				var vals []any
				for _, ev := range def.EnumValues {
					if isDeprecated(ev.Directives) && !inclDeprecated {
						continue
					}
					vals = append(vals, buildEnumValueObject(ev, f.SelectionSet, frags))
				}
				result[key] = vals
			} else {
				result[key] = nil
			}
		case "inputFields":
			if def.Kind == ast.InputObject {
				var fields []any
				for _, fd := range def.Fields {
					fields = append(fields, buildInputValueFromField(fd, schema, f.SelectionSet, vars, frags))
				}
				result[key] = fields
			} else {
				result[key] = nil
			}
		case "ofType":
			// Named types are never wrapper types — ofType is always null here.
			result[key] = nil
		}
	}
	return result
}

// buildPossibleTypes collects types that implement an interface or are members of a union.
func buildPossibleTypes(def *ast.Definition, schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) []any {
	var possible []any
	//exhaustive:ignore
	switch def.Kind {
	case ast.Interface:
		for _, name := range sortedTypeNames(schema) {
			td := schema.Types[name]
			if td.Kind != ast.Object {
				continue
			}
			for _, iname := range td.Interfaces {
				if iname == def.Name {
					possible = append(possible, buildNamedTypeObject(td, schema, ss, vars, frags))
					break
				}
			}
		}
	case ast.Union:
		for _, utype := range def.Types {
			if utd, ok := schema.Types[utype]; ok {
				possible = append(possible, buildNamedTypeObject(utd, schema, ss, vars, frags))
			}
		}
	default:
		// no-op for kinds that cannot have possibleTypes.
	}
	return possible
}

// ─── __Type (wrapper: NON_NULL / LIST) ────────────────────────────────────────

// buildTypeRef builds a __Type introspection object from an *ast.Type reference.
// Handles NON_NULL and LIST wrapper types as well as named types.
func buildTypeRef(t *ast.Type, schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) map[string]any {
	if t == nil {
		return nil
	}

	if t.NonNull {
		// NON_NULL wrapper: inner is the same type without the NonNull flag.
		inner := &ast.Type{NamedType: t.NamedType, Elem: t.Elem}
		result := map[string]any{}
		for _, f := range introspFields(ss, "__Type", frags) {
			key := f.Alias
			if key == "" {
				key = f.Name
			}
			switch f.Name {
			case "kind":
				result[key] = "NON_NULL"
			case "name":
				result[key] = nil
			case "ofType":
				result[key] = buildTypeRef(inner, schema, f.SelectionSet, vars, frags)
			default:
				result[key] = nil
			}
		}
		return result
	}

	if t.Elem != nil {
		// LIST wrapper.
		result := map[string]any{}
		for _, f := range introspFields(ss, "__Type", frags) {
			key := f.Alias
			if key == "" {
				key = f.Name
			}
			switch f.Name {
			case "kind":
				result[key] = "LIST"
			case "name":
				result[key] = nil
			case "ofType":
				result[key] = buildTypeRef(t.Elem, schema, f.SelectionSet, vars, frags)
			default:
				result[key] = nil
			}
		}
		return result
	}

	// Named type — delegate to buildNamedTypeObject.
	def, ok := schema.Types[t.NamedType]
	if !ok {
		return nil
	}
	return buildNamedTypeObject(def, schema, ss, vars, frags)
}

// ─── __Field ──────────────────────────────────────────────────────────────────

// buildFieldDefObject builds a __Field introspection object from a FieldDefinition.
func buildFieldDefObject(fd *ast.FieldDefinition, schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) map[string]any {
	result := map[string]any{}
	for _, f := range introspFields(ss, "__Field", frags) {
		key := f.Alias
		if key == "" {
			key = f.Name
		}
		switch f.Name {
		case "name":
			result[key] = fd.Name
		case "description":
			result[key] = nilIfEmpty(fd.Description)
		case "args":
			inclDeprecated := introspArgBool(f, "includeDeprecated", false, vars)
			var args []any
			for _, arg := range fd.Arguments {
				if isDeprecated(arg.Directives) && !inclDeprecated {
					continue
				}
				args = append(args, buildInputValueFromArg(arg, schema, f.SelectionSet, vars, frags))
			}
			if args == nil {
				args = []any{}
			}
			result[key] = args
		case "type":
			result[key] = buildTypeRef(fd.Type, schema, f.SelectionSet, vars, frags)
		case "isDeprecated":
			result[key] = isDeprecated(fd.Directives)
		case "deprecationReason":
			if isDeprecated(fd.Directives) {
				result[key] = nilIfEmpty(deprecationReason(fd.Directives))
			} else {
				result[key] = nil
			}
		}
	}
	return result
}

// ─── __InputValue ─────────────────────────────────────────────────────────────

// buildInputValueFromArg builds a __InputValue from an ArgumentDefinition.
func buildInputValueFromArg(arg *ast.ArgumentDefinition, schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) map[string]any {
	result := map[string]any{}
	for _, f := range introspFields(ss, "__InputValue", frags) {
		key := f.Alias
		if key == "" {
			key = f.Name
		}
		switch f.Name {
		case "name":
			result[key] = arg.Name
		case "description":
			result[key] = nilIfEmpty(arg.Description)
		case "type":
			result[key] = buildTypeRef(arg.Type, schema, f.SelectionSet, vars, frags)
		case "defaultValue":
			if arg.DefaultValue != nil {
				result[key] = arg.DefaultValue.String()
			} else {
				result[key] = nil
			}
		case "isDeprecated":
			result[key] = isDeprecated(arg.Directives)
		case "deprecationReason":
			if isDeprecated(arg.Directives) {
				result[key] = nilIfEmpty(deprecationReason(arg.Directives))
			} else {
				result[key] = nil
			}
		}
	}
	return result
}

// buildInputValueFromField builds a __InputValue from a FieldDefinition (for input objects).
func buildInputValueFromField(fd *ast.FieldDefinition, schema *ast.Schema, ss ast.SelectionSet, vars map[string]any, frags ast.FragmentDefinitionList) map[string]any {
	result := map[string]any{}
	for _, f := range introspFields(ss, "__InputValue", frags) {
		key := f.Alias
		if key == "" {
			key = f.Name
		}
		switch f.Name {
		case "name":
			result[key] = fd.Name
		case "description":
			result[key] = nilIfEmpty(fd.Description)
		case "type":
			result[key] = buildTypeRef(fd.Type, schema, f.SelectionSet, vars, frags)
		case "defaultValue":
			if fd.DefaultValue != nil {
				result[key] = fd.DefaultValue.String()
			} else {
				result[key] = nil
			}
		case "isDeprecated":
			result[key] = isDeprecated(fd.Directives)
		case "deprecationReason":
			if isDeprecated(fd.Directives) {
				result[key] = nilIfEmpty(deprecationReason(fd.Directives))
			} else {
				result[key] = nil
			}
		}
	}
	return result
}

// ─── __EnumValue ──────────────────────────────────────────────────────────────

// buildEnumValueObject builds a __EnumValue introspection object.
func buildEnumValueObject(ev *ast.EnumValueDefinition, ss ast.SelectionSet, frags ast.FragmentDefinitionList) map[string]any {
	result := map[string]any{}
	for _, f := range introspFields(ss, "__EnumValue", frags) {
		key := f.Alias
		if key == "" {
			key = f.Name
		}
		switch f.Name {
		case "name":
			result[key] = ev.Name
		case "description":
			result[key] = nilIfEmpty(ev.Description)
		case "isDeprecated":
			result[key] = isDeprecated(ev.Directives)
		case "deprecationReason":
			if isDeprecated(ev.Directives) {
				result[key] = nilIfEmpty(deprecationReason(ev.Directives))
			} else {
				result[key] = nil
			}
		}
	}
	return result
}

// ─── __Directive ──────────────────────────────────────────────────────────────

// buildDirectiveObject builds a __Directive introspection object.
func buildDirectiveObject(dd *ast.DirectiveDefinition, schema *ast.Schema, ss ast.SelectionSet, frags ast.FragmentDefinitionList) map[string]any {
	result := map[string]any{}
	for _, f := range introspFields(ss, "__Directive", frags) {
		key := f.Alias
		if key == "" {
			key = f.Name
		}
		switch f.Name {
		case "name":
			result[key] = dd.Name
		case "description":
			result[key] = nilIfEmpty(dd.Description)
		case "locations":
			locs := make([]any, len(dd.Locations))
			for i, loc := range dd.Locations {
				locs[i] = string(loc)
			}
			result[key] = locs
		case "args":
			inclDeprecated := introspArgBool(f, "includeDeprecated", false, nil)
			var args []any
			for _, arg := range dd.Arguments {
				if isDeprecated(arg.Directives) && !inclDeprecated {
					continue
				}
				args = append(args, buildInputValueFromArg(arg, schema, f.SelectionSet, nil, frags))
			}
			if args == nil {
				args = []any{}
			}
			result[key] = args
		case "isRepeatable":
			result[key] = dd.IsRepeatable
		}
	}
	return result
}

// ─── Full introspection (for GetIntrospectionSchema?format=JSON) ──────────────

// buildFullSchemaObject builds a complete __schema introspection object
// without selection filtering. All standard fields are included.
func buildFullSchemaObject(schema *ast.Schema) map[string]any {
	var queryType, mutationType, subscriptionType any
	if schema.Query != nil {
		queryType = map[string]any{"name": schema.Query.Name}
	}
	if schema.Mutation != nil {
		mutationType = map[string]any{"name": schema.Mutation.Name}
	}
	if schema.Subscription != nil {
		subscriptionType = map[string]any{"name": schema.Subscription.Name}
	}

	var types []any
	for _, name := range sortedTypeNames(schema) {
		types = append(types, buildFullTypeObject(schema.Types[name], schema))
	}

	var directives []any
	dnames := make([]string, 0, len(schema.Directives))
	for n := range schema.Directives {
		dnames = append(dnames, n)
	}
	sort.Strings(dnames)
	for _, name := range dnames {
		directives = append(directives, buildFullDirectiveObject(schema.Directives[name], schema))
	}

	return map[string]any{
		"description":      nil,
		"queryType":        queryType,
		"mutationType":     mutationType,
		"subscriptionType": subscriptionType,
		"types":            types,
		"directives":       directives,
	}
}

func buildFullTypeObject(def *ast.Definition, schema *ast.Schema) map[string]any {
	out := map[string]any{
		"kind":           string(def.Kind),
		"name":           def.Name,
		"description":    nilIfEmpty(def.Description),
		"specifiedByURL": nil,
		"interfaces":     nil,
		"possibleTypes":  nil,
		"fields":         nil,
		"enumValues":     nil,
		"inputFields":    nil,
	}

	//exhaustive:ignore
	switch def.Kind {
	case ast.Object:
		var fields []any
		for _, fd := range def.Fields {
			if len(fd.Name) >= 2 && fd.Name[:2] == "__" {
				continue
			}
			fields = append(fields, buildFullFieldObject(fd, schema))
		}
		out["fields"] = fields
		ifaces := make([]any, 0, len(def.Interfaces))
		for _, iname := range def.Interfaces {
			if idef, ok := schema.Types[iname]; ok {
				ifaces = append(ifaces, map[string]any{"kind": string(idef.Kind), "name": idef.Name, "ofType": nil})
			}
		}
		out["interfaces"] = ifaces

	case ast.Interface:
		var fields []any
		for _, fd := range def.Fields {
			if len(fd.Name) >= 2 && fd.Name[:2] == "__" {
				continue
			}
			fields = append(fields, buildFullFieldObject(fd, schema))
		}
		out["fields"] = fields
		out["possibleTypes"] = buildFullPossibleTypes(def, schema)

	case ast.Union:
		out["possibleTypes"] = buildFullPossibleTypes(def, schema)

	case ast.Enum:
		var vals []any
		for _, ev := range def.EnumValues {
			vals = append(vals, map[string]any{
				"name":              ev.Name,
				"description":       nilIfEmpty(ev.Description),
				"isDeprecated":      isDeprecated(ev.Directives),
				"deprecationReason": nil,
			})
		}
		out["enumValues"] = vals

	case ast.InputObject:
		var fields []any
		for _, fd := range def.Fields {
			fields = append(fields, map[string]any{
				"name":              fd.Name,
				"description":       nilIfEmpty(fd.Description),
				"type":              buildFullTypeRef(fd.Type, schema),
				"defaultValue":      nil,
				"isDeprecated":      false,
				"deprecationReason": nil,
			})
		}
		out["inputFields"] = fields
	default:
		// no-op for scalar and unknown kinds.
	}

	return out
}

func buildFullFieldObject(fd *ast.FieldDefinition, schema *ast.Schema) map[string]any {
	var args []any
	for _, arg := range fd.Arguments {
		args = append(args, map[string]any{
			"name":              arg.Name,
			"description":       nilIfEmpty(arg.Description),
			"type":              buildFullTypeRef(arg.Type, schema),
			"defaultValue":      nil,
			"isDeprecated":      false,
			"deprecationReason": nil,
		})
	}
	if args == nil {
		args = []any{}
	}
	return map[string]any{
		"name":              fd.Name,
		"description":       nilIfEmpty(fd.Description),
		"args":              args,
		"type":              buildFullTypeRef(fd.Type, schema),
		"isDeprecated":      isDeprecated(fd.Directives),
		"deprecationReason": nil,
	}
}

func buildFullTypeRef(t *ast.Type, schema *ast.Schema) map[string]any {
	if t == nil {
		return nil
	}
	if t.NonNull {
		inner := &ast.Type{NamedType: t.NamedType, Elem: t.Elem}
		return map[string]any{
			"kind":   "NON_NULL",
			"name":   nil,
			"ofType": buildFullTypeRef(inner, schema),
		}
	}
	if t.Elem != nil {
		return map[string]any{
			"kind":   "LIST",
			"name":   nil,
			"ofType": buildFullTypeRef(t.Elem, schema),
		}
	}
	def, ok := schema.Types[t.NamedType]
	if !ok {
		return map[string]any{"kind": "SCALAR", "name": t.NamedType, "ofType": nil}
	}
	return map[string]any{
		"kind":   string(def.Kind),
		"name":   def.Name,
		"ofType": nil,
	}
}

func buildFullPossibleTypes(def *ast.Definition, schema *ast.Schema) []any {
	var possible []any
	//exhaustive:ignore
	switch def.Kind {
	case ast.Interface:
		for _, name := range sortedTypeNames(schema) {
			td := schema.Types[name]
			if td.Kind != ast.Object {
				continue
			}
			for _, iname := range td.Interfaces {
				if iname == def.Name {
					possible = append(possible, map[string]any{"kind": string(td.Kind), "name": td.Name, "ofType": nil})
					break
				}
			}
		}
	case ast.Union:
		for _, utype := range def.Types {
			if utd, ok := schema.Types[utype]; ok {
				possible = append(possible, map[string]any{"kind": string(utd.Kind), "name": utd.Name, "ofType": nil})
			}
		}
	default:
		// no-op for kinds that cannot have possibleTypes.
	}
	return possible
}

func buildFullDirectiveObject(dd *ast.DirectiveDefinition, schema *ast.Schema) map[string]any {
	locs := make([]any, len(dd.Locations))
	for i, loc := range dd.Locations {
		locs[i] = string(loc)
	}
	var args []any
	for _, arg := range dd.Arguments {
		args = append(args, map[string]any{
			"name":              arg.Name,
			"description":       nilIfEmpty(arg.Description),
			"type":              buildFullTypeRef(arg.Type, schema),
			"defaultValue":      nil,
			"isDeprecated":      false,
			"deprecationReason": nil,
		})
	}
	if args == nil {
		args = []any{}
	}
	return map[string]any{
		"name":         dd.Name,
		"description":  nilIfEmpty(dd.Description),
		"locations":    locs,
		"args":         args,
		"isRepeatable": dd.IsRepeatable,
	}
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// nilIfEmpty returns nil for an empty string, otherwise the string itself.
func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// sortedTypeNames returns the schema's type names in alphabetical order.
func sortedTypeNames(schema *ast.Schema) []string {
	names := make([]string, 0, len(schema.Types))
	for name := range schema.Types {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// isDeprecated returns true if the directive list contains @deprecated.
func isDeprecated(directives ast.DirectiveList) bool {
	return directives.ForName("deprecated") != nil
}

// deprecationReason returns the reason string from @deprecated, or "".
func deprecationReason(directives ast.DirectiveList) string {
	d := directives.ForName("deprecated")
	if d == nil {
		return ""
	}
	reason := d.Arguments.ForName("reason")
	if reason == nil || reason.Value == nil {
		return ""
	}
	return reason.Value.Raw
}
