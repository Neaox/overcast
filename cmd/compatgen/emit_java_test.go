//go:build dev

package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/awsmodel"
)

// The Java emitter, over the same hermetic fixture the rest of the generator's
// tests use (testdata/shapes/widgets.json + testdata/recipes/widgets.json).
// Nothing here reads the committed corpus, opens a network connection or
// compiles the emitted source; the emitted corpus is proved to compile by the
// java-sdk suite's own `mvn package`.
//
// Unlike the Go emitter's tests these need no stand-in SDK: the Java spelling
// is derived from the pinned model, which the fixture already carries. That is
// the whole content of the plan's binding decision for this backend, and it is
// what makes these tests hermetic without a second module.

// javaGoldenPath is the emitted source for the fixture service. It carries a
// .golden suffix so javac never sees it: it names an SDK package for a service
// that does not exist.
const javaGoldenPath = "testdata/golden/ScenariosWidgetsGen.java.golden"

// javaFixtureSDKID is the fixture service's SDK id, which the speller needs in
// order to write a colliding model class out in full.
const javaFixtureSDKID = "Widgets"

// withJavaTypeShapes adds one operation to the fixture model carrying a member
// of every modeled type the widgets service does not otherwise have, so the
// spelling table and its refusals can be read back row by row.
//
// It is added to the loaded model rather than to testdata/shapes/widgets.json
// because the recipe does not name the operation: putting it in the file would
// widen the probe group and rewrite both emitters' goldens for members no
// scenario sends. Nothing here reaches generation — the caller has already
// generated, or is only spelling values.
func withJavaTypeShapes(model *serviceModel) *serviceModel {
	member := func(target string) awsmodel.SnapshotMember {
		return awsmodel.SnapshotMember{Target: target}
	}
	add := func(name string, shape awsmodel.SnapshotShape) {
		model.Shapes[name] = shape
	}
	add("TuneWidget", awsmodel.SnapshotShape{Type: "operation", Input: "TuneWidgetRequest"})
	// An operation the model gives no input at all: a scenario naming a member
	// of it has nowhere to put one.
	add("PulseWidget", awsmodel.SnapshotShape{Type: "operation"})
	add("TuneWidgetRequest", awsmodel.SnapshotShape{Type: "structure", Members: map[string]awsmodel.SnapshotMember{
		"Ticks":     member("smithy.api#Long"),
		"Ratio":     member("smithy.api#Float"),
		"Precision": member("smithy.api#Double"),
		"Enabled":   member("smithy.api#Boolean"),
		"Nudge":     member("smithy.api#Byte"),
		"Offset":    member("smithy.api#Short"),
		"Huge":      member("smithy.api#BigInteger"),
		"Exact":     member("smithy.api#BigDecimal"),
		"Spec":      member("TuneSpec"),
		"Palette":   member("ColorMap"),
		"Codes":     member("ColorList"),
		"Layers":    member("ColorListList"),
		"Weights":   member("WeightMap"),
		"Marker":    member("Check"),
	}})
	add("TuneSpec", awsmodel.SnapshotShape{Type: "structure", Members: map[string]awsmodel.SnapshotMember{
		"Label": member("smithy.api#String"),
		"Shade": member("Color"),
		"Tints": member("ColorList"),
	}})
	add("ColorMap", awsmodel.SnapshotShape{Type: "map", Key: "Color", Value: "smithy.api#String"})
	add("ColorList", awsmodel.SnapshotShape{Type: "list", Member: "Color"})
	add("ColorListList", awsmodel.SnapshotShape{Type: "list", Member: "ColorList"})
	add("WeightMap", awsmodel.SnapshotShape{Type: "map", Key: "smithy.api#Integer", Value: "smithy.api#String"})
	// A shape whose SDK class is named after something the emitted file already
	// binds. IAM, Resource Groups and Greengrass all model a shape named Group.
	add("Check", awsmodel.SnapshotShape{Type: "structure", Members: map[string]awsmodel.SnapshotMember{
		"Key": member("smithy.api#String"),
	}})
	return model
}

