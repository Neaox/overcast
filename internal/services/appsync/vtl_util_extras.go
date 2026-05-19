package appsync

// vtl_util_extras.go — additional $util sub-objects for VTL evaluation.
//
// Provides:
//   - $util.transform — DynamoDB filter/condition expression builders
//   - $util.http      — URL encoding/decoding helpers
//   - $util.str       — string utilities
//   - $ctx.info.selectionSetGraphQL — serialisation helper

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
)

// ─── $util.transform additions to buildUtil ──────────────────────────────────

// vtlBuildTransform returns the $util.transform sub-object.
func vtlBuildTransform() map[string]any {
	return map[string]any{
		"toDynamoDBFilterExpression":    vtlFunc(vtlTransformToDynamoDBFilterExpression),
		"toDynamoDBConditionExpression": vtlFunc(vtlTransformToDynamoDBConditionExpression),
	}
}

// vtlBuildHttp returns the $util.http sub-object.
func vtlBuildHttp() map[string]any {
	return map[string]any{
		"encodeUrl":   vtlFunc(vtlHttpEncodeUrl),
		"decodeUrl":   vtlFunc(vtlHttpDecodeUrl),
		"copyHeaders": vtlFunc(vtlHttpCopyHeaders),
	}
}

// vtlBuildStr returns the $util.str sub-object.
func vtlBuildStr() map[string]any {
	return map[string]any{
		"toLower": vtlFunc(func(args []any) any { return strings.ToLower(vtlStrArg(args)) }),
		"toUpper": vtlFunc(func(args []any) any { return strings.ToUpper(vtlStrArg(args)) }),
		"toReplace": vtlFunc(func(args []any) any {
			if len(args) < 3 {
				return vtlStrArg(args)
			}
			return strings.ReplaceAll(vtlToString(args[0]), vtlToString(args[1]), vtlToString(args[2]))
		}),
		"trim": vtlFunc(func(args []any) any { return strings.TrimSpace(vtlStrArg(args)) }),
		"isEmpty": vtlFunc(func(args []any) any {
			s := vtlStrArg(args)
			return len(strings.TrimSpace(s)) == 0
		}),
		"beginsWith": vtlFunc(func(args []any) any {
			if len(args) < 2 {
				return false
			}
			return strings.HasPrefix(vtlToString(args[0]), vtlToString(args[1]))
		}),
		"endsWith": vtlFunc(func(args []any) any {
			if len(args) < 2 {
				return false
			}
			return strings.HasSuffix(vtlToString(args[0]), vtlToString(args[1]))
		}),
		"normalize": vtlFunc(func(args []any) any {
			// Unicode NFC normalisation.
			if len(args) == 0 {
				return ""
			}
			return strings.ToValidUTF8(vtlToString(args[0]), "")
		}),
	}
}

func vtlStrArg(args []any) string {
	if len(args) == 0 {
		return ""
	}
	return vtlToString(args[0])
}

// ─── $util.transform implementations ─────────────────────────────────────────

// vtlTransformToDynamoDBFilterExpression converts an AppSync filter expression
// object to a DynamoDB filter expression struct.
//
// AppSync filter format:
//
//	{
//	  "and": [ {filter}, ... ],
//	  "or":  [ {filter}, ... ],
//	  "not": {filter},
//	  "fieldName": { "eq": "v", "ne": "v", "lt": "v", "le": "v",
//	                 "gt": "v", "ge": "v", "contains": "v",
//	                 "notContains": "v", "between": ["lo","hi"],
//	                 "beginsWith": "v", "in": ["v",...],
//	                 "attributeExists": bool, "attributeType": "S",
//	                 "size": {comparison} }
//	}
func vtlTransformToDynamoDBFilterExpression(args []any) any {
	if len(args) == 0 {
		return "{}"
	}
	filter, ok := args[0].(map[string]any)
	if !ok {
		return "{}"
	}
	b := &dynExprBuilder{}
	expr := b.buildFilter(filter)
	result := map[string]any{
		"expression":       expr,
		"expressionNames":  b.names,
		"expressionValues": b.values,
	}
	out, _ := json.Marshal(result)
	return string(out)
}

// vtlTransformToDynamoDBConditionExpression is identical to the filter version
// for our purposes (same DSL, used for condition checks on writes).
func vtlTransformToDynamoDBConditionExpression(args []any) any {
	return vtlTransformToDynamoDBFilterExpression(args)
}

// ─── DynamoDB expression builder ─────────────────────────────────────────────

type dynExprBuilder struct {
	names  map[string]any
	values map[string]any
	nameN  int
	valN   int
}

func (b *dynExprBuilder) nameKey() string {
	b.nameN++
	return fmt.Sprintf("#n%d", b.nameN)
}

func (b *dynExprBuilder) valKey() string {
	b.valN++
	return fmt.Sprintf(":v%d", b.valN)
}

func (b *dynExprBuilder) addName(field string) string {
	if b.names == nil {
		b.names = map[string]any{}
	}
	k := b.nameKey()
	b.names[k] = field
	return k
}

func (b *dynExprBuilder) addValue(v any) string {
	if b.values == nil {
		b.values = map[string]any{}
	}
	k := b.valKey()
	b.values[k] = toDynamoDBValue(v)
	return k
}

