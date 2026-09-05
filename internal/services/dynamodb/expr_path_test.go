package dynamodb

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// parsePath tests
// ---------------------------------------------------------------------------

func TestParsePath_simpleAttribute(t *testing.T) {
	tokens, _ := tokenise("nickname")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "nickname" {
		t.Errorf("expected 'nickname', got %q", path.String())
	}
	if !path.isSimple() {
		t.Error("expected simple path")
	}
	if path.topLevel() != "nickname" {
		t.Errorf("expected topLevel 'nickname', got %q", path.topLevel())
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
	tokens, _ := tokenise("entries[0]")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "entries[0]" {
		t.Errorf("expected 'entries[0]', got %q", path.String())
	}
}

func TestParsePath_complexPath(t *testing.T) {
	tokens, _ := tokenise("payload[2].nested.entries[0].val")
	s := newTokStream(tokens)
	path, err := parsePath(s, nil)
	if err != nil {
		t.Fatal(err)
	}
	if path.String() != "payload[2].nested.entries[0].val" {
		t.Errorf("expected 'payload[2].nested.entries[0].val', got %q", path.String())
	}
}

func TestParsePath_alias(t *testing.T) {
	tokens, _ := tokenise("#attr.nickname")
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
	// Given: a clause keyword in an attribute-name position. SET is on the
	// reserved-word list, so DynamoDB refuses it as a bare attribute name.
	tokens, _ := tokenise("SET")
	s := newTokStream(tokens)

	// When: it is parsed as a path
	_, err := parsePath(s, nil)

	// Then: it is rejected as a reserved word
	var rw *reservedWordError
	if !errors.As(err, &rw) {
		t.Fatalf("expected a reservedWordError, got %v", err)
	}
	if rw.word != "SET" {
		t.Errorf("keyword = %q, want \"SET\"", rw.word)
	}
}

func TestParsePath_reservedWordViaAlias(t *testing.T) {
	// Given: a reserved word reached through an ExpressionAttributeNames alias
	tokens, _ := tokenise("#s")
	s := newTokStream(tokens)

	// When: it is parsed as a path
	path, err := parsePath(s, map[string]string{"#s": "size"})

	// Then: it resolves — the alias is the documented escape hatch
	if err != nil {
		t.Fatal(err)
	}
	if path.topLevel() != "size" {
		t.Errorf("expected topLevel 'size', got %q", path.topLevel())
	}
}

// ---------------------------------------------------------------------------
// getByPath tests
// ---------------------------------------------------------------------------

func TestGetByPath_topLevel(t *testing.T) {
	item := Item{
		"nickname": attrValue{"S": "Alice"},
	}
	tokens, _ := tokenise("nickname")
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
		"nickname": attrValue{"S": "Alice"},
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
	tokens, _ := tokenise("nickname")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	err := setByPath(item, path, attrValue{"S": "Alice"})
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(item["nickname"]) != "Alice" {
		t.Errorf("expected 'Alice', got %q", extractScalar(item["nickname"]))
	}
}

func TestSetByPath_nested(t *testing.T) {
	item := Item{
		"info": attrValue{"M": map[string]any{
			"nickname": map[string]any{"S": "Alice"},
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
		"nickname": attrValue{"S": "Alice"},
		"age":      attrValue{"N": "30"},
	}
	tokens, _ := tokenise("nickname")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	if !removeByPath(item, path) {
		t.Error("expected remove to succeed")
	}
	if _, ok := item["nickname"]; ok {
		t.Error("expected 'nickname' to be removed")
	}
	if _, ok := item["age"]; !ok {
		t.Error("expected 'age' to remain")
	}
}

func TestRemoveByPath_notFound(t *testing.T) {
	item := Item{}
	tokens, _ := tokenise("absent")
	s := newTokStream(tokens)
	path, _ := parsePath(s, nil)

	if removeByPath(item, path) {
		t.Error("expected remove to return false for a path the item lacks")
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