func TestEmitJava_matchesTheGoldenSource(t *testing.T) {
	// Given: the fixture service, generated.
	_, gen := generateFixture(t)

	// When: it is emitted as Java.
	emission, err := emitJava(gen)
	if err != nil {
		t.Fatalf("emitJava: %v", err)
	}

	// Then: byte for byte what is committed.
	want := "compat/suites/java-sdk/src/main/java/io/overcast/compat/groups/ScenariosWidgetsGen.java"
	if emission.Path != want {
		t.Errorf("emitted path = %s, want %s", emission.Path, want)
	}
	assertGolden(t, javaGoldenPath, emission.Contents)
}

func TestEmitJava_isDeterministic(t *testing.T) {
	_, gen := generateFixture(t)

	first, err := emitJava(gen)
	if err != nil {
		t.Fatalf("emitJava: %v", err)
	}
	second, err := emitJava(gen)
	if err != nil {
		t.Fatalf("emitJava: %v", err)
	}
	if string(first.Contents) != string(second.Contents) {
		t.Error("two emissions of one scenario differ; the byte-identical regeneration gate would fail at random")
	}
	if !strings.HasSuffix(string(first.Contents), "}\n") {
		t.Error("the emitted file does not end with exactly one trailing newline after the class")
	}
	if strings.Contains(string(first.Contents), "\r") {
		t.Error("the emitted file carries CRLF line endings")
	}
}

// TestEmitJava_emitsEveryGroupAndTest keeps the emitted registrations in step
// with the scenario. A test the emitter silently dropped would report as a hard
// failure in the suite (the generated-no-backend rule), which is loud but late.
func TestEmitJava_emitsEveryGroupAndTest(t *testing.T) {
	_, gen := generateFixture(t)
	emission, err := emitJava(gen)
	if err != nil {
		t.Fatalf("emitJava: %v", err)
	}
	source := string(emission.Contents)
	for _, g := range gen.scenario.Groups {
		for _, tc := range g.Tests {
			key := `"` + g.Name + ":" + tc.Name + `"`
			if !strings.Contains(source, key) {
				t.Errorf("no impl registered under %s", key)
			}
			if !strings.Contains(source, "private void "+javaNameTestMethod(g.Name, tc.Name)+"(TestContext t)") {
				t.Errorf("no method emitted for %s/%s", g.Name, tc.Name)
			}
		}
		// Every group registers both hooks, even the probe group whose two lists
		// are empty: an empty phase is a no-op, not a missing one.
		for _, method := range []string{javaNameSetupMethod(g.Name), javaNameTeardownMethod(g.Name)} {
			if !strings.Contains(source, "private void "+method+"(TestContext t)") {
				t.Errorf("no %s emitted for %s", method, g.Name)
			}
		}
	}
	if !strings.Contains(source, "// Code generated by cmd/compatgen; DO NOT EDIT.") {
		t.Error("the generated-file marker is missing")
	}
}

