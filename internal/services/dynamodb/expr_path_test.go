package dynamodb

import (
	"testing"
)

// ---------------------------------------------------------------------------
// parsePath tests
// ---------------------------------------------------------------------------

func TestParsePath_simpleAttribute(t *testing.T) {
	tokens, _ := tokenise("name")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "name" {
		t.Errorf("expected 'name', got %q", path.String())
	}
	if !path.isSimple() {
		t.Error("expected simple path")
	}
	if path.topLevel() != "name" {
		t.Errorf("expected topLevel 'name', got %q", path.topLevel())
	}
}

func TestParsePath_nestedDot(t *testing.T) {
	tokens, _ := tokenise("a.b.c")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "a.b.c" {
		t.Errorf("expected 'a.b.c', got %q", path.String())
	}
	if path.isSimple() {
		t.Error("expected nested path")
	}
	if path.topLevel() != "a" {
		t.Errorf("expected topLevel 'a', got %q", path.topLevel())
	}
}

func TestParsePath_listIndex(t *testing.T) {
	tokens, _ := tokenise("items[0]")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "items[0]" {
		t.Errorf("expected 'items[0]', got %q", path.String())
	}
}

func TestParsePath_complexPath(t *testing.T) {
	tokens, _ := tokenise("data[2].nested.list[0].val")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "data[2].nested.list[0].val" {
		t.Errorf("expected 'data[2].nested.list[0].val', got %q", path.String())
	}
}

func TestParsePath_alias(t *testing.T) {
	tokens, _ := tokenise("#attr.name")
	s := newTokStream(tokens)
	names := map[string]string{"#attr": "myattr"}
	path, err := parsePath(s, names)
	if err != nil {
		t.Fatal(err)
	}
	if path.topLevel() != "myattr" {
		t.Errorf("expected topLevel 'myattr', got %q", path.topLevel())
	}
}

func TestParsePath_keywordAsName(t *testing.T) {
	// DynamoDB allows reserved words as attribute names
	tokens, _ := tokenise("SET")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.topLevel() != "SET" {
		t.Errorf("expected topLevel 'SET', got %q", path.topLevel())
	}
}

// ---------------------------------------------------------------------------
// getByPath tests
// ---------------------------------------------------------------------------

func TestGetByPath_topLevel(t *testing.T) {
	item := Item{
		"name": attrValue{"S": "Alice"},
	}
	tokens, _ := tokenise("name")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	val, ok := getByPath(item, path)
	if !ok {
		t.Fatal("expected path to exist")
	}
	if extractScalar(val) != "Alice" {
		t.Errorf("expected 'Alice', got %q", extractScalar(val))
	}
}

func TestGetByPath_nested(t *testing.T) {
	item := Item{
		"info": attrValue{"M": map[string]any{
			"address": map[string]any{"M": map[string]any{
				"city": map[string]any{"S": "London"},
			}},
		}},
	}
	tokens, _ := tokenise("info.address.city")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	val, ok := getByPath(item, path)
	if !ok {
		t.Fatal("expected path to exist")
	}
	if extractScalar(val) != "London" {
		t.Errorf("expected 'London', got %q", extractScalar(val))
	}
}

func TestGetByPath_listIndex(t *testing.T) {
	item := Item{
		"tags": attrValue{"L": []any{
			map[string]any{"S": "first"},
			map[string]any{"S": "second"},
		}},
	}
	tokens, _ := tokenise("tags[1]")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	val, ok := getByPath(item, path)
	if !ok {
		t.Fatal("expected path to exist")
	}
	if extractScalar(val) != "second" {
		t.Errorf("expected 'second', got %q", extractScalar(val))
	}
}

func TestGetByPath_notFound(t *testing.T) {
	item := Item{
		"name": attrValue{"S": "Alice"},
	}
	tokens, _ := tokenise("age")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	_, ok := getByPath(item, path)
	if ok {
		t.Error("expected path to not exist")
	}
}

func TestGetByPath_indexOutOfBounds(t *testing.T) {
	item := Item{
		"tags": attrValue{"L": []any{
			map[string]any{"S": "only"},
		}},
	}
	tokens, _ := tokenise("tags[5]")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	_, ok := getByPath(item, path)
	if ok {
		t.Error("expected path to not exist for out of bounds index")
	}
}

// ---------------------------------------------------------------------------
// setByPath tests
// ---------------------------------------------------------------------------

func TestSetByPath_topLevel(t *testing.T) {
	item := Item{}
	tokens, _ := tokenise("name")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	err := setByPath(item, path, attrValue{"S": "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["name"]) != "Alice" {
		t.Errorf("expected 'Alice', got %q", extractScalar(item["name"]))
	}
}

func TestSetByPath_nested(t *testing.T) {
	item := Item{
		"info": attrValue{"M": map[string]any{
			"name": map[string]any{"S": "Alice"},
		}},
	}
	tokens, _ := tokenise("info.age")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	err := setByPath(item, path, attrValue{"N": "30"})
	if err != nil {
		t.Fatal(err)
	}

	// Navigate to verify
	tokens2, _ := tokenise("info.age")
	s2 := newTokStream(tokens2)
	path2, _ := parsePath(s2, nil)
	val, ok := getByPath(item, path2)
	if !ok || extractScalar(val) != "30" {
		t.Errorf("expected nested value '30', got ok=%v val=%v", ok, val)
	}
}

func TestSetByPath_listIndex(t *testing.T) {
	item := Item{
		"tags": attrValue{"L": []any{
			map[string]any{"S": "old"},
		}},
	}
	tokens, _ := tokenise("tags[0]")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	err := setByPath(item, path, attrValue{"S": "new"})
	if err != nil {
		t.Fatal(err)
	}

	// Verify
	tokens2, _ := tokenise("tags[0]")
	s2 := newTokStream(tokens2)
	path2, _ := parsePath(s2, nil)
	val, ok := getByPath(item, path2)
	if !ok || extractScalar(val) != "new" {
		t.Error("expected updated list element")
	}
}

// ---------------------------------------------------------------------------
// removeByPath tests
// ---------------------------------------------------------------------------

func TestRemoveByPath_topLevel(t *testing.T) {
	item := Item{
		"name": attrValue{"S": "Alice"},
		"age":  attrValue{"N": "30"},
	}
	tokens, _ := tokenise("name")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	if !removeByPath(item, path) {
		t.Error("expected remove to succeed")
	}
	if _, ok := item["name"]; ok {
		t.Error("expected 'name' to be removed")
	}
	if _, ok := item["age"]; !ok {
		t.Error("expected 'age' to remain")
	}
}

func TestRemoveByPath_notFound(t *testing.T) {
	item := Item{}
	tokens, _ := tokenise("missing")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	if removeByPath(item, path) {
		t.Error("expected remove to return false for missing path")
	}
}

func TestRemoveByPath_listElement(t *testing.T) {
	item := Item{
		"tags": attrValue{"L": []any{
			map[string]any{"S": "a"},
			map[string]any{"S": "b"},
			map[string]any{"S": "c"},
		}},
	}
	tokens, _ := tokenise("tags[1]")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	if !removeByPath(item, path) {
		t.Error("expected remove to succeed")
	}
	list := extractList(item["tags"])
	if len(list) != 2 {
		t.Errorf("expected 2 elements after remove, got %d", len(list))
	}
}
