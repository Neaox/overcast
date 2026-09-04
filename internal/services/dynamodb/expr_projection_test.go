package dynamodb

import (
	"testing"
)

// ---------------------------------------------------------------------------
// ProjectionExpression tests
// ---------------------------------------------------------------------------

func TestProjection_simpleAttributes(t *testing.T) {
	table := &Table{
		KeySchema: []KeySchemaElement{
			{AttributeName: "id", KeyType: "HASH"},
		},
	}
	proj, err := compileProjection("nickname, age", nil)
	if err != nil {
		t.Fatal(err)
	}

	item := Item{
		"id":       attrValue{"S": "1"},
		"nickname": attrValue{"S": "Alice"},
		"age":      attrValue{"N": "30"},
		"secret":   attrValue{"S": "hidden"},
	}

	result := applyProjection(item, proj, table)

	// Should include id (key), nickname, age but not secret
	if _, ok := result["id"]; !ok {
		t.Error("expected key attribute 'id' to be included")
	}
	if _, ok := result["nickname"]; !ok {
		t.Error("expected projected 'nickname' to be included")
	}
	if _, ok := result["age"]; !ok {
		t.Error("expected projected 'age' to be included")
	}
	if _, ok := result["secret"]; ok {
		t.Error("expected 'secret' to be excluded")
	}
}

func TestProjection_nilProjection(t *testing.T) {
	table := &Table{
		KeySchema: []KeySchemaElement{
			{AttributeName: "id", KeyType: "HASH"},
		},
	}
	item := Item{
		"id":       attrValue{"S": "1"},
		"nickname": attrValue{"S": "Alice"},
	}

	// Nil projection should return all attributes
	result := applyProjection(item, nil, table)
	if len(result) != 2 {
		t.Errorf("expected all 2 attributes, got %d", len(result))
	}
}

func TestProjection_emptyExpression(t *testing.T) {
	proj, err := compileProjection("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if proj != nil {
		t.Error("expected nil projection for empty expression")
	}
}

func TestProjection_withAlias(t *testing.T) {
	table := &Table{
		KeySchema: []KeySchemaElement{
			{AttributeName: "id", KeyType: "HASH"},
		},
	}
	names := map[string]string{"#s": "status"}
	proj, err := compileProjection("#s", names)
	if err != nil {
		t.Fatal(err)
	}

	item := Item{
		"id":     attrValue{"S": "1"},
		"status": attrValue{"S": "active"},
		"other":  attrValue{"S": "data"},
	}

	result := applyProjection(item, proj, table)
	if _, ok := result["status"]; !ok {
		t.Error("expected aliased 'status' to be included")
	}
	if _, ok := result["other"]; ok {
		t.Error("expected 'other' to be excluded")
	}
}

func TestProjection_alwaysIncludesKeys(t *testing.T) {
	table := &Table{
		KeySchema: []KeySchemaElement{
			{AttributeName: "pk", KeyType: "HASH"},
			{AttributeName: "sk", KeyType: "RANGE"},
		},
	}
	proj, err := compileProjection("payload", nil)
	if err != nil {
		t.Fatal(err)
	}

	item := Item{
		"pk":      attrValue{"S": "p1"},
		"sk":      attrValue{"S": "s1"},
		"payload": attrValue{"S": "value"},
	}

	result := applyProjection(item, proj, table)
	if _, ok := result["pk"]; !ok {
		t.Error("expected hash key 'pk' to always be included")
	}
	if _, ok := result["sk"]; !ok {
		t.Error("expected sort key 'sk' to always be included")
	}
	if _, ok := result["payload"]; !ok {
		t.Error("expected projected 'payload' to be included")
	}
}

func TestProjection_nestedPath(t *testing.T) {
	table := &Table{
		KeySchema: []KeySchemaElement{
			{AttributeName: "id", KeyType: "HASH"},
		},
	}
	proj, err := compileProjection("info.nickname", nil)
	if err != nil {
		t.Fatal(err)
	}

	item := Item{
		"id": attrValue{"S": "1"},
		"info": attrValue{"M": map[string]any{
			"nickname": map[string]any{"S": "Alice"},
			"secret":   map[string]any{"S": "hidden"},
		}},
	}

	result := applyProjection(item, proj, table)
	if _, ok := result["id"]; !ok {
		t.Error("expected key to be included")
	}
	// The nested path should be reconstructed
	if _, ok := result["info"]; !ok {
		t.Error("expected 'info' parent to exist in result")
	}
}

func TestProjection_missingAttribute(t *testing.T) {
	table := &Table{
		KeySchema: []KeySchemaElement{
			{AttributeName: "id", KeyType: "HASH"},
		},
	}
	proj, err := compileProjection("absent", nil)
	if err != nil {
		t.Fatal(err)
	}

	item := Item{
		"id":       attrValue{"S": "1"},
		"nickname": attrValue{"S": "Alice"},
	}

	result := applyProjection(item, proj, table)
	if _, ok := result["absent"]; ok {
		t.Error("expected a projected attribute the item lacks to not appear in result")
	}
	// But key should still be there
	if _, ok := result["id"]; !ok {
		t.Error("expected key to always be included")
	}
}