// TestEmitJava_refusesWhatItCannotSpell pins every refusal this backend
// introduces, and what each costs: the group leaves the java-sdk column rather
// than being emitted as a guess or dropped silently.
//
// None arises from a committed scenario — the recipes and the upstream refusals
// see to that — so each is constructed against the fixture model.
func TestEmitJava_refusesWhatItCannotSpell(t *testing.T) {
	for _, tc := range []struct {
		name       string
		op         string
		params     map[string]any
		wantMember string
		wantDetail string
	}{
		{
			// The fixture models ListWidgets with an optional CreatedAfter
			// targeting smithy.api#Timestamp. Blobs, documents and unions reach
			// the same branch; a timestamp is the one the fixture carries.
			name:       "a member whose modeled kind has no IR literal",
			op:         "ListWidgets",
			params:     map[string]any{"CreatedAfter": "2026-09-06T00:00:00Z"},
			wantMember: "CreatedAfter",
			wantDetail: "no Java value expression for a timestamp member",
		},
		{
			// A recipe naming a member the operation's input does not declare.
			// The binder refuses one upstream; this is the backstop, and it is
			// also what an operation with a unit input looks like.
			name:       "a member the model does not give the operation",
			op:         "RotateWidget",
			params:     map[string]any{"Nonexistent": "x", "WidgetId": "w-1"},
			wantMember: "Nonexistent",
			wantDetail: "no member \"Nonexistent\"",
		},
		{
			// "30" is not 30 anywhere else in the IR, and accepting it here
			// would let a wrong literal through into a typed slot.
			name:       "a literal of the wrong JSON type for the member",
			op:         "RotateWidget",
			params:     map[string]any{"Angle": "45", "WidgetId": "w-1"},
			wantMember: "Angle",
			wantDetail: "wants a number, got a string",
		},
		{
			// A deferred expression resolves into one scalar slot. A whole list
			// from a $ref has no typed slot to land in, and inventing one would
			// mean converting a document to a List<String> at run time —
			// reflection, by another name.
			name:       "an expression bound to a composite member",
			op:         "UntagWidget",
			params:     map[string]any{"TagKeys": map[string]any{"$ref": "tags.keys"}},
			wantMember: "TagKeys",
			wantDetail: "can only be bound to a scalar member",
		},
		{
			// The AWS SDK for Java v2 spells "unset" as null, so there is no
			// setter argument that sends an explicit JSON null: a scenario
			// asking for one would silently omit the member instead.
			name:       "an explicit null",
			op:         "UpdateWidget",
			params:     map[string]any{"Description": nil, "WidgetId": "w-1"},
			wantMember: "Description",
			wantDetail: "cannot send an explicit null",
		},
		{
			// An operation the model gives no input at all. The scaffolder
			// refuses one upstream; this is what the backstop says about it,
			// and it is a different sentence from "no such member".
			name:       "a member of an operation with a unit input",
			op:         "PulseWidget",
			params:     map[string]any{"WidgetId": "w-1"},
			wantMember: "WidgetId",
			wantDetail: "gives PulseWidget no input",
		},
		{
			name:       "a non-whole number into an integer member",
			op:         "RotateWidget",
			params:     map[string]any{"Angle": json.Number("1.5"), "WidgetId": "w-1"},
			wantMember: "Angle",
			wantDetail: "wants a whole number, got 1.5",
		},
		{
			// 2^31 does not fit an Integer, and the literal is reported as the
			// scenario wrote it rather than as a float64 rounded it.
			name:       "a number out of range for the member's Java type",
			op:         "RotateWidget",
			params:     map[string]any{"Angle": json.Number("2147483648"), "WidgetId": "w-1"},
			wantMember: "Angle",
			wantDetail: "2147483648 is out of range for integer",
		},
		{
			// java.math.BigInteger has no literal; a long standing in for one
			// would silently change a value chosen to be carried exactly.
			name:       "a bigInteger member",
			op:         "TuneWidget",
			params:     map[string]any{"Huge": json.Number("170141183460469231731687303715884105728")},
			wantMember: "Huge",
			wantDetail: "no Java literal builds a bigInteger member",
		},
		{
			name:       "a bigDecimal member",
			op:         "TuneWidget",
			params:     map[string]any{"Exact": json.Number("1.5")},
			wantMember: "Exact",
			wantDetail: "no Java literal builds a bigDecimal member",
		},
		{
			// javac rejects a float literal outside the type's range outright,
			// so it is refused here rather than emitted as 1e+300f.
			name:       "a float literal out of range for the member's Java type",
			op:         "TuneWidget",
			params:     map[string]any{"Ratio": json.Number("1e300")},
			wantMember: "Ratio",
			wantDetail: "1e300 is out of range for float",
		},
		{
			name:       "a literal of the wrong JSON type for an enum member",
			op:         "CreateWidget",
			params:     map[string]any{"Color": json.Number("1"), "Name": "w"},
			wantMember: "Color",
			wantDetail: "an enum member wants a string, got a number",
		},
		{
			// The IR's objects have string keys, so there is no value that
			// could fill a map the model keys by anything else.
			name:       "a map the model does not key by a string",
			op:         "TuneWidget",
			params:     map[string]any{"Weights": map[string]any{"1": "heavy"}},
			wantMember: "Weights",
			wantDetail: "keyed by integer has no IR spelling",
		},
		{
			// One list or map down is as far as the SDK's String form reaches;
			// falling back to fromValue would put "null" on the wire for a
			// value the pinned SDK does not know.
			name:       "an enum nested deeper than the SDK spells with Strings",
			op:         "TuneWidget",
			params:     map[string]any{"Layers": []any{[]any{"blue"}}},
			wantMember: "Layers",
			wantDetail: "no String form for the enum Color nested this deep",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a group whose call carries that member.
			_, gen := generateFixture(t)
			withJavaTypeShapes(gen.model)
			const name = "widgets-gen-refused"
			gen.scenario.Groups = append(gen.scenario.Groups, group{
				Name: name,
				Kind: groupLifecycle,
				Tests: []test{newTest(tc.op, tc.op, call{Op: tc.op, Params: tc.params},
					responseField(checks("$.Widgets", isList())))},
			})

			// When: the service is emitted.
			emission, err := emitJava(gen)
			if err != nil {
				t.Fatalf("emitJava: %v", err)
			}

			// Then: the group is not in the emitted source, it is reported as
			// unable so the registry scopes it away from java-sdk, and the
			// refusal names the member and says why.
			if strings.Contains(string(emission.Contents), name) {
				t.Error("a group the emitter cannot spell was emitted anyway")
			}
			if !emission.Refused[name] {
				t.Fatal("the refused group was not reported as unable")
			}
			if len(emission.Gaps) != 1 {
				t.Fatalf("gaps = %+v, want one", emission.Gaps)
			}
			got := emission.Gaps[0]
			if got.Reason != javaEmitReason+":"+tc.wantMember || got.Operation != tc.op || got.Group != name {
				t.Errorf("gap = %+v", got)
			}
			if !strings.Contains(got.Detail, tc.wantDetail) {
				t.Errorf("gap detail does not say why: %q, want it to mention %q", got.Detail, tc.wantDetail)
			}
		})
	}
}

