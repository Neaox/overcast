//go:build dev

package main

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/capabilities"
)

// The fixture: testdata/shapes/widgets.json models a service whose operations
// cover every emitter decision, testdata/recipes/widgets.json gives one
// resource every role, and the capability table below decides which
// operations count as implemented.
//
//	CreateWidget … ListWidgetTags   implemented, in the recipe  → lifecycle tests
//	SetWidgetSize                   implemented, update family  → update-without-mutable
//	ArchiveWidget                   implemented, no role        → probe-of-implemented-op
//	FreezeWidget                    undeclared, returns nothing → probe with a read-back
//	PingWidgets                     undeclared, returns Status  → probe with a responseField
//	RotateWidget                    Unsupported, Angle unbound  → unbound-required-member
//	DescribeGizmo                   undeclared, GizmoArn unbound → unbound-required-member
//	PurgeWidgets                    undeclared, nothing to see  → no-output-to-assert

func fixtureCaps() capabilityTable {
	table := capabilityTable{}
	for _, op := range []string{"CreateWidget", "GetWidget", "ListWidgets", "UpdateWidget", "DeleteWidget", "TagWidget", "UntagWidget", "ListWidgetTags", "SetWidgetSize", "ArchiveWidget"} {
		table[op] = capabilities.StatusSupported
	}
	table["RotateWidget"] = capabilities.StatusUnsupported
	return table
}

var fixtureClient = clientInfo{SDKID: "Widgets", EndpointPrefix: "widgets", SigningName: "widgets", Protocol: "awsJson1_1", APIVersion: "2026-01-01", TargetPrefix: "WidgetService"}

func modelSchemaDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "compat", "model")
}

type fixture struct {
	schemas *schemaSet
	model   *serviceModel
	recipe  recipe
	values  *valuesTable
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	schemas, err := loadSchemas(modelSchemaDir(t))
	if err != nil {
		t.Fatalf("load schemas: %v", err)
	}
	model, err := loadModel(filepath.Join("testdata", "shapes"), "widgets")
	if err != nil {
		t.Fatalf("load fixture model: %v", err)
	}
	recipes, err := loadRecipes(filepath.Join("testdata", "recipes"), schemas)
	if err != nil {
		t.Fatalf("load fixture recipe: %v", err)
	}
	values, err := loadValues(filepath.Join("testdata", "values.json"), schemas)
	if err != nil {
		t.Fatalf("load fixture values: %v", err)
	}
	return fixture{schemas: schemas, model: model, recipe: recipes[0], values: values}
}

func generateFixture(t *testing.T) (fixture, *generation) {
	t.Helper()
	f := loadFixture(t)
	gen, err := generate(f.model, f.recipe, f.values, fixtureCaps(), fixtureClient)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	return f, gen
}

func TestGenerate_lifecycleGroupCoversEveryRole(t *testing.T) {
	// Given: the fixture recipe.
	// When: it is generated.
	_, gen := generateFixture(t)

	// Then: one lifecycle group with the tests in lifecycle order, and one
	// probe group with only the unimplemented operations.
	want := map[string][]string{
		"widgets-gen-widget": {"CreateWidget", "GetWidget", "UpdateWidget", "TagWidget", "ListWidgetTags", "UntagWidget", "ListWidgets", "DeleteWidget"},
		"widgets-gen-probe":  {"FreezeWidget", "PingWidgets"},
	}
	if len(gen.scenario.Groups) != len(want) {
		t.Fatalf("got %d groups, want %d", len(gen.scenario.Groups), len(want))
	}
	for _, g := range gen.scenario.Groups {
		var names []string
		for _, tc := range g.Tests {
			names = append(names, tc.Name)
		}
		if strings.Join(names, ",") != strings.Join(want[g.Name], ",") {
			t.Errorf("group %s tests = %v, want %v", g.Name, names, want[g.Name])
		}
	}
}

func TestGenerate_refusesWithMachineReadableReasons(t *testing.T) {
	_, gen := generateFixture(t)
	want := map[string]string{
		"ArchiveWidget": reasonProbeOfImplementedOp,
		"DescribeGizmo": reasonUnboundRequiredMember + ":GizmoArn",
		"PurgeWidgets":  reasonNoOutputToAssert,
		"RotateWidget":  reasonUnboundRequiredMember + ":Angle",
		"SetWidgetSize": reasonUpdateWithoutMutable,
	}
	got := map[string]string{}
	for _, gp := range gen.gaps {
		got[gp.Operation] = gp.Reason
		if gp.Detail == "" || gp.Service != "widgets" {
			t.Errorf("gap %+v is missing detail or service", gp)
		}
	}
	for op, reason := range want {
		if got[op] != reason {
			t.Errorf("%s: reason %q, want %q", op, got[op], reason)
		}
	}
	if len(got) != len(want) {
		t.Errorf("gaps = %v, want exactly %v", got, want)
	}
}

