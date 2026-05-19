package dynamodb

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Filter/Condition expression compilation and evaluation tests
// ---------------------------------------------------------------------------

// makeItem is a helper that builds an Item from key-value pairs.
func makeItem(attrs map[string]attrValue) Item {
	return Item(attrs)
}

// ---------------------------------------------------------------------------
// Simple comparisons
// ---------------------------------------------------------------------------

func TestFilter_equalityMatch(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "active"},
	})
	f, err := compileFilter("status = :s", nil, map[string]attrValue{
		":s": {"S": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := evalFilter(f, item)
	if err != nil {
		t.Fatal(err)
	}
	if !result {
		t.Error("expected match for equal values")
	}
}

func TestFilter_equalityNoMatch(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "inactive"},
	})
	f, err := compileFilter("status = :s", nil, map[string]attrValue{
		":s": {"S": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected no match for different values")
	}
}

func TestFilter_notEqual(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "active"},
	})
	f, err := compileFilter("status <> :s", nil, map[string]attrValue{
		":s": {"S": "inactive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected match for not-equal values")
	}
}

func TestFilter_lessThan(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"price": {"N": "10"},
	})
	f, err := compileFilter("price < :p", nil, map[string]attrValue{
		":p": {"N": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 10 < 20")
	}
}

func TestFilter_greaterThanOrEqual(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"price": {"N": "20"},
	})
	f, err := compileFilter("price >= :p", nil, map[string]attrValue{
		":p": {"N": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 20 >= 20")
	}
}

func TestFilter_lessThanOrEqual(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"price": {"N": "15"},
	})
	f, err := compileFilter("price <= :p", nil, map[string]attrValue{
		":p": {"N": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 15 <= 20")
	}
}

func TestFilter_greaterThan(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"price": {"N": "25"},
	})
	f, err := compileFilter("price > :p", nil, map[string]attrValue{
		":p": {"N": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 25 > 20")
	}
}

func TestFilter_missingAttribute_equality(t *testing.T) {
	item := makeItem(map[string]attrValue{})
	f, err := compileFilter("status = :s", nil, map[string]attrValue{
		":s": {"S": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected false when attribute is missing")
	}
}

func TestFilter_missingAttribute_notEqual(t *testing.T) {
	item := makeItem(map[string]attrValue{})
	f, err := compileFilter("status <> :s", nil, map[string]attrValue{
		":s": {"S": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected true when attribute is missing and using <>")
	}
}

// ---------------------------------------------------------------------------
// Logical operators
// ---------------------------------------------------------------------------

func TestFilter_and(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"a": {"N": "5"},
		"b": {"N": "10"},
	})
	f, err := compileFilter("a > :lo AND b < :hi", nil, map[string]attrValue{
		":lo": {"N": "3"},
		":hi": {"N": "15"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected AND to be true when both conditions true")
	}
}

func TestFilter_andFalse(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"a": {"N": "5"},
		"b": {"N": "20"},
	})
	f, err := compileFilter("a > :lo AND b < :hi", nil, map[string]attrValue{
		":lo": {"N": "3"},
		":hi": {"N": "15"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected AND to be false when second condition false")
	}
}

func TestFilter_or(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "active"},
	})
	f, err := compileFilter("status = :a OR status = :b", nil, map[string]attrValue{
		":a": {"S": "active"},
		":b": {"S": "pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected OR to be true when first condition true")
	}
}

func TestFilter_orBothFalse(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "deleted"},
	})
	f, err := compileFilter("status = :a OR status = :b", nil, map[string]attrValue{
		":a": {"S": "active"},
		":b": {"S": "pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected OR to be false when both conditions false")
	}
}

func TestFilter_not(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "active"},
	})
	f, err := compileFilter("NOT status = :s", nil, map[string]attrValue{
		":s": {"S": "inactive"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected NOT to negate false to true")
	}
}

func TestFilter_notTrue(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "active"},
	})
	f, err := compileFilter("NOT status = :s", nil, map[string]attrValue{
		":s": {"S": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected NOT to negate true to false")
	}
}

func TestFilter_parentheses(t *testing.T) {
	// (a OR b) AND c — tests that parentheses override default precedence
	item := makeItem(map[string]attrValue{
		"x": {"N": "1"},
		"y": {"N": "2"},
		"z": {"N": "3"},
	})
	f, err := compileFilter("(x = :one OR y = :ten) AND z = :three", nil, map[string]attrValue{
		":one":   {"N": "1"},
		":ten":   {"N": "10"},
		":three": {"N": "3"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected (1=1 OR 2=10) AND 3=3 to be true")
	}
}

func TestFilter_complexPrecedence(t *testing.T) {
	// NOT a = :v OR b = :v  -- NOT binds tighter than OR
	// = NOT(a=1) OR b=1 = true OR true = true
	item := makeItem(map[string]attrValue{
		"a": {"N": "2"},
		"b": {"N": "1"},
	})
	f, err := compileFilter("NOT a = :one OR b = :one", nil, map[string]attrValue{
		":one": {"N": "1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected NOT(2=1) OR 1=1 to be true")
	}
}

// ---------------------------------------------------------------------------
// BETWEEN
// ---------------------------------------------------------------------------

func TestFilter_between(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"price": {"N": "15"},
	})
	f, err := compileFilter("price BETWEEN :lo AND :hi", nil, map[string]attrValue{
		":lo": {"N": "10"},
		":hi": {"N": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 15 BETWEEN 10 AND 20 to be true")
	}
}

func TestFilter_betweenOutOfRange(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"price": {"N": "25"},
	})
	f, err := compileFilter("price BETWEEN :lo AND :hi", nil, map[string]attrValue{
		":lo": {"N": "10"},
		":hi": {"N": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected 25 BETWEEN 10 AND 20 to be false")
	}
}

func TestFilter_betweenInclusive(t *testing.T) {
	// BETWEEN is inclusive on both ends
	item := makeItem(map[string]attrValue{
		"price": {"N": "10"},
	})
	f, err := compileFilter("price BETWEEN :lo AND :hi", nil, map[string]attrValue{
		":lo": {"N": "10"},
		":hi": {"N": "20"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 10 BETWEEN 10 AND 20 to be true (inclusive)")
	}
}

func TestFilter_betweenStrings(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"name": {"S": "banana"},
	})
	f, err := compileFilter("name BETWEEN :lo AND :hi", nil, map[string]attrValue{
		":lo": {"S": "apple"},
		":hi": {"S": "cherry"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 'banana' BETWEEN 'apple' AND 'cherry'")
	}
}

// ---------------------------------------------------------------------------
// IN
// ---------------------------------------------------------------------------

func TestFilter_in(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "active"},
	})
	f, err := compileFilter("status IN (:a, :b, :c)", nil, map[string]attrValue{
		":a": {"S": "active"},
		":b": {"S": "pending"},
		":c": {"S": "deleted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 'active' IN ('active','pending','deleted')")
	}
}

func TestFilter_inNoMatch(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "unknown"},
	})
	f, err := compileFilter("status IN (:a, :b)", nil, map[string]attrValue{
		":a": {"S": "active"},
		":b": {"S": "pending"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected 'unknown' not IN ('active','pending')")
	}
}

func TestFilter_inSingleValue(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"n": {"N": "42"},
	})
	f, err := compileFilter("n IN (:v)", nil, map[string]attrValue{
		":v": {"N": "42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 42 IN (42)")
	}
}

// ---------------------------------------------------------------------------
// Functions
// ---------------------------------------------------------------------------

func TestFilter_attributeExists(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"name": {"S": "Alice"},
	})
	f, err := compileFilter("attribute_exists(name)", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected attribute_exists to be true for existing attribute")
	}
}

