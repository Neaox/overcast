//go:build dev

package main

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// The type-spelling table: one IR value plus the member's modeled shape, as
// Java source.
//
// This is the whole of what the java-sdk backend knows about writing typed
// Java, and both writers go through it — emit_java.go's builder chains and
// `-explain -lang java` (explain_typed.go), which share javaRequestLines. There
// is no second description of a call anywhere.
//
//	modeled member          value              emitted
//	---------------------   ----------------   ------------------------------
//	string                  "blue"             "blue"
//	integer                 30                 30
//	long                    30                 30L
//	boolean                 false              false
//	enum Color              "blue"             Color.fromValue("blue")
//	list<QueueAttributeName> ["All"]           List.of(QueueAttributeName.fromValue("All"))
//	map<string,string>      {"a":"b"}          Map.of("a", "b")
//	map<QueueAttributeName,string> {"a":"b"}   Map.of(QueueAttributeName.fromValue("a"), "b")
//	list<structure Tag>     [{"Key":"k"}]      List.of(Tag.builder().key("k").build())
//	string                  {"$ref":"q"}       b.string("M", Values.ref("q"))
//	enum Color              {"$ref":"c"}       Color.fromValue(b.string("M", Values.ref("c")))
//
// # Why the model is the authority here, and the Go emitter's SDK is not
//
// docs/plans/compat-coverage-modelgen.md §3.2 settles it: a typed backend
// resolves its SDK's field types at emit time *wherever the SDK's nullability
// is not derivable from the model*, and for the AWS SDK for Java v2 it is —
// every scalar is boxed, so a builder setter takes the value whatever the
// member's optionality and a boxed 0 really is serialized. That is measured
// rather than assumed: JavaSdkWireFactsTest in the suite sends
// ReceiveMessage's VisibilityTimeout as 0 through a real client and asserts the
// member is on the wire. The consequence is that this file has no counterpart
// to emit_go_spell.go's zero-value refusal, and needs no SDK lookup at
// generation time.
//
// What the model cannot say is whether the *pinned* SDK is new enough to have
// the operation at all. That axis is answered by the suite's own `mvn package`,
// which is a compile error rather than a wrong request — see emit_java.go.

// javaSpeller renders one service's values against that service's modeled
// shapes, recording the model classes it named so the emitted file's import
// block lists exactly what it turned out to need. That makes an instance
// single-use: refusal detection runs over a throwaway one, because a group that
// is refused must not leave its import behind.
type javaSpeller struct {
	model *serviceModel
	// modelTypes is the set of `…​.model.<Name>` classes some spelled value
	// named. An emitted file imports each of them, and nothing else.
	modelTypes map[string]bool
}

func newJavaSpeller(model *serviceModel) *javaSpeller {
	return &javaSpeller{model: model, modelTypes: map[string]bool{}}
}

// javaScalarBinders maps a Smithy scalar shape type to the Binder accessor that
// converts a deferred expression into the Java box the SDK gives that member.
// The AWS SDK for Java v2 boxes every scalar, so the list is the box set.
var javaScalarBinders = map[string]string{
	"string":     "string",
	"boolean":    "bool",
	"byte":       "byteValue",
	"short":      "shortValue",
	"integer":    "integer",
	"intEnum":    "integer",
	"long":       "longValue",
	"float":      "floatValue",
	"double":     "doubleValue",
	"bigInteger": "",
	"bigDecimal": "",
}

// javaUnsupportedKinds are the modeled member kinds no value in the IR's
// grammar can carry. Timestamps, blobs and documents have no portable literal
// and are already refused upstream (compat/model/README.md § Recipes), so this
// is a backstop rather than a live path; a union has no Java literal either.
var javaUnsupportedKinds = map[string]bool{
	"timestamp": true,
	"blob":      true,
	"document":  true,
	"union":     true,
}

