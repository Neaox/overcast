package cloudformation

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// parseTemplate parses a JSON or YAML CloudFormation template.
// Both formats are fully supported. YAML short-form intrinsic-function tags
// (e.g. !Ref, !Sub, !GetAtt, !Join) are normalised to their canonical long-form
// map equivalents so the rest of the resolution code works identically for
// both input formats.
func parseTemplate(body string) (*Template, error) {
	body = strings.TrimSpace(body)
	if strings.HasPrefix(body, "{") {
		return parseTemplateJSON(body)
	}
	return parseTemplateYAML(body)
}

func parseTemplateJSON(body string) (*Template, error) {
	var tmpl Template
	if err := json.Unmarshal([]byte(body), &tmpl); err != nil {
		return nil, fmt.Errorf("cfn: parse template (json): %w", err)
	}
	if tmpl.Resources == nil {
		return nil, fmt.Errorf("cfn: template has no Resources section")
	}
	return &tmpl, nil
}

func parseTemplateYAML(body string) (*Template, error) {
	// Decode into a yaml.Node tree so we can handle CloudFormation-specific
	// tag aliases (e.g. !Ref, !Sub, !GetAtt) before unmarshalling into Template.
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(body), &root); err != nil {
		return nil, fmt.Errorf("cfn: parse template (yaml): %w", err)
	}
	if root.Kind == 0 {
		return nil, fmt.Errorf("cfn: empty YAML template")
	}

	// Convert the node tree to a plain Go value, translating CFN tags along the way.
	raw := yamlNodeToValue(&root)

	// Round-trip through JSON to populate the typed Template struct, reusing the
	// existing JSON struct tags.
	jsonBytes, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("cfn: parse template (yaml→json): %w", err)
	}
	var tmpl Template
	if err := json.Unmarshal(jsonBytes, &tmpl); err != nil {
		return nil, fmt.Errorf("cfn: parse template (yaml→json unmarshal): %w", err)
	}
	if tmpl.Resources == nil {
		return nil, fmt.Errorf("cfn: template has no Resources section")
	}
	return &tmpl, nil
}

// yamlNodeToValue converts a *yaml.Node tree to a plain Go value
// (map[string]any, []any, string, bool, float64, nil).
//
// CloudFormation short-form tag aliases are expanded to their canonical
// long-form map equivalents:
//
//	!Ref Foo            → {"Ref": "Foo"}
//	!Sub "x-${Foo}"    → {"Fn::Sub": "x-${Foo}"}
//	!GetAtt Res.Attr   → {"Fn::GetAtt": ["Res", "Attr"]}
//	!Join [-, [a, b]]  → {"Fn::Join": ["-", ["a","b"]]}
//	!Select [0, [a]]   → {"Fn::Select": [0, ["a"]]}
//	!Split [,, str]    → {"Fn::Split": [",", "str"]}
//	!If [c, a, b]      → {"Fn::If": ["c","a","b"]}
//	!Base64 v          → {"Fn::Base64": "v"}
//	!GetAZs ""         → {"Fn::GetAZs": ""}
//	!ImportValue v     → {"Fn::ImportValue": "v"}
//	!Condition c       → {"Condition": "c"}
func yamlNodeToValue(n *yaml.Node) any {
	if n == nil {
		return nil
	}

	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil
		}
		return yamlNodeToValue(n.Content[0])

	case yaml.MappingNode:
		m := make(map[string]any, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, _ := yamlNodeToValue(n.Content[i]).(string)
			m[key] = yamlNodeToValue(n.Content[i+1])
		}
		return m

	case yaml.SequenceNode:
		// Sequence nodes can carry a CFN tag when written as:
		//   Key: !Join
		//     - "-"
		//     - [a, b]
		s := make([]any, len(n.Content))
		for i, child := range n.Content {
			s[i] = yamlNodeToValue(child)
		}
		switch n.Tag {
		case "!Join":
			return map[string]any{"Fn::Join": s}
		case "!Select":
			return map[string]any{"Fn::Select": s}
		case "!Split":
			return map[string]any{"Fn::Split": s}
		case "!If":
			return map[string]any{"Fn::If": s}
		case "!Sub":
			// !Sub [template, vars] — array form
			return map[string]any{"Fn::Sub": s}
		case "!GetAtt":
			// !GetAtt [Logical, Attr] — array form
			return map[string]any{"Fn::GetAtt": s}
		}
		return s

	case yaml.ScalarNode:
		tag := n.Tag
		val := n.Value
		switch tag {
		case "!Ref":
			return map[string]any{"Ref": val}
		case "!Sub":
			return map[string]any{"Fn::Sub": val}
		case "!Base64":
			return map[string]any{"Fn::Base64": val}
		case "!GetAZs":
			return map[string]any{"Fn::GetAZs": val}
		case "!ImportValue":
			return map[string]any{"Fn::ImportValue": val}
		case "!Condition":
			return map[string]any{"Condition": val}
		case "!GetAtt":
			// Scalar form: "LogicalId.Attribute"
			parts := strings.SplitN(val, ".", 2)
			if len(parts) == 2 {
				return map[string]any{"Fn::GetAtt": []any{parts[0], parts[1]}}
			}
			return map[string]any{"Fn::GetAtt": val}
		default:
			// Decode to native Go type (bool, int, float64, string, nil).
			var v any
			if err := n.Decode(&v); err != nil {
				return val
			}
			return v
		}

	case yaml.AliasNode:
		return yamlNodeToValue(n.Alias)
	}

	return nil
}