func TestFilter_attributeExistsFalse(t *testing.T) {
	item := makeItem(map[string]attrValue{})
	f, err := compileFilter("attribute_exists(name)", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected attribute_exists to be false for missing attribute")
	}
}

func TestFilter_attributeNotExists(t *testing.T) {
	item := makeItem(map[string]attrValue{})
	f, err := compileFilter("attribute_not_exists(name)", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected attribute_not_exists to be true for missing attribute")
	}
}

func TestFilter_attributeType(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"name": {"S": "Alice"},
	})
	f, err := compileFilter("attribute_type(name, :t)", nil, map[string]attrValue{
		":t": {"S": "S"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected attribute_type to match S")
	}
}

func TestFilter_attributeTypeMismatch(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"name": {"S": "Alice"},
	})
	f, err := compileFilter("attribute_type(name, :t)", nil, map[string]attrValue{
		":t": {"S": "N"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected attribute_type to not match N for string attribute")
	}
}

func TestFilter_beginsWith(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"name": {"S": "Alice"},
	})
	f, err := compileFilter("begins_with(name, :p)", nil, map[string]attrValue{
		":p": {"S": "Ali"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected begins_with('Alice', 'Ali') to be true")
	}
}

func TestFilter_beginsWithNoMatch(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"name": {"S": "Bob"},
	})
	f, err := compileFilter("begins_with(name, :p)", nil, map[string]attrValue{
		":p": {"S": "Ali"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if result {
		t.Error("expected begins_with('Bob', 'Ali') to be false")
	}
}

func TestFilter_containsString(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"desc": {"S": "hello world"},
	})
	f, err := compileFilter("contains(desc, :s)", nil, map[string]attrValue{
		":s": {"S": "world"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected contains('hello world', 'world') to be true")
	}
}

func TestFilter_containsSet(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"tags": {"SS": []any{"go", "rust", "python"}},
	})
	f, err := compileFilter("contains(tags, :t)", nil, map[string]attrValue{
		":t": {"S": "rust"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected set contains to find 'rust'")
	}
}

func TestFilter_containsList(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"items": {"L": []any{
			map[string]any{"N": "42"},
		}},
	})
	f, err := compileFilter("contains(items, :v)", nil, map[string]attrValue{
		":v": {"N": "42"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected list contains to find 42")
	}
}

func TestFilter_sizeEquals(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"name": {"S": "hello"},
	})
	f, err := compileFilter("size(name) = :s", nil, map[string]attrValue{
		":s": {"N": "5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected size('hello') = 5")
	}
}

func TestFilter_sizeGreaterThan(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"tags": {"L": []any{
			map[string]any{"S": "a"},
			map[string]any{"S": "b"},
			map[string]any{"S": "c"},
		}},
	})
	f, err := compileFilter("size(tags) > :s", nil, map[string]attrValue{
		":s": {"N": "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected size of 3-element list > 2")
	}
}