// buildFilter recurses through the AppSync filter DSL.
func (b *dynExprBuilder) buildFilter(filter map[string]any) string {
	var parts []string

	if and, ok := filter["and"]; ok {
		if list, ok := and.([]any); ok {
			var sub []string
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					sub = append(sub, b.buildFilter(m))
				}
			}
			if len(sub) > 0 {
				parts = append(parts, "("+strings.Join(sub, " AND ")+")")
			}
		}
	}
	if or, ok := filter["or"]; ok {
		if list, ok := or.([]any); ok {
			var sub []string
			for _, item := range list {
				if m, ok := item.(map[string]any); ok {
					sub = append(sub, b.buildFilter(m))
				}
			}
			if len(sub) > 0 {
				parts = append(parts, "("+strings.Join(sub, " OR ")+")")
			}
		}
	}
	if not, ok := filter["not"]; ok {
		if m, ok := not.(map[string]any); ok {
			parts = append(parts, "NOT ("+b.buildFilter(m)+")")
		}
	}

	// Field-level comparisons.
	for field, rawOps := range filter {
		if field == "and" || field == "or" || field == "not" {
			continue
		}
		ops, ok := rawOps.(map[string]any)
		if !ok {
			continue
		}
		nk := b.addName(field)
		for op, val := range ops {
			var clause string
			switch op {
			case "eq":
				vk := b.addValue(val)
				clause = nk + " = " + vk
			case "ne":
				vk := b.addValue(val)
				clause = nk + " <> " + vk
			case "lt":
				vk := b.addValue(val)
				clause = nk + " < " + vk
			case "le":
				vk := b.addValue(val)
				clause = nk + " <= " + vk
			case "gt":
				vk := b.addValue(val)
				clause = nk + " > " + vk
			case "ge":
				vk := b.addValue(val)
				clause = nk + " >= " + vk
			case "contains":
				vk := b.addValue(val)
				clause = "contains(" + nk + ", " + vk + ")"
			case "notContains":
				vk := b.addValue(val)
				clause = "not contains(" + nk + ", " + vk + ")"
			case "beginsWith":
				vk := b.addValue(val)
				clause = "begins_with(" + nk + ", " + vk + ")"
			case "between":
				if list, ok := val.([]any); ok && len(list) >= 2 {
					v1 := b.addValue(list[0])
					v2 := b.addValue(list[1])
					clause = nk + " BETWEEN " + v1 + " AND " + v2
				}
			case "in":
				if list, ok := val.([]any); ok {
					var vks []string
					for _, item := range list {
						vks = append(vks, b.addValue(item))
					}
					clause = nk + " IN (" + strings.Join(vks, ", ") + ")"
				}
			case "attributeExists":
				if exists, ok := val.(bool); ok && exists {
					clause = "attribute_exists(" + nk + ")"
				} else {
					clause = "attribute_not_exists(" + nk + ")"
				}
			case "attributeType":
				vk := b.addValue(val)
				clause = "attribute_type(" + nk + ", " + vk + ")"
			case "size":
				// size comparison: {"size": {"gt": 5}}
				if sizeCmp, ok := val.(map[string]any); ok {
					for sop, sv := range sizeCmp {
						vk := b.addValue(sv)
						switch sop {
						case "eq":
							clause = "size(" + nk + ") = " + vk
						case "ne":
							clause = "size(" + nk + ") <> " + vk
						case "lt":
							clause = "size(" + nk + ") < " + vk
						case "le":
							clause = "size(" + nk + ") <= " + vk
						case "gt":
							clause = "size(" + nk + ") > " + vk
						case "ge":
							clause = "size(" + nk + ") >= " + vk
						default:
							clause = "size(" + nk + ") = " + vk
						}
					}
				}
			}
			if clause != "" {
				parts = append(parts, clause)
			}
		}
	}

	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " AND ")
}

// ─── $util.http implementations ───────────────────────────────────────────────

func vtlHttpEncodeUrl(args []any) any {
	if len(args) == 0 {
		return ""
	}
	return url.QueryEscape(vtlToString(args[0]))
}

func vtlHttpDecodeUrl(args []any) any {
	if len(args) == 0 {
		return ""
	}
	decoded, err := url.QueryUnescape(vtlToString(args[0]))
	if err != nil {
		return vtlToString(args[0])
	}
	return decoded
}

func vtlHttpCopyHeaders(args []any) any {
	if len(args) == 0 {
		return map[string]any{}
	}
	headers, ok := args[0].(map[string]any)
	if !ok {
		return map[string]any{}
	}
	out := make(map[string]any, len(headers))
	for k, v := range headers {
		out[k] = v
	}
	return out
}

// ─── selectionSetGraphQL helper ───────────────────────────────────────────────

// selectionSetToGraphQL serialises an AST selection set to a GraphQL-syntax
// string of the form "{ field1 field2 { subField } ... }".
//
// This is used to populate $ctx.info.selectionSetGraphQL in both VTL and JS.
func selectionSetToGraphQL(sel ast.SelectionSet) string {
	if len(sel) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("{ ")
	writeSelectionSet(&sb, sel)
	sb.WriteString("}")
	return sb.String()
}

func writeSelectionSet(sb *strings.Builder, sel ast.SelectionSet) {
	for _, s := range sel {
		switch v := s.(type) {
		case *ast.Field:
			sb.WriteString(v.Name)
			if len(v.SelectionSet) > 0 {
				sb.WriteString(" { ")
				writeSelectionSet(sb, v.SelectionSet)
				sb.WriteString("} ")
			} else {
				sb.WriteString(" ")
			}
		case *ast.InlineFragment:
			sb.WriteString("... on ")
			sb.WriteString(v.TypeCondition)
			sb.WriteString(" { ")
			writeSelectionSet(sb, v.SelectionSet)
			sb.WriteString("} ")
		case *ast.FragmentSpread:
			sb.WriteString("...")
			sb.WriteString(v.Name)
			sb.WriteString(" ")
		}
	}
}