// TestBuildRegistry_scopesAJavaRefusedGroupAwayFromJavaSdk is the other half of
// a Java refusal, and the reason a refusal is not merely cosmetic: `suites` is
// derived from backend availability, so java-sdk must not be listed against a
// group its emitter did not write a method for — the loader's
// generated-no-backend rule would turn the refusal into a hard failure naming
// the group.
func TestBuildRegistry_scopesAJavaRefusedGroupAwayFromJavaSdk(t *testing.T) {
	_, gen := generateFixture(t)
	units := []*generation{gen}
	backends := []string{"cli", "go-sdk", "java-sdk"}
	refused := gen.scenario.Groups[0].Name

	reg := buildRegistry(units, backends, nil, unableSuites{refused: {"java-sdk": true}})
	for _, g := range reg.Groups {
		want := backends
		if g.Name == refused {
			want = []string{"cli", "go-sdk"}
		}
		if strings.Join(g.Suites, ",") != strings.Join(want, ",") {
			t.Errorf("group %s suites = %v, want %v", g.Name, g.Suites, want)
		}
	}

	// And a group no backend can execute is left out entirely rather than
	// written with an empty `suites`, which the schema refuses.
	none := unableSuites{refused: {"cli": true, "go-sdk": true, "java-sdk": true}}
	for _, g := range buildRegistry(units, backends, nil, none).Groups {
		if g.Name == refused {
			t.Error("a group no suite can execute was still registered")
		}
	}
}