func TestGenerate_probeGroupHoldsNoImplementedOperation(t *testing.T) {
	_, gen := generateFixture(t)
	caps := fixtureCaps()
	for _, g := range gen.scenario.Groups {
		if g.Kind != groupProbe {
			continue
		}
		for _, tc := range g.Tests {
			if caps.implemented(tc.Op) {
				t.Errorf("implemented operation %s sits in probe group %s", tc.Op, g.Name)
			}
		}
		// A probe of an operation that returns nothing reads the bound
		// resource back; one that returns something asserts on it.
		for _, tc := range g.Tests {
			switch tc.Name {
			case "FreezeWidget":
				if tc.Assert[0].Kind != assertReadback || tc.Assert[0].Call.Op != "GetWidget" {
					t.Errorf("FreezeWidget probe asserts %+v, want a GetWidget read-back", tc.Assert[0])
				}
			case "PingWidgets":
				if tc.Assert[0].Kind != assertResponseField || tc.Assert[0].Checks["$.Status"].NonEmpty != true {
					t.Errorf("PingWidgets probe asserts %+v, want $.Status non-empty", tc.Assert[0])
				}
			}
		}
		// The probe's setup creates only what a probe referenced, and tears
		// it down again.
		if len(g.Setup) != 1 || g.Setup[0].Op != "CreateWidget" || len(g.Teardown) != 1 || g.Teardown[0].Op != "DeleteWidget" {
			t.Errorf("probe setup/teardown = %v / %v", g.Setup, g.Teardown)
		}
	}
}