// resolveIntrinsics recursively resolves CloudFormation intrinsic functions in a value.
// Supported intrinsics:
//   - Ref (parameters, pseudo-parameters, logical resource IDs)
//   - Fn::Sub (simple variable substitution)
//   - Fn::Join
//   - Fn::Select
//   - Fn::GetAtt (returns PhysicalResourceId for now)
//   - Fn::If (evaluates conditions)
//   - Fn::Split
//   - Fn::GetAZs
//   - Fn::ImportValue (cross-stack references)
func resolveIntrinsics(v any, ctx *resolveContext) any {
	switch val := v.(type) {
	case map[string]any:
		return resolveMap(val, ctx)
	case []any:
		resolved := make([]any, len(val))
		for i, item := range val {
			resolved[i] = resolveIntrinsics(item, ctx)
		}
		return resolved
	default:
		return v
	}
}

func resolveMap(m map[string]any, ctx *resolveContext) any {
	// Check for intrinsic functions (single-key maps with known keys).
	if len(m) == 1 {
		if ref, ok := m["Ref"]; ok {
			return resolveRef(ref, ctx)
		}
		if sub, ok := m["Fn::Sub"]; ok {
			return resolveSub(sub, ctx)
		}
		if join, ok := m["Fn::Join"]; ok {
			return resolveJoin(join, ctx)
		}
		if sel, ok := m["Fn::Select"]; ok {
			return resolveSelect(sel, ctx)
		}
		if getAtt, ok := m["Fn::GetAtt"]; ok {
			return resolveGetAtt(getAtt, ctx)
		}
		if ifCond, ok := m["Fn::If"]; ok {
			return resolveIf(ifCond, ctx)
		}
		if split, ok := m["Fn::Split"]; ok {
			return resolveSplit(split, ctx)
		}
		if _, ok := m["Fn::GetAZs"]; ok {
			return resolveGetAZs(ctx)
		}
		if impVal, ok := m["Fn::ImportValue"]; ok {
			return resolveImportValue(impVal, ctx)
		}
	}

	// Not an intrinsic — recurse into all values.
	result := make(map[string]any, len(m))
	for k, val := range m {
		result[k] = resolveIntrinsics(val, ctx)
	}
	return result
}

// resolveContext holds the resolution state.
type resolveContext struct {
	Region     string
	AccountID  string
	StackName  string
	StackID    string
	StackTags  []Tag
	Params     map[string]string            // parameter name → value
	Resources  map[string]string            // logical ID → physical ID
	Conditions map[string]bool              // condition name → evaluated value
	Mappings   map[string]any               // raw mappings from template
	Attributes map[string]map[string]string // logical ID → attributes
	Exports    map[string]string            // export name → value (cross-stack)
}

// resolveRef resolves Ref to a parameter value, pseudo-parameter, or logical resource ID.
func resolveRef(ref any, ctx *resolveContext) any {
	name, ok := ref.(string)
	if !ok {
		return ref
	}

	// Pseudo-parameters
	switch name {
	case "AWS::Region":
		return ctx.Region
	case "AWS::AccountId":
		return ctx.AccountID
	case "AWS::StackName":
		return ctx.StackName
	case "AWS::StackId":
		return ctx.StackID
	case "AWS::NoValue":
		return ""
	case "AWS::URLSuffix":
		return "amazonaws.com"
	case "AWS::Partition":
		return "aws"
	case "AWS::NotificationARNs":
		return []any{}
	}

	// Template parameters
	if v, ok := ctx.Params[name]; ok {
		return v
	}

	// Logical resource → Ref value.
	// Handlers may set a "Ref" attribute override when the physical ID
	// (needed for Delete) differs from what Ref should return (e.g. API
	// Gateway resources where the physical ID is "restApiId/resourceId"
	// but Ref should return just the resource ID).
	if attrMap, ok := ctx.Attributes[name]; ok {
		if refOverride, ok := attrMap["Ref"]; ok {
			return refOverride
		}
	}
	if v, ok := ctx.Resources[name]; ok {
		return v
	}

	return name
}