// TestEmitJava_fullyQualifiesAModelClassWhoseNameIsAlreadyBound pins the
// import-collision rule in a whole emitted file rather than in one spelling.
// A service modelling a shape named Group, Call, Check or Values — IAM,
// Resource Groups and Greengrass all model Group — would otherwise emit a
// second import of that simple name beside io.overcast.compat.scenario's, which
// does not compile.
func TestEmitJava_fullyQualifiesAModelClassWhoseNameIsAlreadyBound(t *testing.T) {
	// Given: a group calling an operation whose member targets a shape named
	// after something the file already binds.
	_, gen := generateFixture(t)
	withJavaTypeShapes(gen.model)
	gen.scenario.Groups = append(gen.scenario.Groups, group{
		Name: "widgets-gen-marked",
		Kind: groupLifecycle,
		Tests: []test{newTest("TuneWidget", "TuneWidget",
			call{Op: "TuneWidget", Params: map[string]any{"Marker": map[string]any{"Key": "k"}}},
			responseField(checks("$.Widgets", isList())))},
	})

	// When: the service is emitted.
	emission, err := emitJava(gen)
	if err != nil {
		t.Fatalf("emitJava: %v", err)
	}
	source := string(emission.Contents)

	// Then: the model class is written out in full and never imported, and the
	// scenario class of that name still is.
	if !strings.Contains(source, "software.amazon.awssdk.services.widgets.model.Check.builder()") {
		t.Errorf("the colliding model class was not fully qualified:\n%s", source)
	}
	if strings.Contains(source, "import software.amazon.awssdk.services.widgets.model.Check;") {
		t.Error("the colliding model class was imported, which does not compile beside the scenario one")
	}
	if !strings.Contains(source, "import io.overcast.compat.scenario.Check;") {
		t.Error("the scenario class lost its import to the model class")
	}
}