func TestGenerate_everyTestCarriesAnAssertionAndValidates(t *testing.T) {
	f, gen := generateFixture(t)
	if err := validateScenario(gen.scenario); err != nil {
		t.Fatalf("scenario invariants: %v", err)
	}
	contents, err := encodeDocument(gen.scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.schemas.validate(schemaScenario, contents); err != nil {
		t.Fatalf("generated scenario does not satisfy its schema: %v", err)
	}
	doc := gapsDocument{Version: gapsVersion, Gaps: gen.gaps}
	contents, err = encodeDocument(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.schemas.validate(schemaGaps, contents); err != nil {
		t.Fatalf("generated gaps do not satisfy their schema: %v", err)
	}
}

func TestValidateScenario_rejectsAVacuousTest(t *testing.T) {
	// The constructors cannot build one; a hand-built literal can, and the
	// belt catches it.
	s := &scenario{Version: scenarioVersion, Service: "widgets", Groups: []group{{
		Name: "widgets-gen-widget", Kind: groupLifecycle,
		Tests: []test{{Name: "CreateWidget", Op: "CreateWidget", Call: call{Op: "CreateWidget", Params: map[string]any{}}}},
	}}}
	if err := validateScenario(s); err == nil || !strings.Contains(err.Error(), "no assertion clause") {
		t.Fatalf("vacuous test passed validation: %v", err)
	}
}

func TestGenerate_assertionsFollowTheContract(t *testing.T) {
	_, gen := generateFixture(t)
	_, create, _ := gen.scenario.findTest("widgets-gen-widget", "CreateWidget")
	kinds := assertionKinds(create.Assert)
	if strings.Join(kinds, ",") != "responseField,readback,listContains" {
		t.Errorf("CreateWidget asserts %v", kinds)
	}
	// Identity fields carry the model's pattern, and the read-back asserts
	// the mutable member's initial value.
	if create.Assert[0].Checks["$.WidgetId"].Matches != "^w-[0-9a-f]{8}$" {
		t.Errorf("CreateWidget identity check = %+v", create.Assert[0].Checks["$.WidgetId"])
	}
	if create.Assert[1].Checks["$.Widget.Description"].Equals != "one" {
		t.Errorf("CreateWidget read-back does not assert the initial description: %+v", create.Assert[1].Checks)
	}
	if create.Call.Params["Description"] != "one" {
		t.Errorf("mutable.from was not merged into the create params: %v", create.Call.Params)
	}

	_, update, _ := gen.scenario.findTest("widgets-gen-widget", "UpdateWidget")
	if update.Call.Params["Description"] != "two" || update.Assert[0].Kind != assertReadback || update.Assert[0].Checks["$.Widget.Description"].Equals != "two" {
		t.Errorf("UpdateWidget = %+v", update)
	}

	_, del, _ := gen.scenario.findTest("widgets-gen-widget", "DeleteWidget")
	if del.Assert[0].Kind != assertAbsent || del.Assert[0].Error == nil || del.Assert[0].Error.Code != "Widget.NotFound" || del.Assert[0].Error.Shape != "WidgetNotFound" {
		t.Errorf("DeleteWidget absence = %+v", del.Assert[0])
	}

	_, untag, _ := gen.scenario.findTest("widgets-gen-widget", "UntagWidget")
	if !untag.Assert[0].Checks["$.Tags.compat"].Missing {
		t.Errorf("UntagWidget read-back = %+v", untag.Assert[0])
	}

	// Dependencies follow exports: everything after CreateWidget consumes
	// widget.id.
	_, get, _ := gen.scenario.findTest("widgets-gen-widget", "GetWidget")
	if strings.Join(get.Depends, ",") != "CreateWidget" {
		t.Errorf("GetWidget depends = %v", get.Depends)
	}
	if len(create.Depends) != 0 {
		t.Errorf("CreateWidget depends = %v", create.Depends)
	}
}

func assertionKinds(clauses []assertion) []string {
	var kinds []string
	for _, a := range clauses {
		kinds = append(kinds, a.Kind)
	}
	return kinds
}

func TestGenerate_isByteDeterministic(t *testing.T) {
	f := loadFixture(t)
	var renderings [][]byte
	for i := 0; i < 3; i++ {
		gen, err := generate(f.model, f.recipe, f.values, fixtureCaps(), fixtureClient)
		if err != nil {
			t.Fatal(err)
		}
		contents, err := encodeDocument(gen.scenario)
		if err != nil {
			t.Fatal(err)
		}
		renderings = append(renderings, contents)
	}
	for i := 1; i < len(renderings); i++ {
		if !bytes.Equal(renderings[0], renderings[i]) {
			t.Fatalf("run %d differs from run 0", i)
		}
	}
	if !bytes.HasSuffix(renderings[0], []byte("\n")) || bytes.Contains(renderings[0], []byte("\r")) {
		t.Fatal("output must end in one LF and contain no CR")
	}
}

func TestBinder_rules(t *testing.T) {
	f := loadFixture(t)
	widget := f.recipe.Resources[0]
	b := &binder{model: f.model, service: "widgets", values: f.values}
	exports := exportKinds{"widget.id": "string"}
	scope := bindScope{resources: []resource{widget}, exports: exports}

	cases := []struct {
		name     string
		op       string
		explicit map[string]any
		values   *valuesTable
		wantKey  string
		want     any
		refusal  string
		errText  string
	}{
		{name: "rule 1: explicit bind", op: "GetWidget", wantKey: "WidgetId", want: map[string]any{"$ref": "widget.id"}},
		{name: "rule 3: curated literal", op: "RotateWidget", values: &valuesTable{Services: map[string]serviceValues{"widgets": {Operations: map[string]map[string]any{"RotateWidget": {"Angle": json.Number("90")}}}}}, wantKey: "Angle", want: json.Number("90")},
		{name: "rule 4: range minimum", op: "SetWidgetSize", wantKey: "Size", want: json.Number("1")},
		{name: "rule 5: refuse an unconstrained string", op: "DescribeGizmo", refusal: reasonUnboundRequiredMember + ":GizmoArn"},
		{name: "explicit params are checked against the model", op: "CreateWidget", explicit: map[string]any{"Name": "Not Legal!"}, errText: "does not match"},
		{name: "an unknown member is an error, not a refusal", op: "CreateWidget", explicit: map[string]any{"Title": "x"}, errText: "no input member"},
		{name: "a literal of the wrong kind is an error", op: "SetWidgetSize", explicit: map[string]any{"Size": "big"}, errText: "wants a number"},
		{name: "a literal outside the range is an error", op: "SetWidgetSize", explicit: map[string]any{"Size": json.Number("500")}, errText: "above"},
		{name: "an enum value is checked", op: "CreateWidget", explicit: map[string]any{"Name": "ok", "Color": "green"}, errText: "not one of"},
		{name: "a $name that cannot fit is an error", op: "CreateWidget", explicit: map[string]any{"Name": map[string]any{"$name": strings.Repeat("x", 60)}}, errText: "modeled maximum"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b.values = f.values
			if tc.values != nil {
				b.values = tc.values
			}
			params, ref, err := b.bind("widgets-gen-widget", tc.op, tc.explicit, scope)
			switch {
			case tc.errText != "":
				if err == nil || !strings.Contains(err.Error(), tc.errText) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.errText)
				}
			case tc.refusal != "":
				if err != nil || ref == nil || ref.Reason != tc.refusal {
					t.Fatalf("got params=%v ref=%v err=%v, want refusal %s", params, ref, err, tc.refusal)
				}
			default:
				if err != nil || ref != nil {
					t.Fatalf("bind: ref=%v err=%v", ref, err)
				}
				got, _ := json.Marshal(params[tc.wantKey])
				want, _ := json.Marshal(tc.want)
				if string(got) != string(want) {
					t.Fatalf("%s = %s, want %s", tc.wantKey, got, want)
				}
			}
		})
	}
}

