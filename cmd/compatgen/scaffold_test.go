//go:build dev

package main

import (
	"encoding/json"
	"sort"
	"strings"
	"testing"
)

// Every test here runs against the `widgets` fixture under testdata/ or
// against a hand-built model in this file. None of them reads
// models/aws/shapes/: scaffolding one real service costs more than the whole
// unit suite, and nothing the scaffolder does needs a real corpus to
// demonstrate.

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
	// The mutable placeholder names the update operation. SetWidgetSize is a
	// sub-noun candidate for the same role and must not displace it.
	if len(res.Mutable) != 1 || res.Mutable[0].(map[string]any)["op"] != "UpdateWidget" {
		t.Errorf("mutable = %v", res.Mutable)
	}
}

// TestScaffold_clustersSubNounsOntoTheNounTheCreateNames is the SQS case from
// #1795: GetQueueAttributes and SetQueueAttributes name the noun
// "QueueAttributes", nothing creates it, and before this the queue cluster
// was left with no read, no mutable and no notFound at all.
func TestScaffold_clustersSubNounsOntoTheNounTheCreateNames(t *testing.T) {
	skeleton := scaffold(queueLikeModel(), "queues")
	if len(skeleton.Resources) != 1 {
		t.Fatalf("proposed %d resources, want one lifecycle for the noun Queue", len(skeleton.Resources))
	}
	res := skeleton.Resources[0]
	if res.ID != "queue" {
		t.Fatalf("resource id = %q", res.ID)
	}
	if res.Read["op"] != "GetQueueAttributes" {
		t.Errorf("read = %v, want the Get* on the sub-noun QueueAttributes", res.Read["op"])
	}
	if len(res.Mutable) != 1 || res.Mutable[0].(map[string]any)["op"] != "SetQueueAttributes" {
		t.Errorf("mutable = %v, want the Set* on the sub-noun QueueAttributes", res.Mutable)
	}
	if res.NotFound["error"] != "QueueDoesNotExist" {
		t.Errorf("notFound = %v, want the read's modeled not-found error", res.NotFound)
	}
	// ListQueueTags sorts before ListQueues and its noun ("QueueTag") also
	// sits under "Queue"; the direct match must still win the list role.
	if res.List["op"] != "ListQueues" {
		t.Errorf("list = %v, want the direct match ListQueues", res.List["op"])
	}
	// The tag operations are found, but they inform the hint rather than
	// filling in a tag lifecycle nobody has checked.
	tags, _ := res.Tags[scaffoldTodo].(string)
	for _, want := range []string{"TagQueue", "UntagQueue", "ListQueueTags"} {
		if !strings.Contains(tags, want) {
			t.Errorf("tags hint does not name %s: %s", want, tags)
		}
	}
	if res.Review != "" {
		t.Errorf("a create/delete pair with a modeled not-found error needs no review: %q", res.Review)
	}
}

func TestScaffold_namesTheRuleBehindEveryDerivedValue(t *testing.T) {
	f := loadFixture(t)
	byID := scaffoldByID(scaffold(f.model, "widgets"))

	// itemsPath has two rules, and the comment says which one fired.
	if got := comment(t, byID["cog"].List); !strings.Contains(got, "itemsPath from the sole list member") {
		t.Errorf("cog list comment = %q", got)
	}
	if got := comment(t, scaffoldByID(scaffold(queueLikeModel(), "queues"))["queue"].List); !strings.Contains(got, "itemsPath from @paginated.items") {
		t.Errorf("queue list comment = %q", got)
	}
	// The identity export, the not-found error and the resource itself.
	if got := comment(t, byID["cog"].Exports); !strings.Contains(got, "identity-member rule") || !strings.Contains(got, "CogId") {
		t.Errorf("cog exports comment = %q", got)
	}
	if got := comment(t, byID["sprocket"].NotFound); got != "from the read's modeled errors: SprocketNotFound" {
		t.Errorf("sprocket notFound comment = %q", got)
	}
	if got := byID["widget"].Comment; got != "from name clustering: CreateWidget/DescribeWidget/ListWidgets/UpdateWidget/DeleteWidget" {
		t.Errorf("widget comment = %q", got)
	}
	// Every block that carries a derived value carries its provenance.
	for id, res := range byID {
		for name, block := range map[string]map[string]any{
			"create": res.Create, "exports": res.Exports, "read": res.Read,
			"list": res.List, "delete": res.Delete, "notFound": res.NotFound,
		} {
			if block == nil {
				continue
			}
			if _, isTodo := block[scaffoldTodo]; isTodo {
				continue // wholly unresolved: the hint is the provenance
			}
			if text, ok := block["$comment"].(string); !ok || text == "" {
				t.Errorf("%s.%s carries no $comment: %v", id, name, block)
			}
		}
	}
}