// TestJavaSpeller_spellsEveryShapeOfMember is the type-spelling table read back,
// one row at a time, against the fixture's modeled shapes. The golden file
// proves what a whole service comes out as; this says which rule produced each
// piece of it, and it is where a new rule is added with its own row.
func TestJavaSpeller_spellsEveryShapeOfMember(t *testing.T) {
	f, _ := generateFixture(t)
	for _, tc := range []struct {
		name   string
		op     string
		member string
		value  string
		want   string
		// wantSetter is the builder setter the value is passed to, which is the
		// member's own name except where the SDK spells the String form of a
		// composite of enums `<member>WithStrings`.
		wantSetter string
		wantTypes  []string
	}{
		{"a string", "CreateWidget", "Description", `"one"`, `"one"`, "description", nil},
		{
			// The wire value, not Color.fromValue of it: the SDK declares
			// color(String) beside color(Color), and the String overload cannot
			// turn a value the pinned SDK does not know into "null".
			"an enum", "CreateWidget", "Color", `"blue"`, `"blue"`, "color", nil,
		},
		{"an integer", "RotateWidget", "Angle", `45`, `45`, "angle", nil},
		{"a long", "TuneWidget", "Ticks", `9007199254740993`, `9007199254740993L`, "ticks", nil},
		{"a byte", "TuneWidget", "Nudge", `1`, `(byte) 1`, "nudge", nil},
		{"a short", "TuneWidget", "Offset", `-2`, `(short) -2`, "offset", nil},
		{"a float", "TuneWidget", "Ratio", `1.5`, `1.5f`, "ratio", nil},
		{"a double", "TuneWidget", "Precision", `1.5`, `1.5`, "precision", nil},
		{"a whole number into a double", "TuneWidget", "Precision", `2`, `2.0`, "precision", nil},
		{"a boolean", "TuneWidget", "Enabled", `true`, `true`, "enabled", nil},
		{"a string map", "TagWidget", "Tags", `{"compat":"scenario"}`, `Map.of("compat", "scenario")`, "tags", nil},
		{"a list of strings", "UntagWidget", "TagKeys", `["compat"]`, `List.of("compat")`, "tagKeys", nil},
		{
			// The String form of a list of enums, which the SDK names after the
			// member plus WithStrings.
			"a list of enums", "TuneWidget", "Codes", `["blue","red"]`,
			`List.of("blue", "red")`, "codesWithStrings", nil,
		},
		{
			"an enum-keyed map", "TuneWidget", "Palette", `{"blue":"navy"}`,
			`Map.of("blue", "navy")`, "paletteWithStrings", nil,
		},
		{
			// A structure member on its own, not inside a list: its own builder,
			// one setter per member, in member order — as a human would write
			// it. The nested list of enums takes its own WithStrings setter.
			"a nested structure", "TuneWidget", "Spec", `{"Label":"l","Shade":"red","Tints":["blue"]}`,
			`TuneSpec.builder().label("l").shade("red").tintsWithStrings(List.of("blue")).build()`,
			"spec", []string{"TuneSpec"},
		},
		{
			"a list of structures", "TagSprocket", "Tags", `[{"Key":"k","Value":"v"}]`,
			`List.of(SprocketTag.builder().key("k").value("v").build())`, "tags", []string{"SprocketTag"},
		},
		{
			// A model class whose simple name the emitted file has already
			// bound is written out in full rather than imported over.
			"a structure whose class name is already bound", "TuneWidget", "Marker", `{"Key":"k"}`,
			`software.amazon.awssdk.services.widgets.model.Check.builder().key("k").build()`, "marker", nil,
		},
		{
			"an expression into a string", "GetWidget", "WidgetId", `{"$ref":"widget.id"}`,
			`b.string("WidgetId", Values.ref("widget.id"))`, "widgetId", nil,
		},
		{
			// The Binder result is a String, which is what the enum member's
			// String overload takes — so nothing is written around it.
			"an expression into an enum", "CreateWidget", "Color", `{"$name":"c"}`,
			`b.string("Color", Values.name("c"))`, "color", nil,
		},
		{
			"an expression inside a composite", "TagWidget", "Tags", `{"compat":{"$ref":"t"}}`,
			`Map.of("compat", b.string("Tags", Values.ref("t")))`, "tags", nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sp := newJavaSpeller(withJavaTypeShapes(f.model), javaFixtureSDKID)
			target, err := sp.memberTarget(tc.op, tc.member)
			if err != nil {
				t.Fatal(err)
			}
			var v any
			if err := decodeStrict([]byte(tc.value), &v); err != nil {
				t.Fatal(err)
			}
			got, err := sp.value(target, v, tc.member)
			if err != nil {
				t.Fatalf("spelling %s.%s: %v", tc.op, tc.member, err)
			}
			if got != tc.want {
				t.Errorf("%s.%s = %s, want %s", tc.op, tc.member, got, tc.want)
			}
			if setter := sp.setterFor(target, tc.member); setter != tc.wantSetter {
				t.Errorf("%s.%s setter = %s, want %s", tc.op, tc.member, setter, tc.wantSetter)
			}
			// A model class is recorded only when the spelling actually named
			// it, which is what keeps an emitted file free of unused imports.
			if strings.Join(sp.modelImports(), ",") != strings.Join(tc.wantTypes, ",") {
				t.Errorf("model imports = %v, want %v", sp.modelImports(), tc.wantTypes)
			}
		})
	}
}