// memberTarget resolves the shape a call's input member targets, refusing a
// member the model does not give the operation. An operation with a unit input
// has no members at all, which is the same refusal with a different sentence.
func (sp *javaSpeller) memberTarget(op, member string) (string, error) {
	input := sp.model.InputShape(op)
	if input == "" {
		return "", fmt.Errorf("the model gives %s no input, so it has no member %q", op, member)
	}
	target, ok := sp.model.MemberTarget(input, member)
	if !ok {
		return "", fmt.Errorf("the model gives %s no member %q (its input is %s)", op, member, bareShapeName(input))
	}
	return target, nil
}

// value renders one IR value as a Java expression of the member's modeled type.
//
// member is the modeled member name the value belongs to, which a deferred
// expression carries into its failure message.
func (sp *javaSpeller) value(target string, v any, member string) (string, error) {
	kind := sp.model.Kind(target)
	if javaUnsupportedKinds[kind] {
		return "", fmt.Errorf("the java-sdk emitter has no Java value expression for a %s member (%s)", kind, bareShapeName(target))
	}
	if _, _, isExpr := exprOf(v); isExpr {
		return sp.expr(target, kind, v, member)
	}
	if v == nil {
		// The AWS SDK for Java v2 uses null for "unset", so there is no
		// spelling that sends an explicit JSON null; a scenario asking for one
		// would silently omit the member instead.
		return "", fmt.Errorf("a Java builder setter cannot send an explicit null: null means unset")
	}
	switch kind {
	case "string", "boolean", "integer", "float", "enum":
		return sp.scalar(target, kind, v)
	case "list":
		return sp.list(target, v, member)
	case "map":
		return sp.mapping(target, v, member)
	case "structure":
		return sp.structure(target, v, member)
	}
	return "", fmt.Errorf("no Java literal builds a %s member (%s)", kind, bareShapeName(target))
}

// scalar renders a literal into a scalar member, converting through the enum's
// own fromValue where the model says the member is an enum.
func (sp *javaSpeller) scalar(target, kind string, v any) (string, error) {
	if kind == "enum" {
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("an enum member wants a string, got %s", javaValueKind(v))
		}
		return sp.enumOf(target, javaQuote(s)), nil
	}
	return javaLiteral(sp.shapeType(target), kind, v)
}

// enumOf spells one enum value and records the class the file must import.
//
// `fromValue` rather than the constant: the constant's Java identifier is the
// SCREAMING_SNAKE of the wire value under a naming rule this generator would
// have to reproduce, while `fromValue` takes the wire value the model already
// carries. The cost is that a value the *pinned* SDK does not know resolves to
// UNKNOWN_TO_SDK_VERSION, whose toString is null — a loud wrong request rather
// than a compile error. That is the pin's job, not this table's: see
// compat/suites/java-sdk/AGENTS.md § Generated groups.
func (sp *javaSpeller) enumOf(target, argument string) string {
	class := javaClassOf(target)
	sp.modelTypes[class] = true
	return class + ".fromValue(" + argument + ")"
}

func (sp *javaSpeller) list(target string, v any, member string) (string, error) {
	items, ok := v.([]any)
	if !ok {
		return "", fmt.Errorf("a list member wants a JSON array, got %s", javaValueKind(v))
	}
	elem := sp.model.Shapes[target].Member
	rendered := make([]string, 0, len(items))
	for _, item := range items {
		out, err := sp.value(elem, item, member)
		if err != nil {
			return "", err
		}
		rendered = append(rendered, out)
	}
	return "List.of(" + strings.Join(rendered, ", ") + ")", nil
}

func (sp *javaSpeller) mapping(target string, v any, member string) (string, error) {
	entries, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("a map member wants a JSON object, got %s", javaValueKind(v))
	}
	shape := sp.model.Shapes[target]
	keyKind := sp.model.Kind(shape.Key)
	if keyKind != "string" && keyKind != "enum" {
		return "", fmt.Errorf("a map member keyed by %s has no IR spelling; the IR's objects have string keys", keyKind)
	}
	pairs := make([][2]string, 0, len(entries))
	for _, k := range sortedKeys(entries) {
		key := javaQuote(k)
		if keyKind == "enum" {
			key = sp.enumOf(shape.Key, key)
		}
		out, err := sp.value(shape.Value, entries[k], member)
		if err != nil {
			return "", err
		}
		pairs = append(pairs, [2]string{key, out})
	}
	return javaMapLiteral(pairs), nil
}