// resolveSub handles Fn::Sub — either a string or [string, map].
func resolveSub(sub any, ctx *resolveContext) any {
	var tmplStr string
	extraVars := map[string]string{}

	switch val := sub.(type) {
	case string:
		tmplStr = val
	case []any:
		if len(val) >= 1 {
			if s, ok := val[0].(string); ok {
				tmplStr = s
			}
		}
		if len(val) >= 2 {
			if m, ok := val[1].(map[string]any); ok {
				for k, v := range m {
					resolved := resolveIntrinsics(v, ctx)
					extraVars[k] = fmt.Sprintf("%v", resolved)
				}
			}
		}
	default:
		return sub
	}

	// Replace ${VarName} patterns.
	result := tmplStr
	// First apply extra vars, then try params/resources/pseudo-params.
	for k, v := range extraVars {
		result = strings.ReplaceAll(result, "${"+k+"}", v)
	}

	// Replace remaining ${...} with known values.
	for {
		start := strings.Index(result, "${")
		if start < 0 {
			break
		}
		end := strings.Index(result[start:], "}")
		if end < 0 {
			break
		}
		varName := result[start+2 : start+end]
		resolved := resolveRef(varName, ctx)
		result = result[:start] + fmt.Sprintf("%v", resolved) + result[start+end+1:]
	}

	return result
}

// resolveJoin handles Fn::Join: [delimiter, [values]].
func resolveJoin(join any, ctx *resolveContext) any {
	arr, ok := join.([]any)
	if !ok || len(arr) != 2 {
		return join
	}
	delimiter, _ := arr[0].(string)
	values, ok := arr[1].([]any)
	if !ok {
		return join
	}
	parts := make([]string, 0, len(values))
	for _, v := range values {
		resolved := resolveIntrinsics(v, ctx)
		parts = append(parts, fmt.Sprintf("%v", resolved))
	}
	return strings.Join(parts, delimiter)
}

// resolveSelect handles Fn::Select: [index, [values]].
func resolveSelect(sel any, ctx *resolveContext) any {
	arr, ok := sel.([]any)
	if !ok || len(arr) != 2 {
		return sel
	}
	idx := 0
	switch v := arr[0].(type) {
	case float64:
		idx = int(v)
	case string:
		// Try parsing
		parsed, err := strconv.Atoi(v)
		if err != nil {
			return ""
		}
		idx = parsed
	}
	values, ok := arr[1].([]any)
	if !ok || idx >= len(values) {
		return ""
	}
	return resolveIntrinsics(values[idx], ctx)
}

// resolveGetAtt handles Fn::GetAtt: [logicalId, attribute] or "logicalId.attribute".
func resolveGetAtt(getAtt any, ctx *resolveContext) any {
	var logicalID, attr string
	switch val := getAtt.(type) {
	case []any:
		if len(val) >= 2 {
			logicalID, _ = val[0].(string)
			attr, _ = val[1].(string)
		}
	case string:
		parts := strings.SplitN(val, ".", 2)
		if len(parts) == 2 {
			logicalID = parts[0]
			attr = parts[1]
		}
	}
	if logicalID == "" {
		return getAtt
	}

	// Look up real attribute value first.
	if attrMap, ok := ctx.Attributes[logicalID]; ok {
		if val, ok := attrMap[attr]; ok {
			return val
		}
	}
	// Fallback: return physical ID (which is often an ARN).
	if physID, ok := ctx.Resources[logicalID]; ok {
		return physID
	}
	return fmt.Sprintf("%s.%s", logicalID, attr)
}

// resolveIf handles Fn::If: [conditionName, trueValue, falseValue].
func resolveIf(ifCond any, ctx *resolveContext) any {
	arr, ok := ifCond.([]any)
	if !ok || len(arr) != 3 {
		return ifCond
	}
	condName, _ := arr[0].(string)
	if ctx.Conditions[condName] {
		return resolveIntrinsics(arr[1], ctx)
	}
	return resolveIntrinsics(arr[2], ctx)
}

// resolveSplit handles Fn::Split: [delimiter, string].
func resolveSplit(split any, ctx *resolveContext) any {
	arr, ok := split.([]any)
	if !ok || len(arr) != 2 {
		return split
	}
	delimiter, _ := arr[0].(string)
	source := resolveIntrinsics(arr[1], ctx)
	sourceStr, _ := source.(string)
	parts := strings.Split(sourceStr, delimiter)
	result := make([]any, len(parts))
	for i, p := range parts {
		result[i] = p
	}
	return result
}

// resolveGetAZs returns AZs for the region.
func resolveGetAZs(ctx *resolveContext) any {
	return []any{
		ctx.Region + "a",
		ctx.Region + "b",
		ctx.Region + "c",
	}
}

// resolveImportValue handles Fn::ImportValue: exportName.
// Looks up the export from the Exports map populated by the provisioner.
func resolveImportValue(impVal any, ctx *resolveContext) any {
	// The export name may itself be an intrinsic (e.g. Fn::Sub).
	resolved := resolveIntrinsics(impVal, ctx)
	name, ok := resolved.(string)
	if !ok {
		return fmt.Sprintf("%v", resolved)
	}
	if ctx.Exports != nil {
		if val, found := ctx.Exports[name]; found {
			return val
		}
	}
	// Export not found — return the name as-is (best-effort).
	return name
}

// resolveAllProperties resolves all intrinsics in a resource's properties.
func resolveAllProperties(props map[string]any, ctx *resolveContext) map[string]any {
	if props == nil {
		return nil
	}
	resolved := resolveIntrinsics(props, ctx)
	if m, ok := resolved.(map[string]any); ok {
		return m
	}
	return props
}