// TestJavaPascalAndUnCapitalize pins the two SDK naming rules that disagree, and
// the disagreement itself. Organizations really does declare
// `listAWSServiceAccessForOrganization(ListAwsServiceAccessForOrganizationRequest)`
// — one name pascal-cased, the other merely un-capitalized — and an emitter that
// derived either from the other would not compile against it.
func TestJavaPascalAndUnCapitalize(t *testing.T) {
	for _, tc := range []struct{ in, pascal, unCapitalized string }{
		{"SQS", "Sqs", "sqs"},
		{"DynamoDB", "DynamoDb", "dynamoDB"},
		{"WAFV2", "Wafv2", "wafv2"},
		{"Cognito Identity Provider", "CognitoIdentityProvider", "cognito Identity Provider"},
		{"Organizations", "Organizations", "organizations"},
		{"ListAccounts", "ListAccounts", "listAccounts"},
		{"ListAWSServiceAccessForOrganization", "ListAwsServiceAccessForOrganization", "listAWSServiceAccessForOrganization"},
		{"MD5OfBody", "Md5OfBody", "md5OfBody"},
		// The splitter does not split before a *trailing* single capital: the
		// SDK's rule is `([a-z])([A-Z][a-zA-Z])`, so FooB is one word. A
		// lookahead-free `([a-z])([A-Z])` would make it two and name a class
		// that does not exist.
		{"FooB", "Foob", "fooB"},
		{"FooBar", "FooBar", "fooBar"},
		// Two boundaries with nothing between them. The SDK's camel rule is a
		// zero-width split, so `sBy` and `yCo` are both boundaries even though
		// they overlap; a consuming replacement would swallow `By` into the
		// following word and name ListJobsByconsumableResourceRequest, which
		// software.amazon.awssdk:batch does not declare.
		{"ListJobsByConsumableResource", "ListJobsByConsumableResource", "listJobsByConsumableResource"},
		{"AByCd", "AByCd", "aByCd"},
		// The lookahead is `([a-zA-Z]|[0-9])`, so a digit after the capital is
		// a boundary as much as a letter is.
		{"FooB2", "FooB2", "fooB2"},
	} {
		if got := javaPascal(tc.in); got != tc.pascal {
			t.Errorf("javaPascal(%q) = %q, want %q", tc.in, got, tc.pascal)
		}
		if got := javaUnCapitalize(tc.in); got != tc.unCapitalized {
			t.Errorf("javaUnCapitalize(%q) = %q, want %q", tc.in, got, tc.unCapitalized)
		}
	}
	if got := javaNameClientClass("SQS"); got != "SqsClient" {
		t.Errorf("javaNameClientClass(SQS) = %q", got)
	}
	if got := javaNamePackage("Cognito Identity Provider"); got != "software.amazon.awssdk.services.cognitoidentityprovider" {
		t.Errorf("javaNamePackage = %q", got)
	}
}

// TestJavaMethodName pins the SDK's rename of a name it cannot declare a method
// under. A Java keyword is not an identifier at all — SSM models a `default`
// member — and a request builder already declares `build` and `sdkFields`, so a
// setter of either name is renamed too. A client method is not: nothing named
// `build` is declared on a client, and renaming there would name a method that
// does not exist.
func TestJavaMethodName(t *testing.T) {
	for _, tc := range []struct{ member, setter, method string }{
		// A keyword is not an identifier in either position.
		{"Default", "defaultValue", "defaultValue"},
		{"Package", "packageValue", "packageValue"},
		{"New", "newValue", "newValue"},
		// A builder's own methods are only a collision on the builder.
		{"Build", "buildValue", "build"},
		{"SdkFields", "sdkFieldsValue", "sdkFields"},
		// Everything else is unCapitalize and nothing more.
		{"QueueUrl", "queueUrl", "queueUrl"},
		{"Defaults", "defaults", "defaults"},
		{"AWSServiceAccessPrincipals", "awsServiceAccessPrincipals", "awsServiceAccessPrincipals"},
	} {
		if got := javaSetter(tc.member); got != tc.setter {
			t.Errorf("javaSetter(%q) = %q, want %q", tc.member, got, tc.setter)
		}
		if got := javaMethod(tc.member); got != tc.method {
			t.Errorf("javaMethod(%q) = %q, want %q", tc.member, got, tc.method)
		}
	}
}

// TestEmitJavaIndex pins both halves of the index: it lists what was emitted,
// and it still compiles when nothing was.
func TestEmitJavaIndex_listsEveryEmittedServiceAndCompilesWhenEmpty(t *testing.T) {
	source := string(emitJavaIndex([]string{"sqs", "organizations"}))
	if !strings.Contains(source, "new ScenariosOrganizationsGen(clients)") || !strings.Contains(source, "new ScenariosSqsGen(clients)") {
		t.Errorf("index does not name both services:\n%s", source)
	}
	if strings.Index(source, "ScenariosOrganizationsGen") > strings.Index(source, "ScenariosSqsGen") {
		t.Error("the index is not sorted; regeneration would depend on recipe read order")
	}

	empty := string(emitJavaIndex(nil))
	if !strings.Contains(empty, "return List.of();") {
		t.Errorf("the empty index does not compile to an empty list:\n%s", empty)
	}
}

