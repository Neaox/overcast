package dynamodb

import (
	"testing"
)

// ---------------------------------------------------------------------------
// UPDATE expression tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// SET clause
// ---------------------------------------------------------------------------

func TestUpdate_setSimple(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
	}
	err := applyUpdateExpression(item,
		"SET #n = :v",
		map[string]string{"#n": "nickname"},
		map[string]attrValue{":v": {"S": "Alice"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["nickname"]) != "Alice" {
		t.Errorf("expected 'Alice', got %q", extractScalar(item["nickname"]))
	}
}

func TestUpdate_setOverwrite(t *testing.T) {
	item := Item{
		"id":       attrValue{"S": "1"},
		"nickname": attrValue{"S": "Old"},
	}
	err := applyUpdateExpression(item,
		"SET nickname = :v",
		nil,
		map[string]attrValue{":v": {"S": "New"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["nickname"]) != "New" {
		t.Errorf("expected 'New', got %q", extractScalar(item["nickname"]))
	}
}

func TestUpdate_setMultiple(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
	}
	err := applyUpdateExpression(item,
		"SET a = :a, b = :b",
		nil,
		map[string]attrValue{
			":a": {"S": "alpha"},
			":b": {"S": "beta"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["a"]) != "alpha" {
		t.Errorf("expected 'alpha', got %q", extractScalar(item["a"]))
	}
	if extractScalar(item["b"]) != "beta" {
		t.Errorf("expected 'beta', got %q", extractScalar(item["b"]))
	}
}

func TestUpdate_setArithmeticAdd(t *testing.T) {
	item := Item{
		"id":    attrValue{"S": "1"},
		"tally": attrValue{"N": "10"},
	}
	err := applyUpdateExpression(item,
		"SET tally = tally + :inc",
		nil,
		map[string]attrValue{":inc": {"N": "5"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["tally"]) != "15" {
		t.Errorf("expected '15', got %q", extractScalar(item["tally"]))
	}
}

func TestUpdate_setArithmeticSubtract(t *testing.T) {
	item := Item{
		"id":    attrValue{"S": "1"},
		"tally": attrValue{"N": "10"},
	}
	err := applyUpdateExpression(item,
		"SET tally = tally - :dec",
		nil,
		map[string]attrValue{":dec": {"N": "3"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["tally"]) != "7" {
		t.Errorf("expected '7', got %q", extractScalar(item["tally"]))
	}
}

func TestUpdate_setIfNotExists_missing(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
	}
	err := applyUpdateExpression(item,
		"SET hits = if_not_exists(hits, :zero)",
		nil,
		map[string]attrValue{":zero": {"N": "0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["hits"]) != "0" {
		t.Errorf("expected '0' for new attribute, got %q", extractScalar(item["hits"]))
	}
}

func TestUpdate_setIfNotExists_existing(t *testing.T) {
	item := Item{
		"id":   attrValue{"S": "1"},
		"hits": attrValue{"N": "42"},
	}
	err := applyUpdateExpression(item,
		"SET hits = if_not_exists(hits, :zero)",
		nil,
		map[string]attrValue{":zero": {"N": "0"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["hits"]) != "42" {
		t.Errorf("expected '42' (kept existing), got %q", extractScalar(item["hits"]))
	}
}

func TestUpdate_setListAppend(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
		"tags": attrValue{"L": []any{
			map[string]any{"S": "a"},
		}},
	}
	err := applyUpdateExpression(item,
		"SET tags = list_append(tags, :new)",
		nil,
		map[string]attrValue{
			":new": {"L": []any{
				map[string]any{"S": "b"},
				map[string]any{"S": "c"},
			}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	list := extractList(item["tags"])
	if len(list) != 3 {
		t.Errorf("expected 3 elements, got %d", len(list))
	}
}

func TestUpdate_setNestedPath(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
		"info": attrValue{"M": map[string]any{
			"nickname": map[string]any{"S": "Alice"},
		}},
	}
	err := applyUpdateExpression(item,
		"SET info.age = :age",
		nil,
		map[string]attrValue{":age": {"N": "30"}},
	)
	if err != nil {
		t.Fatal(err)
	}

	tokens, _ := tokenise("info.age")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)
	val, ok := getByPath(item, path)
	if !ok || extractScalar(val) != "30" {
		t.Error("expected nested attribute to be set")
	}
}

// ---------------------------------------------------------------------------
// REMOVE clause
// ---------------------------------------------------------------------------

func TestUpdate_remove(t *testing.T) {
	item := Item{
		"id":       attrValue{"S": "1"},
		"nickname": attrValue{"S": "Alice"},
		"age":      attrValue{"N": "30"},
	}
	err := applyUpdateExpression(item, "REMOVE age", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := item["age"]; ok {
		t.Error("expected 'age' to be removed")
	}
	if _, ok := item["nickname"]; !ok {
		t.Error("expected 'nickname' to remain")
	}
}

func TestUpdate_removeMultiple(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
		"a":  attrValue{"S": "x"},
		"b":  attrValue{"S": "y"},
		"c":  attrValue{"S": "z"},
	}
	err := applyUpdateExpression(item, "REMOVE a, b", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := item["a"]; ok {
		t.Error("expected 'a' to be removed")
	}
	if _, ok := item["b"]; ok {
		t.Error("expected 'b' to be removed")
	}
	if _, ok := item["c"]; !ok {
		t.Error("expected 'c' to remain")
	}
}

func TestUpdate_removeNonExistent(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
	}
	// REMOVE of non-existent attribute is a no-op
	err := applyUpdateExpression(item, "REMOVE absent", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// ADD clause
// ---------------------------------------------------------------------------

func TestUpdate_addNumber(t *testing.T) {
	item := Item{
		"id":    attrValue{"S": "1"},
		"tally": attrValue{"N": "10"},
	}
	err := applyUpdateExpression(item,
		"ADD tally :inc",
		nil,
		map[string]attrValue{":inc": {"N": "5"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["tally"]) != "15" {
		t.Errorf("expected '15', got %q", extractScalar(item["tally"]))
	}
}

func TestUpdate_addNumberNewAttribute(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
	}
	err := applyUpdateExpression(item,
		"ADD tally :v",
		nil,
		map[string]attrValue{":v": {"N": "7"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["tally"]) != "7" {
		t.Errorf("expected '7', got %q", extractScalar(item["tally"]))
	}
}

func TestUpdate_addStringSet(t *testing.T) {
	item := Item{
		"id":   attrValue{"S": "1"},
		"tags": attrValue{"SS": []any{"a", "b"}},
	}
	err := applyUpdateExpression(item,
		"ADD tags :new",
		nil,
		map[string]attrValue{":new": {"SS": []any{"b", "c"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// Should be union: {"a", "b", "c"}
	raw, ok := item["tags"]["SS"]
	if !ok {
		t.Fatal("expected SS attribute")
	}
	arr, ok := raw.([]any)
	if !ok {
		t.Fatal("expected array")
	}
	if len(arr) != 3 {
		t.Errorf("expected 3 elements after union, got %d", len(arr))
	}
}

func TestUpdate_addNewSet(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
	}
	err := applyUpdateExpression(item,
		"ADD tags :v",
		nil,
		map[string]attrValue{":v": {"SS": []any{"x", "y"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if attrType(item["tags"]) != "SS" {
		t.Errorf("expected SS type, got %s", attrType(item["tags"]))
	}
}

// ---------------------------------------------------------------------------
// DELETE clause
// ---------------------------------------------------------------------------

func TestUpdate_deleteSetElements(t *testing.T) {
	item := Item{
		"id":   attrValue{"S": "1"},
		"tags": attrValue{"SS": []any{"a", "b", "c"}},
	}
	err := applyUpdateExpression(item,
		"DELETE tags :rem",
		nil,
		map[string]attrValue{":rem": {"SS": []any{"b"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	raw := item["tags"]["SS"]
	arr := raw.([]any)
	if len(arr) != 2 {
		t.Errorf("expected 2 elements after delete, got %d", len(arr))
	}
}

func TestUpdate_deleteAllSetElements(t *testing.T) {
	item := Item{
		"id":   attrValue{"S": "1"},
		"tags": attrValue{"SS": []any{"a"}},
	}
	err := applyUpdateExpression(item,
		"DELETE tags :rem",
		nil,
		map[string]attrValue{":rem": {"SS": []any{"a"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	// DynamoDB removes the attribute when the set becomes empty
	if _, ok := item["tags"]; ok {
		t.Error("expected empty set attribute to be removed")
	}
}

func TestUpdate_deleteFromNonExistent(t *testing.T) {
	item := Item{
		"id": attrValue{"S": "1"},
	}
	// DELETE from non-existent attribute is a no-op
	err := applyUpdateExpression(item,
		"DELETE tags :rem",
		nil,
		map[string]attrValue{":rem": {"SS": []any{"a"}}},
	)
	if err != nil {
		t.Fatal(err)
	}
}

// ---------------------------------------------------------------------------
// Mixed clauses
// ---------------------------------------------------------------------------

func TestUpdate_mixedSetRemove(t *testing.T) {
	item := Item{
		"id":       attrValue{"S": "1"},
		"nickname": attrValue{"S": "Alice"},
		"legacy":   attrValue{"S": "superseded"},
	}
	err := applyUpdateExpression(item,
		"SET nickname = :n REMOVE legacy",
		nil,
		map[string]attrValue{":n": {"S": "Bob"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["nickname"]) != "Bob" {
		t.Errorf("expected 'Bob', got %q", extractScalar(item["nickname"]))
	}
	if _, ok := item["legacy"]; ok {
		t.Error("expected 'legacy' to be removed")
	}
}

func TestUpdate_mixedSetAddDelete(t *testing.T) {
	item := Item{
		"id":    attrValue{"S": "1"},
		"tally": attrValue{"N": "10"},
		"tags":  attrValue{"SS": []any{"a", "b"}},
	}
	err := applyUpdateExpression(item,
		"SET tally = tally + :inc ADD tags :new DELETE tags :rem",
		nil,
		map[string]attrValue{
			":inc": {"N": "5"},
			":new": {"SS": []any{"c"}},
			":rem": {"SS": []any{"a"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["tally"]) != "15" {
		t.Errorf("expected tally '15', got %q", extractScalar(item["tally"]))
	}
}

// ---------------------------------------------------------------------------
// Compilation errors
// ---------------------------------------------------------------------------

func TestUpdate_invalidClause(t *testing.T) {
	_, err := compileUpdate("INVALID x = :v", nil, nil)
	if err == nil {
		t.Error("expected error for invalid clause keyword")
	}
}

func TestUpdate_missingPlaceholder(t *testing.T) {
	err := applyUpdateExpression(Item{}, "SET a = :missing", nil, map[string]attrValue{})
	if err == nil {
		t.Error("expected error for missing placeholder")
	}
}