// ---------------------------------------------------------------------------
// Nested paths in expressions
// ---------------------------------------------------------------------------

func TestFilter_nestedPath(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"info": {"M": map[string]any{
			"rating": map[string]any{"N": "4.5"},
		}},
	})
	f, err := compileFilter("info.rating >= :r", nil, map[string]attrValue{
		":r": {"N": "4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected info.rating >= 4 for rating 4.5")
	}
}

// ---------------------------------------------------------------------------
// ExpressionAttributeNames
// ---------------------------------------------------------------------------

func TestFilter_withAttributeNames(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"status": {"S": "active"},
	})
	names := map[string]string{"#s": "status"}
	f, err := compileFilter("#s = :v", names, map[string]attrValue{
		":v": {"S": "active"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected alias resolution to work")
	}
}

// ---------------------------------------------------------------------------
// Size as operand
// ---------------------------------------------------------------------------

func TestFilter_sizeAsOperand(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"data": {"L": []any{
			map[string]any{"S": "a"},
			map[string]any{"S": "b"},
		}},
	})
	f, err := compileFilter("size(data) = :two", nil, map[string]attrValue{
		":two": {"N": "2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected size(data) = 2 for 2-element list")
	}
}

// ---------------------------------------------------------------------------
// Compilation errors
// ---------------------------------------------------------------------------

func TestFilter_missingPlaceholder(t *testing.T) {
	_, err := compileFilter("status = :missing", nil, map[string]attrValue{})
	if err == nil {
		t.Error("expected error for missing placeholder")
	}
}

func TestFilter_missingAlias(t *testing.T) {
	_, err := compileFilter("#missing = :v", nil, map[string]attrValue{
		":v": {"S": "x"},
	})
	if err == nil {
		t.Error("expected error for missing alias")
	}
}

func TestFilter_trailingTokens(t *testing.T) {
	_, err := compileFilter("a = :v extra", nil, map[string]attrValue{
		":v": {"S": "x"},
	})
	if err == nil {
		t.Error("expected error for trailing tokens")
	}
}

// ---------------------------------------------------------------------------
// Regression: "size" and "contains" as attribute names
// ---------------------------------------------------------------------------

func TestFilter_sizeAsAttributeName(t *testing.T) {
	// "size" should be treated as an attribute name when not followed by '('
	item := makeItem(map[string]attrValue{
		"size": {"S": "large"},
	})
	f, err := compileFilter("size = :s", nil, map[string]attrValue{
		":s": {"S": "large"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected size='large' to match when 'size' is an attribute name")
	}
}

func TestFilter_containsAsAttributeName(t *testing.T) {
	item := makeItem(map[string]attrValue{
		"contains": {"S": "yes"},
	})
	f, err := compileFilter("contains = :v", nil, map[string]attrValue{
		":v": {"S": "yes"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected 'contains' as attribute name to work")
	}
}

func TestFilter_sizeAsFunction(t *testing.T) {
	// "size" should still work as a function when followed by '('
	item := makeItem(map[string]attrValue{
		"name": {"S": "hello"},
	})
	f, err := compileFilter("size(name) = :s", nil, map[string]attrValue{
		":s": {"N": "5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, _ := evalFilter(f, item)
	if !result {
		t.Error("expected size('hello') = 5")
	}
}