// TestJavaValue_coversTheIRsValueGrammar keeps the naming table total: every
// form the IR admits has a Java expression, and nothing else does.
func TestJavaValue_coversTheIRsValueGrammar(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{"a string", `"hello"`, `"hello"`},
		// Every untyped number is compared as JSON against a response the
		// runtime normalised to a double, so it is written as one.
		{"a number", `30`, `30.0`},
		{"a boolean", `false`, `false`},
		{"null", `null`, `null`},
		{"a list", `["a","b"]`, `Values.list("a", "b")`},
		{"an object", `{"K":"v"}`, `Values.map("K", "v")`},
		{"$name", `{"$name":"q"}`, `Values.name("q")`},
		{"$ref", `{"$ref":"queue.url"}`, `Values.ref("queue.url")`},
		{"$lit", `{"$lit":{"$weird":1}}`, `Values.lit(Values.map("$weird", 1.0))`},
		{"$concat", `{"$concat":["a",{"$ref":"q.u"}]}`, `Values.concat("a", Values.ref("q.u"))`},
		{"$index", `{"$index":[{"$ref":"q.urls"},1]}`, `Values.index(Values.ref("q.urls"), 1)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var v any
			if err := decodeStrict([]byte(tc.in), &v); err != nil {
				t.Fatal(err)
			}
			got, err := javaValue(v)
			if err != nil {
				t.Fatalf("javaValue(%s): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("javaValue(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}

	// A value outside the grammar has no expression, and says so rather than
	// rendering something that would not compile.
	if _, err := javaValue(struct{}{}); err == nil {
		t.Error("a value outside the IR's grammar was rendered")
	}
}

// TestJavaMethodNamesAreUnique refuses two names that fold to one Java
// identifier before the suite fails to build with no indication of which pair
// caused it.
func TestJavaMethodNamesAreUnique(t *testing.T) {
	groups := []group{
		{Name: "svc-gen-a-b", Tests: []test{{Name: "C"}}},
		{Name: "svc-gen-a", Tests: []test{{Name: "BC"}}},
	}
	err := javaMethodNamesAreUnique("svc", groups)
	if err == nil {
		t.Fatal("two colliding method names were accepted")
	}
	for _, want := range []string{"svc-gen-a-b/C", "svc-gen-a/BC", "testSvcGenABC"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the collision message lacks %q: %v", want, err)
		}
	}
}

// TestExplainJavaRendersTheEmittedCall is the definition-of-done item that there
// is one naming table: `-explain -lang java` must print the statements the
// emitter writes, not a second description of them. Both go through
// javaRequestLines, over the same modeled shapes.
func TestExplainJavaRendersTheEmittedCall(t *testing.T) {
	f, gen := generateFixture(t)
	g, tc, ok := gen.scenario.findTest("widgets-gen-widget", "CreateWidget")
	if !ok {
		t.Fatal("fixture has no CreateWidget")
	}
	e := &explainer{st: javaStyle(newJavaSpeller(f.model, javaFixtureSDKID), nil)}
	explained := e.test(gen.scenario, g, tc, func() {})

	emission, err := emitJava(gen)
	if err != nil {
		t.Fatalf("emitJava: %v", err)
	}
	emitted := string(emission.Contents)

	lines, err := javaRequestLines(newJavaSpeller(f.model, javaFixtureSDKID), tc.Call.Op, tc.Call.Params)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) < 3 {
		t.Fatalf("CreateWidget renders %d line(s); the fixture no longer exercises member binding", len(lines))
	}
	for _, line := range lines {
		if !strings.Contains(explained, line) {
			t.Errorf("-explain -lang java does not render %q:\n%s", line, explained)
		}
		if !strings.Contains(emitted, line) {
			t.Errorf("the emitted source does not contain %q", line)
		}
	}
	if !strings.Contains(explained, "client."+javaMethod(tc.Call.Op)+"(request)") {
		t.Errorf("-explain -lang java does not call the operation:\n%s", explained)
	}
}