func TestScaffold_marksAnIrreversibleLifecycleForReview(t *testing.T) {
	f := loadFixture(t)
	byID := scaffoldByID(scaffold(f.model, "widgets"))

	// Nothing deletes a cog, so nothing the run does undoes CreateCog.
	cog := byID["cog"]
	if cog.Review != scaffoldReviewMarker {
		t.Errorf("cog $review = %q, want %q", cog.Review, scaffoldReviewMarker)
	}
	if !strings.Contains(cog.Comment, "no delete, so nothing undoes CreateCog") {
		t.Errorf("cog comment does not say why it is marked: %q", cog.Comment)
	}
	// A create, a delete and a modeled not-found error to prove the delete:
	// the run undoes its own work, so the proposal stands unmarked.
	for _, id := range []string{"sprocket", "widget"} {
		if byID[id].Review != "" {
			t.Errorf("%s marked for review despite a verifiable delete: %q", id, byID[id].Review)
		}
	}
	// The rule holds for every proposal, not just these three.
	for id, res := range byID {
		verifiable := res.Delete != nil && res.NotFound != nil && res.NotFound["error"] != nil
		if verifiable == (res.Review != "") {
			t.Errorf("%s: review marker %q disagrees with delete=%v notFound=%v", id, res.Review, res.Delete, res.NotFound)
		}
	}
}

func TestScaffold_emitsATodoForEveryFieldAHumanSupplies(t *testing.T) {
	f := loadFixture(t)
	skeleton := scaffold(f.model, "widgets")
	for _, res := range skeleton.Resources {
		// The vocabulary a service may need and the model cannot propose is
		// present on every resource, so the reader sees what is expected
		// rather than what happened to be optional.
		for name, block := range map[string]map[string]any{
			"requires": res.Requires, "derived": res.Derived,
			"tags": res.Tags, "async": res.Async, "operations": res.Operations,
		} {
			hint, ok := block[scaffoldTodo].(string)
			if !ok || len(hint) < 20 {
				t.Errorf("%s.%s is not a $todo with a hint: %v", res.ID, name, block)
			}
		}
		// `binds` is either one $todo per @required member, or — for a
		// lifecycle with no @required member at all — a single $todo standing
		// for the whole field.
		if _, wholeBlock := res.Binds[scaffoldTodo]; !wholeBlock {
			for member, target := range res.Binds {
				if member == "$comment" {
					continue
				}
				if _, ok := target.(map[string]any)[scaffoldTodo]; !ok {
					t.Errorf("%s.binds.%s is not a $todo: %v", res.ID, member, target)
				}
			}
		}
		for _, m := range res.Mutable {
			for _, field := range []string{"member", "from", "to", "readPath"} {
				if _, ok := m.(map[string]any)[field].(map[string]any)[scaffoldTodo]; !ok {
					t.Errorf("%s.mutable.%s is not a $todo: %v", res.ID, field, m)
				}
			}
		}
	}
	// The prose form the placeholders used to take is gone: a `$todo` key is
	// the only thing recipe.schema.json is guaranteed to refuse.
	contents, err := encodeDocument(skeleton)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), "<todo") {
		t.Error("skeleton still carries a prose <todo> placeholder the schema would accept as a value")
	}
}

func TestScaffold_refusesToProposeAStructureAsAnIdentity(t *testing.T) {
	f := loadFixture(t)
	byID := scaffoldByID(scaffold(f.model, "widgets"))

	// DescribeWidget answers with an envelope, not an identity.
	hint, ok := byID["widget"].Read["identityPath"].(map[string]any)[scaffoldTodo].(string)
	if !ok {
		t.Fatalf("widget read identityPath = %v, want a $todo", byID["widget"].Read["identityPath"])
	}
	for _, want := range []string{"$.Widget", "not an identity", "WidgetId"} {
		if !strings.Contains(hint, want) {
			t.Errorf("identityPath hint does not mention %q: %s", want, hint)
		}
	}
	// A list of envelopes is the same problem one level down.
	if _, ok := byID["widget"].List["identityPath"].(map[string]any); !ok {
		t.Errorf("widget list identityPath = %v, want a $todo for a list of structures", byID["widget"].List["identityPath"])
	}
	// A scalar identity is still proposed outright, with its provenance.
	if byID["cog"].Exports["id"] != "$.CogId" || byID["cog"].List["identityPath"] != "$" {
		t.Errorf("cog identity = %v, list identityPath = %v", byID["cog"].Exports["id"], byID["cog"].List["identityPath"])
	}
}