func TestBinder_recordsAutomaticNameMatch(t *testing.T) {
	f := loadFixture(t)
	widget := f.recipe.Resources[0]
	widget.Binds = nil                                           // no rule-1 bind…
	widget.Exports = map[string]string{"WidgetId": "$.WidgetId"} // …but an export named like the member
	b := &binder{model: f.model, service: "widgets", values: f.values}
	scope := bindScope{resources: []resource{widget}, exports: exportKinds{"widget.WidgetId": "string"}}
	params, ref, err := b.bind("g", "GetWidget", nil, scope)
	if err != nil || ref != nil {
		t.Fatalf("bind: %v %v", ref, err)
	}
	if got := params["WidgetId"].(map[string]any)["$ref"]; got != "widget.WidgetId" {
		t.Fatalf("bound to %v", got)
	}
	if len(b.auto) != 1 || b.auto[0].Member != "WidgetId" {
		t.Fatalf("automatic binding not recorded: %+v", b.auto)
	}
}

func TestValues_rejectsAnExpression(t *testing.T) {
	f := loadFixture(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "values.json")
	writeFile(t, path, `{"version":1,"services":{"widgets":{"members":{"Angle":{"$ref":"widget.id"}}}}}`)
	if _, err := loadValues(path, f.schemas); err == nil || !strings.Contains(err.Error(), "values.schema.json") {
		t.Fatalf("an expression in values.json loaded: %v", err)
	}
}

func TestRecipe_rejectsUnknownFieldsAndBadReferences(t *testing.T) {
	f := loadFixture(t)
	cases := []struct{ name, body, want string }{
		{"unknown field", `{"service":"w","resources":[{"id":"a","create":{"op":"CreateWidget"},"colour":"blue"}]}`, "recipe.schema.json"},
		{"unknown expression", `{"service":"w","resources":[{"id":"a","create":{"op":"CreateWidget","params":{"Name":{"$todo":"x"}}}}]}`, "recipe.schema.json"},
		{"bad path", `{"service":"w","resources":[{"id":"a","create":{"op":"CreateWidget"},"exports":{"id":"WidgetId"}}]}`, "recipe.schema.json"},
		{"unknown requirement", `{"service":"w","resources":[{"id":"a","requires":["b"],"create":{"op":"CreateWidget"}}]}`, "unknown resource"},
		{"identity not exported", `{"service":"w","resources":[{"id":"a","create":{"op":"CreateWidget"},"read":{"op":"GetWidget","identityPath":"$.Widget.WidgetId","identity":"nope"}}]}`, "not an export"},
		{"authored op without assertion", `{"service":"w","resources":[{"id":"a","create":{"op":"CreateWidget"},"operations":[{"op":"PingWidgets","assert":[]}]}]}`, "recipe.schema.json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "w.json")
			writeFile(t, path, tc.body)
			_, err := loadRecipe(path, f.schemas)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestGenerate_recipeContradictingTheModelIsAnError(t *testing.T) {
	f := loadFixture(t)
	cases := []struct {
		name   string
		mutate func(r *recipe)
		want   string
	}{
		{"unknown operation", func(r *recipe) { r.Resources[0].Delete = &recipeCall{Op: "DestroyWidget"} }, "does not model"},
		{"unknown export path", func(r *recipe) { r.Resources[0].Exports = map[string]string{"id": "$.Widget.Id"} }, "no member"},
		{"unknown error shape", func(r *recipe) { r.Resources[0].NotFound = &notFoundSpec{Error: "WidgetExists"} }, "does not declare error"},
		{"literal of the wrong kind for the read path", func(r *recipe) { r.Resources[0].Mutable[0].To = json.Number("2") }, "wants a string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := f.recipe
			r.Resources = []resource{f.recipe.Resources[0]}
			tc.mutate(&r)
			_, err := generate(f.model, r, f.values, fixtureCaps(), fixtureClient)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestGenerate_refusesUpdateWhoseReadConsumes(t *testing.T) {
	f := loadFixture(t)
	r := f.recipe
	res := f.recipe.Resources[0]
	read := *res.Read
	read.Consuming = true
	res.Read = &read
	res.NotFound = nil
	r.Resources = []resource{res}
	gen, err := generate(f.model, r, f.values, fixtureCaps(), fixtureClient)
	if err != nil {
		t.Fatal(err)
	}
	var reasons []string
	for _, gp := range gen.gaps {
		if gp.Operation == "UpdateWidget" {
			reasons = append(reasons, gp.Reason)
		}
	}
	if strings.Join(reasons, ",") != reasonNoReadbackPath {
		t.Fatalf("UpdateWidget reasons = %v, want %s", reasons, reasonNoReadbackPath)
	}
}
