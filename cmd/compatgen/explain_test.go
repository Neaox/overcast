//go:build dev

package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestExplain_rendersEveryLanguage(t *testing.T) {
	_, gen := generateFixture(t)
	g, tc, ok := gen.scenario.findTest("widgets-gen-widget", "CreateWidget")
	if !ok {
		t.Fatal("fixture has no CreateWidget")
	}
	for _, lang := range rendererNames() {
		t.Run(lang, func(t *testing.T) {
			out := renderers[lang](gen.scenario, g, tc)
			// Operation names are spelled per language (get_widget, getWidget,
			// GetWidget), so compare with case and separators folded.
			folded := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(out))
			for _, want := range []string{"widgetsgenwidget/createwidget", "createwidget", "getwidget", "listwidgets", "assert"} {
				if !strings.Contains(folded, want) {
					t.Errorf("%s rendering lacks %q:\n%s", lang, want, out)
				}
			}
			if strings.Contains(out, "$ref") || strings.Contains(out, "$name") {
				t.Errorf("%s rendering leaks IR syntax:\n%s", lang, out)
			}
		})
	}
}

func TestExplain_readsTheCommittedScenario(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-root", repoRoot, "-explain", "sqs-gen-queue/DeleteQueue", "-lang", "python"}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"client.delete_queue(", "AWS.SimpleQueueService.NonExistentQueue", "QueueDoesNotExist", "boto3.client(\"sqs\""} {
		if !strings.Contains(out, want) {
			t.Errorf("explain output lacks %q:\n%s", want, out)
		}
	}
	if code := run([]string{"-root", repoRoot, "-explain", "sqs-gen-queue/Nope"}, &stdout, &stderr); code != 1 {
		t.Fatalf("unknown test accepted: code=%d", code)
	}
	if code := run([]string{"-root", repoRoot, "-explain", "sqs-gen-queue/DeleteQueue", "-lang", "cobol"}, &stdout, &stderr); code != 1 {
		t.Fatalf("unknown language accepted: code=%d", code)
	}
}

func TestScaffold_proposesASkeletonTheSchemaRefuses(t *testing.T) {
	f := loadFixture(t)
	skeleton := scaffold(f.model, "widgets")
	// One cluster per Create* operation, in name order; the gauge, which has
	// no create, is not something name clustering can propose.
	var ids []string
	res := scaffoldResource{}
	for _, proposed := range skeleton.Resources {
		ids = append(ids, proposed.ID)
		if proposed.ID == "widget" {
			res = proposed
		}
	}
	if strings.Join(ids, ",") != "cog,sprocket,widget" {
		t.Fatalf("scaffold proposed %v, want one cluster per create operation", ids)
	}
	// Describe* is preferred to Get* as the read, which is a heuristic the
	// recipe author is expected to correct — the skeleton is a time-saver,
	// never an authority.
	if res.Create["op"] != "CreateWidget" || res.Read["op"] != "DescribeWidget" || res.List["op"] != "ListWidgets" || res.Delete["op"] != "DeleteWidget" {
		t.Errorf("lifecycle roles = create %v read %v list %v delete %v", res.Create["op"], res.Read["op"], res.List["op"], res.Delete["op"])
	}
	if res.List["itemsPath"] != "$.Widgets" || res.NotFound["error"] != "WidgetNotFound" {
		t.Errorf("list itemsPath %v, notFound %v", res.List["itemsPath"], res.NotFound)
	}
	if _, ok := res.Binds["WidgetId"]; !ok {
		t.Errorf("required member WidgetId not pre-listed in binds: %v", res.Binds)
	}
	contents, err := encodeDocument(skeleton)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), scaffoldTodo) {
		t.Fatal("skeleton carries no $todo placeholder")
	}
	if err := f.schemas.validate(schemaRecipe, contents); err == nil {
		t.Fatal("a skeleton must not pass as a finished recipe")
	}
	// The mutable placeholder names the update operation.
	if len(res.Mutable) != 1 || res.Mutable[0].(map[string]any)["op"] != "UpdateWidget" {
		t.Errorf("mutable = %v", res.Mutable)
	}
}

func TestReport_listsCoverageRefusalsAndSamples(t *testing.T) {
	_, gen := generateFixture(t)
	var out bytes.Buffer
	writeReport(&out, gen, 2)
	report := out.String()
	for _, want := range []string{
		"21 of 34 modeled operations",
		"| RotateWidget | widgets-gen-probe | `never-probe` |",
		"| SetWidgetSize | widgets-gen-probe | `update-without-mutable` |",
		"### Automatic name-match bindings",
		"None — every bound member",
		"### Sampled scenarios (2, seed 1113)",
		"```python",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	var again bytes.Buffer
	writeReport(&again, gen, 2)
	if again.String() != report {
		t.Fatal("the report is not deterministic")
	}
}