func TestScaffold_isDeterministic(t *testing.T) {
	f := loadFixture(t)
	first, err := encodeDocument(scaffold(f.model, "widgets"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := encodeDocument(scaffold(f.model, "widgets"))
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("scaffold run %d differs from the first:\n%s", i+2, again)
		}
	}
}

func scaffoldByID(skeleton scaffoldRecipe) map[string]scaffoldResource {
	byID := make(map[string]scaffoldResource, len(skeleton.Resources))
	for _, res := range skeleton.Resources {
		byID[res.ID] = res
	}
	return byID
}

func comment(t *testing.T, block map[string]any) string {
	t.Helper()
	if block == nil {
		t.Fatal("no block to read a $comment from")
	}
	text, _ := block["$comment"].(string)
	return text
}

// queueLikeModel is SQS's queue lifecycle, cut down to what the scaffolder
// reads. It is built here rather than loaded from models/aws/shapes/ so the
// sub-noun clustering, the @paginated.items rule and the tag-operation
// hint are all exercised without the corpus.
func queueLikeModel() *serviceModel {
	shape := func(kind string, members map[string]modelMember, traits map[string]json.RawMessage) modelShape {
		return modelShape{Type: kind, Members: members, Traits: traits}
	}
	required := map[string]json.RawMessage{"smithy.api#required": json.RawMessage("{}")}
	str := func(traits map[string]json.RawMessage) modelMember {
		return modelMember{Target: "smithy.api#String", Traits: traits}
	}
	op := func(input, output string, errs ...string) modelShape {
		return modelShape{Type: "operation", Input: input, Output: output, Errors: errs}
	}
	shapes := map[string]modelShape{
		"CreateQueue":        op("CreateQueueRequest", "CreateQueueResult"),
		"DeleteQueue":        op("QueueUrlRequest", "smithy.api#Unit", "QueueDoesNotExist"),
		"GetQueueAttributes": op("QueueUrlRequest", "GetQueueAttributesResult", "InvalidAddress", "QueueDoesNotExist"),
		"ListQueueTags":      op("QueueUrlRequest", "ListQueueTagsResult"),
		"ListQueues":         op("smithy.api#Unit", "ListQueuesResult"),
		"SetQueueAttributes": op("SetQueueAttributesRequest", "smithy.api#Unit", "QueueDoesNotExist"),
		"TagQueue":           op("QueueUrlRequest", "smithy.api#Unit"),
		"UntagQueue":         op("QueueUrlRequest", "smithy.api#Unit"),

		"CreateQueueRequest":       shape("structure", map[string]modelMember{"QueueName": str(required)}, nil),
		"CreateQueueResult":        shape("structure", map[string]modelMember{"QueueUrl": str(nil)}, nil),
		"QueueUrlRequest":          shape("structure", map[string]modelMember{"QueueUrl": str(required)}, nil),
		"GetQueueAttributesResult": shape("structure", map[string]modelMember{"Attributes": {Target: "AttributeMap"}}, nil),
		"ListQueueTagsResult":      shape("structure", map[string]modelMember{"Tags": {Target: "AttributeMap"}}, nil),
		"ListQueuesResult":         shape("structure", map[string]modelMember{"NextToken": str(nil), "QueueUrls": {Target: "QueueUrlList"}}, nil),
		"SetQueueAttributesRequest": shape("structure", map[string]modelMember{
			"Attributes": {Target: "AttributeMap", Traits: required},
			"QueueUrl":   str(required),
		}, nil),
		"AttributeMap":      {Type: "map", Key: "smithy.api#String", Value: "smithy.api#String"},
		"QueueUrlList":      {Type: "list", Member: "smithy.api#String"},
		"QueueDoesNotExist": shape("structure", nil, map[string]json.RawMessage{"smithy.api#error": json.RawMessage(`"client"`)}),
		"InvalidAddress":    shape("structure", nil, map[string]json.RawMessage{"smithy.api#error": json.RawMessage(`"client"`)}),
	}
	// ListQueues declares which output member carries the page, so itemsPath
	// comes from the trait rather than from "the sole list member".
	listQueues := shapes["ListQueues"]
	listQueues.Traits = map[string]json.RawMessage{
		"smithy.api#paginated": json.RawMessage(`{"inputToken":"NextToken","outputToken":"NextToken","items":"QueueUrls"}`),
	}
	shapes["ListQueues"] = listQueues

	model := &serviceModel{shapeSnapshot: shapeSnapshot{Service: "queues", Shapes: shapes}}
	for name, s := range shapes {
		if s.Type == "operation" {
			model.operationNames = append(model.operationNames, name)
		}
	}
	sort.Strings(model.operationNames)
	return model
}
