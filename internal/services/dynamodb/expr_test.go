package dynamodb

import (
	"testing"
)

// ---------------------------------------------------------------------------
// Tokeniser tests
// ---------------------------------------------------------------------------

func TestTokenise_simpleComparison(t *testing.T) {
	tokens, err := tokenise("price = :p")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// price, =, :p, EOF
	if len(tokens) != 4 {
		t.Fatalf("expected 4 tokens, got %d: %v", len(tokens), tokens)
	}
	assertToken(t, tokens[0], tokIdent, "price")
	assertToken(t, tokens[1], tokEq, "=")
	assertToken(t, tokens[2], tokPlaceholder, ":p")
	assertToken(t, tokens[3], tokEOF, "")
}

func TestTokenise_comparisonOperators(t *testing.T) {
	tokens, err := tokenise("a <> b <= c < d >= e > f")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// a, <>, b, <=, c, <, d, >=, e, >, f, EOF
	expected := []struct {
		kind tokenKind
		val  string
	}{
		{tokIdent, "a"}, {tokNeq, "<>"},
		{tokIdent, "b"}, {tokLE, "<="},
		{tokIdent, "c"}, {tokLT, "<"},
		{tokIdent, "d"}, {tokGE, ">="},
		{tokIdent, "e"}, {tokGT, ">"},
		{tokIdent, "f"}, {tokEOF, ""},
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, exp := range expected {
		assertToken(t, tokens[i], exp.kind, exp.val)
	}
}

func TestTokenise_keywords(t *testing.T) {
	tokens, err := tokenise("AND or Not BETWEEN in SET remove ADD delete")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []tokenKind{
		tokAND, tokOR, tokNOT, tokBETWEEN, tokIN,
		tokSET, tokREMOVE, tokADD, tokDELETE,
		tokEOF,
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, exp := range expected {
		if tokens[i].kind != exp {
			t.Errorf("token %d: expected kind %s, got %s (%q)", i, tokenKindName(exp), tokenKindName(tokens[i].kind), tokens[i].val)
		}
	}
}

func TestTokenise_aliasAndPlaceholder(t *testing.T) {
	tokens, err := tokenise("#name = :val")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToken(t, tokens[0], tokAlias, "#name")
	assertToken(t, tokens[1], tokEq, "=")
	assertToken(t, tokens[2], tokPlaceholder, ":val")
}

func TestTokenise_pathWithIndex(t *testing.T) {
	tokens, err := tokenise("list[0].name")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []struct {
		kind tokenKind
		val  string
	}{
		{tokIdent, "list"}, {tokLBracket, "["}, {tokNumber, "0"},
		{tokRBracket, "]"}, {tokDot, "."}, {tokIdent, "name"},
		{tokEOF, ""},
	}
	if len(tokens) != len(expected) {
		t.Fatalf("expected %d tokens, got %d", len(expected), len(tokens))
	}
	for i, exp := range expected {
		assertToken(t, tokens[i], exp.kind, exp.val)
	}
}

func TestTokenise_arithmetic(t *testing.T) {
	tokens, err := tokenise("x + y - z")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToken(t, tokens[0], tokIdent, "x")
	assertToken(t, tokens[1], tokPlus, "+")
	assertToken(t, tokens[2], tokIdent, "y")
	assertToken(t, tokens[3], tokMinus, "-")
	assertToken(t, tokens[4], tokIdent, "z")
}

func TestTokenise_emptyAlias(t *testing.T) {
	_, err := tokenise("# = :v")
	if err == nil {
		t.Fatal("expected error for empty alias")
	}
}

func TestTokenise_emptyPlaceholder(t *testing.T) {
	_, err := tokenise("a = :")
	if err == nil {
		t.Fatal("expected error for empty placeholder")
	}
}

func TestTokenise_unexpectedChar(t *testing.T) {
	_, err := tokenise("a @ b")
	if err == nil {
		t.Fatal("expected error for unexpected character")
	}
}

func TestTokenise_parens(t *testing.T) {
	tokens, err := tokenise("(a)")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertToken(t, tokens[0], tokLParen, "(")
	assertToken(t, tokens[1], tokIdent, "a")
	assertToken(t, tokens[2], tokRParen, ")")
}

// ---------------------------------------------------------------------------
// Attribute value helpers tests
// ---------------------------------------------------------------------------

func TestAttrValueEqual_sameString(t *testing.T) {
	a := attrValue{"S": "hello"}
	b := attrValue{"S": "hello"}
	if !attrValueEqual(a, b) {
		t.Error("expected equal")
	}
}

func TestAttrValueEqual_differentString(t *testing.T) {
	a := attrValue{"S": "hello"}
	b := attrValue{"S": "world"}
	if attrValueEqual(a, b) {
		t.Error("expected not equal")
	}
}

func TestAttrValueEqual_differentTypes(t *testing.T) {
	a := attrValue{"S": "42"}
	b := attrValue{"N": "42"}
	if attrValueEqual(a, b) {
		t.Error("expected not equal for different types")
	}
}

func TestAttrValueCompare_numbers(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"1", "2", -1},
		{"5", "5", 0},
		{"10", "3", 1},
		{"-1.5", "2.5", -1},
		{"0", "0", 0},
	}
	for _, tc := range tests {
		a := attrValue{"N": tc.a}
		b := attrValue{"N": tc.b}
		got, err := attrValueCompare(a, b)
		if err != nil {
			t.Errorf("compare(%s, %s): %v", tc.a, tc.b, err)
			continue
		}
		if got != tc.want {
			t.Errorf("compare(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestAttrValueCompare_strings(t *testing.T) {
	a := attrValue{"S": "apple"}
	b := attrValue{"S": "banana"}
	got, err := attrValueCompare(a, b)
	if err != nil {
		t.Fatal(err)
	}
	if got >= 0 {
		t.Error("expected apple < banana")
	}
}

func TestAttrValueCompare_typeMismatch(t *testing.T) {
	a := attrValue{"S": "hello"}
	b := attrValue{"N": "5"}
	_, err := attrValueCompare(a, b)
	if err == nil {
		t.Error("expected error for type mismatch")
	}
}

func TestAttrType(t *testing.T) {
	tests := []struct {
		val  attrValue
		want string
	}{
		{attrValue{"S": "hello"}, "S"},
		{attrValue{"N": "42"}, "N"},
		{attrValue{"BOOL": true}, "BOOL"},
		{attrValue{"NULL": true}, "NULL"},
		{attrValue{"L": []any{}}, "L"},
		{attrValue{"M": map[string]any{}}, "M"},
		{attrValue{"SS": []any{"a", "b"}}, "SS"},
		{attrValue{"NS": []any{"1", "2"}}, "NS"},
	}
	for _, tc := range tests {
		got := attrType(tc.val)
		if got != tc.want {
			t.Errorf("attrType(%v) = %q, want %q", tc.val, got, tc.want)
		}
	}
}

func TestExtractScalar(t *testing.T) {
	if got := extractScalar(attrValue{"S": "hello"}); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
	if got := extractScalar(attrValue{"N": "42"}); got != "42" {
		t.Errorf("expected '42', got %q", got)
	}
}

func TestAttrValueSize(t *testing.T) {
	tests := []struct {
		name string
		val  attrValue
		want int
	}{
		{"string", attrValue{"S": "hello"}, 5},
		{"binary", attrValue{"B": "abc"}, 3},
		{"list", attrValue{"L": []any{"a", "b", "c"}}, 3},
		{"map", attrValue{"M": map[string]any{"a": 1, "b": 2}}, 2},
		{"string set", attrValue{"SS": []any{"a", "b"}}, 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := attrValueSize(tc.val)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("size = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestNumberAttrValue(t *testing.T) {
	v := numberAttrValue(3.14)
	if attrType(v) != "N" {
		t.Errorf("expected N type, got %s", attrType(v))
	}
	if extractScalar(v) != "3.14" {
		t.Errorf("expected 3.14, got %s", extractScalar(v))
	}
}

func TestSetContains(t *testing.T) {
	set := attrValue{"SS": []any{"apple", "banana", "cherry"}}
	needle := attrValue{"S": "banana"}
	if !setContains(set, needle) {
		t.Error("expected set to contain banana")
	}
	missing := attrValue{"S": "grape"}
	if setContains(set, missing) {
		t.Error("expected set to not contain grape")
	}
}

func TestListContains(t *testing.T) {
	list := attrValue{"L": []any{
		map[string]any{"S": "a"},
		map[string]any{"N": "42"},
	}}
	if !listContains(list, attrValue{"S": "a"}) {
		t.Error("expected list to contain 'a'")
	}
	if !listContains(list, attrValue{"N": "42"}) {
		t.Error("expected list to contain 42")
	}
	if listContains(list, attrValue{"S": "z"}) {
		t.Error("expected list to not contain 'z'")
	}
}

// ---------------------------------------------------------------------------
// TokStream tests
// ---------------------------------------------------------------------------

func TestTokStream_peekAndNext(t *testing.T) {
	tokens := []token{
		{tokIdent, "x", 0},
		{tokEq, "=", 2},
		{tokPlaceholder, ":v", 4},
		{tokEOF, "", 6},
	}
	s := newTokStream(tokens)

	// peek does not advance
	if s.peek().kind != tokIdent {
		t.Error("peek should return first token")
	}
	if s.peek().kind != tokIdent {
		t.Error("peek should not advance")
	}

	// next advances
	tok := s.next()
	if tok.kind != tokIdent || tok.val != "x" {
		t.Errorf("expected ident 'x', got %v", tok)
	}
	if s.peek().kind != tokEq {
		t.Error("after next, peek should return second token")
	}
}

func TestTokStream_expect(t *testing.T) {
	tokens := []token{
		{tokEq, "=", 0},
		{tokEOF, "", 1},
	}
	s := newTokStream(tokens)

	tok, err := s.expect(tokEq)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok.kind != tokEq {
		t.Error("expected '=' token")
	}

	_, err = s.expect(tokIdent)
	if err == nil {
		t.Error("expected error for wrong token kind")
	}
}

func TestTokStream_at(t *testing.T) {
	tokens := []token{
		{tokIdent, "x", 0},
		{tokEOF, "", 1},
	}
	s := newTokStream(tokens)

	if !s.at(tokIdent) {
		t.Error("expected at(ident) to be true")
	}
	if !s.at(tokEq, tokIdent) {
		t.Error("expected at(eq|ident) to be true")
	}
	if s.at(tokEq, tokNeq) {
		t.Error("expected at(eq|neq) to be false")
	}
}

// ---------------------------------------------------------------------------
// Resolve helpers tests
// ---------------------------------------------------------------------------

func TestResolveAlias_found(t *testing.T) {
	names := map[string]string{"#n": "name"}
	got, err := resolveAlias("#n", names)
	if err != nil {
		t.Fatal(err)
	}
	if got != "name" {
		t.Errorf("expected 'name', got %q", got)
	}
}

func TestResolveAlias_notFound(t *testing.T) {
	_, err := resolveAlias("#missing", map[string]string{})
	if err == nil {
		t.Error("expected error for missing alias")
	}
}

func TestResolvePlaceholder_found(t *testing.T) {
	values := map[string]attrValue{":v": {"S": "hello"}}
	got, err := resolvePlaceholder(":v", values)
	if err != nil {
		t.Fatal(err)
	}
	if extractScalar(got) != "hello" {
		t.Errorf("expected 'hello', got %q", extractScalar(got))
	}
}

func TestResolvePlaceholder_notFound(t *testing.T) {
	_, err := resolvePlaceholder(":missing", map[string]attrValue{})
	if err == nil {
		t.Error("expected error for missing placeholder")
	}
}

// assertToken is a test helper for token assertions.
func assertToken(t *testing.T, tok token, kind tokenKind, val string) {
	t.Helper()
	if tok.kind != kind {
		t.Errorf("token: expected kind %s, got %s (val=%q)", tokenKindName(kind), tokenKindName(tok.kind), tok.val)
	}
	if tok.val != val {
		t.Errorf("token: expected val %q, got %q", val, tok.val)
	}
}