// javaMapLiteral spells a map. Map.of takes at most ten pairs, so a wider one
// goes through Map.ofEntries — which has no limit and is otherwise identical.
func javaMapLiteral(pairs [][2]string) string {
	if len(pairs) <= 10 {
		parts := make([]string, 0, len(pairs)*2)
		for _, pair := range pairs {
			parts = append(parts, pair[0], pair[1])
		}
		return "Map.of(" + strings.Join(parts, ", ") + ")"
	}
	parts := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		parts = append(parts, "Map.entry("+pair[0]+", "+pair[1]+")")
	}
	return "Map.ofEntries(" + strings.Join(parts, ", ") + ")"
}

func (sp *javaSpeller) structure(target string, v any, member string) (string, error) {
	members, ok := v.(map[string]any)
	if !ok {
		return "", fmt.Errorf("a structure member wants a JSON object, got %s", javaValueKind(v))
	}
	class := javaClassOf(target)
	sp.modelTypes[class] = true
	var out strings.Builder
	out.WriteString(class + ".builder()")
	for _, k := range sortedKeys(members) {
		field, ok := sp.model.MemberTarget(target, k)
		if !ok {
			return "", fmt.Errorf("the model gives %s no member %q", class, k)
		}
		rendered, err := sp.value(field, members[k], member)
		if err != nil {
			return "", err
		}
		out.WriteString("." + javaSetter(k) + "(" + rendered + ")")
	}
	out.WriteString(".build()")
	return out.String(), nil
}

// expr renders a deferred value expression into a typed slot.
//
// The expression itself is still the IR's — Values.ref, Values.name and the
// rest, rendered by javaValue — and it still resolves through the run's context
// bag. What changes is where the result lands: a Binder accessor converts it to
// the one Java box this member wants, chosen here from the model, and the enum
// conversion is written in the source rather than discovered at run time.
func (sp *javaSpeller) expr(target, kind string, v any, member string) (string, error) {
	shapeType := sp.shapeType(target)
	if kind == "enum" {
		shapeType = "string"
	}
	accessor, known := javaScalarBinders[shapeType]
	if !known {
		return "", fmt.Errorf("a value expression can only be bound to a scalar member, and this one is a %s", kind)
	}
	if accessor == "" {
		return "", fmt.Errorf("no Binder accessor produces the Java type for a %s member", shapeType)
	}
	rendered, err := javaValue(v)
	if err != nil {
		return "", err
	}
	out := fmt.Sprintf("b.%s(%s, %s)", accessor, javaQuote(member), rendered)
	if kind == "enum" {
		out = sp.enumOf(target, out)
	}
	return out, nil
}

// shapeType is the member's Smithy shape type — the distinction Kind collapses,
// and the one that decides which Java box the SDK gave the member: `integer`
// and `long` are both Kind "integer" and are Integer and Long in Java.
func (sp *javaSpeller) shapeType(target string) string {
	if kind, ok := preludeJavaTypes[target]; ok {
		return kind
	}
	shape, ok := sp.model.Shapes[target]
	if !ok {
		return "unknown"
	}
	if shape.Type == "set" {
		return "list"
	}
	return shape.Type
}

// preludeJavaTypes names the Smithy prelude targets, which the snapshot never
// emits as shapes of their own. It mirrors model.go's preludeKinds but keeps the
// width the Java box depends on.
var preludeJavaTypes = map[string]string{
	"smithy.api#String":           "string",
	"smithy.api#Blob":             "blob",
	"smithy.api#Boolean":          "boolean",
	"smithy.api#PrimitiveBoolean": "boolean",
	"smithy.api#Byte":             "byte",
	"smithy.api#Short":            "short",
	"smithy.api#Integer":          "integer",
	"smithy.api#PrimitiveInteger": "integer",
	"smithy.api#Long":             "long",
	"smithy.api#PrimitiveLong":    "long",
	"smithy.api#Float":            "float",
	"smithy.api#Double":           "double",
	"smithy.api#BigInteger":       "bigInteger",
	"smithy.api#BigDecimal":       "bigDecimal",
	"smithy.api#Timestamp":        "timestamp",
	"smithy.api#Document":         "document",
	"smithy.api#Unit":             "unit",
}

// modelImports is the sorted set of `…​.model.<Name>` classes the spelled values
// named, for the emitted file's import block.
func (sp *javaSpeller) modelImports() []string {
	out := make([]string, 0, len(sp.modelTypes))
	for name := range sp.modelTypes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Literals
// ---------------------------------------------------------------------------

// javaLiteral renders an IR scalar as a Java constant of the member's own type.
// A mismatch is an error rather than a coercion: "30" is not 30 anywhere else in
// the IR, and accepting it here would let a wrong literal through.
func javaLiteral(shapeType, kind string, v any) (string, error) {
	switch kind {
	case "string":
		s, ok := v.(string)
		if !ok {
			return "", fmt.Errorf("a string member wants a string, got %s", javaValueKind(v))
		}
		return javaQuote(s), nil
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return "", fmt.Errorf("a boolean member wants a boolean, got %s", javaValueKind(v))
		}
		return strconv.FormatBool(b), nil
	case "integer":
		n, ok := javaNumberOf(v)
		if !ok {
			return "", fmt.Errorf("a %s member wants a number, got %s", shapeType, javaValueKind(v))
		}
		if n != math.Trunc(n) {
			return "", fmt.Errorf("a %s member wants a whole number, got %v", shapeType, n)
		}
		min, max, known := javaIntRange(shapeType)
		if !known {
			return "", fmt.Errorf("no Java literal builds a %s member", shapeType)
		}
		if n < min || n > max {
			return "", fmt.Errorf("%v is out of range for %s", n, shapeType)
		}
		if shapeType == "long" {
			return strconv.FormatInt(int64(n), 10) + "L", nil
		}
		return strconv.FormatInt(int64(n), 10), nil
	case "float":
		n, ok := javaNumberOf(v)
		if !ok {
			return "", fmt.Errorf("a %s member wants a number, got %s", shapeType, javaValueKind(v))
		}
		rendered := strconv.FormatFloat(n, 'g', -1, 64)
		if !strings.ContainsAny(rendered, ".eE") {
			rendered += ".0"
		}
		if shapeType == "float" {
			return rendered + "f", nil
		}
		return rendered, nil
	}
	return "", fmt.Errorf("no Java literal builds a %s member", kind)
}

func javaIntRange(shapeType string) (min, max float64, known bool) {
	switch shapeType {
	case "byte":
		return math.MinInt8, math.MaxInt8, true
	case "short":
		return math.MinInt16, math.MaxInt16, true
	case "integer", "intEnum":
		return math.MinInt32, math.MaxInt32, true
	case "long":
		return math.MinInt64, math.MaxInt64, true
	}
	return 0, 0, false
}

func javaNumberOf(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int:
		return float64(n), true
	}
	return 0, false
}

// javaQuote renders a Go string as a Java string literal. Java accepts \uXXXX
// but not Go's \xNN, so control characters go through the unicode form and
// everything printable stays as itself — the emitted file is UTF-8.
func javaQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == utf8.RuneError {
				fmt.Fprintf(&b, `\u%04x`, r)
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// javaClassOf is the Java class the AWS SDK for Java v2 generates for a modeled
// shape: its bare name run through the code generator's pascalCase, which is
// what turns AWSOrganizationsNotInUse into AwsOrganizationsNotInUse.
func javaClassOf(shapeID string) string { return javaPascal(bareShapeName(shapeID)) }

// javaValueKind names an IR value's JSON type for an error message.
func javaValueKind(v any) string {
	switch value := v.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case bool:
		return "a boolean"
	case json.Number, float64, int:
		return "a number"
	case []any:
		return "a list"
	case map[string]any:
		if len(value) == 0 {
			return "an object"
		}
		return "an object (" + strings.Join(sortedKeys(value), ", ") + ")"
	}
	return fmt.Sprintf("a %T", v)
}
