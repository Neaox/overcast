package dynamodb

// expr_filter.go implements DynamoDB ConditionExpression / FilterExpression
// compilation and evaluation.
//
// Supported features (full DynamoDB expression grammar):
//   - Comparisons: =, <>, <, <=, >, >= (with type-aware comparison)
//   - Logical: AND, OR, NOT, parentheses
//   - BETWEEN val1 AND val2
//   - IN (val1, val2, ...)
//   - Functions: attribute_exists, attribute_not_exists, attribute_type,
//     begins_with, contains, size
//   - Nested document paths: a.b.c, a[0].b
//   - Operands: document paths and :placeholder values
//
// Grammar (operator precedence, lowest to highest):
//   expr     → or_expr
//   or_expr  → and_expr ( OR and_expr )*
//   and_expr → not_expr ( AND not_expr )*
//   not_expr → NOT not_expr | primary
//   primary  → comparison | function_call | BETWEEN | IN | '(' expr ')'

import (
	"fmt"
	"strings"
)

// filterExpr is a compiled, evaluatable filter/condition expression.
type filterExpr interface {
	eval(item Item) (bool, error)
}

// compileFilter parses and compiles a DynamoDB filter/condition expression.
func compileFilter(
	expr string,
	attrNames map[string]string,
	attrValues map[string]attrValue,
) (filterExpr, error) {
	tokens, err := tokenise(expr)
	if err != nil {
		return nil, err
	}
	s := newTokStream(tokens)
	f, err := parseFilterOr(s, attrNames, attrValues)
	if err != nil {
		return nil, err
	}
	if !s.at(tokEOF) {
		return nil, fmt.Errorf("unexpected token %q at position %d after expression", s.peek().val, s.peek().pos)
	}
	return f, nil
}

// evalFilter runs a compiled filter against an item.
func evalFilter(f filterExpr, item Item) (bool, error) {
	return f.eval(item)
}

// ---------------------------------------------------------------------------
// AST nodes
// ---------------------------------------------------------------------------

type filterAnd struct{ left, right filterExpr }
type filterOr struct{ left, right filterExpr }
type filterNot struct{ inner filterExpr }

func (e *filterAnd) eval(item Item) (bool, error) {
	l, err := e.left.eval(item)
	if err != nil || !l {
		return false, err
	}
	return e.right.eval(item)
}

func (e *filterOr) eval(item Item) (bool, error) {
	l, err := e.left.eval(item)
	if err != nil {
		return false, err
	}
	if l {
		return true, nil
	}
	return e.right.eval(item)
}

func (e *filterNot) eval(item Item) (bool, error) {
	v, err := e.inner.eval(item)
	if err != nil {
		return false, err
	}
	return !v, nil
}

// filterCompare handles =, <>, <, <=, >, >=.
type filterCompare struct {
	left  operand
	right operand
	op    tokenKind
}

func (e *filterCompare) eval(item Item) (bool, error) {
	lv, lok := e.left.resolve(item)
	rv, rok := e.right.resolve(item)

	// For equality with missing attributes.
	if !lok || !rok {
		//exhaustive:ignore
		switch e.op {
		case tokEq:
			return false, nil
		case tokNeq:
			// If both missing, they're "equal" (both don't exist).
			if !lok && !rok {
				return false, nil
			}
			return true, nil
		default:
			return false, nil
		}
	}

	//exhaustive:ignore
	switch e.op {
	case tokEq:
		return attrValueEqual(lv, rv), nil
	case tokNeq:
		return !attrValueEqual(lv, rv), nil
	case tokLT, tokLE, tokGT, tokGE:
		cmp, err := attrValueCompare(lv, rv)
		if err != nil {
			return false, err
		}
		//exhaustive:ignore
		switch e.op {
		case tokLT:
			return cmp < 0, nil
		case tokLE:
			return cmp <= 0, nil
		case tokGT:
			return cmp > 0, nil
		case tokGE:
			return cmp >= 0, nil
		default:
			return false, fmt.Errorf("unknown comparison operator: %v", e.op)
		}
	default:
		return false, fmt.Errorf("unknown comparison operator: %v", e.op)
	}
}

// filterBetween handles path BETWEEN val1 AND val2.
type filterBetween struct {
	operand operand
	low     operand
	high    operand
}

func (e *filterBetween) eval(item Item) (bool, error) {
	v, ok := e.operand.resolve(item)
	if !ok {
		return false, nil
	}
	lo, lok := e.low.resolve(item)
	hi, hok := e.high.resolve(item)
	if !lok || !hok {
		return false, nil
	}
	cmpLo, err := attrValueCompare(v, lo)
	if err != nil {
		return false, err
	}
	cmpHi, err := attrValueCompare(v, hi)
	if err != nil {
		return false, err
	}
	return cmpLo >= 0 && cmpHi <= 0, nil
}

// filterIn handles path IN (val1, val2, ...).
type filterIn struct {
	operand operand
	list    []operand
}

func (e *filterIn) eval(item Item) (bool, error) {
	v, ok := e.operand.resolve(item)
	if !ok {
		return false, nil
	}
	for _, candidate := range e.list {
		cv, cok := candidate.resolve(item)
		if cok && attrValueEqual(v, cv) {
			return true, nil
		}
	}
	return false, nil
}

// Function expressions.

type filterAttrExists struct {
	path   docPath
	negate bool // attribute_not_exists
}

func (e *filterAttrExists) eval(item Item) (bool, error) {
	_, ok := getByPath(item, e.path)
	if e.negate {
		return !ok, nil
	}
	return ok, nil
}

type filterAttrType struct {
	path     docPath
	typeCode operand
}

func (e *filterAttrType) eval(item Item) (bool, error) {
	v, ok := getByPath(item, e.path)
	if !ok {
		return false, nil
	}
	tc, tok := e.typeCode.resolve(item)
	if !tok {
		return false, nil
	}
	expected := extractScalar(tc)
	return attrType(v) == expected, nil
}

type filterBeginsWith struct {
	path   operand
	prefix operand
}

func (e *filterBeginsWith) eval(item Item) (bool, error) {
	v, ok := e.path.resolve(item)
	if !ok {
		return false, nil
	}
	p, pok := e.prefix.resolve(item)
	if !pok {
		return false, nil
	}
	vs := extractScalar(v)
	ps := extractScalar(p)
	return strings.HasPrefix(vs, ps), nil
}

type filterContains struct {
	path operand
	val  operand
}

func (e *filterContains) eval(item Item) (bool, error) {
	v, ok := e.path.resolve(item)
	if !ok {
		return false, nil
	}
	needle, nok := e.val.resolve(item)
	if !nok {
		return false, nil
	}

	// DynamoDB contains() behaviour:
	// - For String: substring match
	// - For Set (SS/NS/BS): member check
	// - For List (L): element check
	switch attrType(v) {
	case "S":
		return strings.Contains(extractScalar(v), extractScalar(needle)), nil
	case "SS", "NS", "BS":
		return setContains(v, needle), nil
	case "L":
		return listContains(v, needle), nil
	default:
		// For other types, try substring on scalar.
		return strings.Contains(extractScalar(v), extractScalar(needle)), nil
	}
}

type filterSizeCompare struct {
	path  docPath
	op    tokenKind
	right operand
}

func (e *filterSizeCompare) eval(item Item) (bool, error) {
	v, ok := getByPath(item, e.path)
	if !ok {
		return false, nil
	}
	sz, err := attrValueSize(v)
	if err != nil {
		return false, err
	}
	rv, rok := e.right.resolve(item)
	if !rok {
		return false, nil
	}
	sizeVal := numberAttrValue(float64(sz))
	cmp, err := attrValueCompare(sizeVal, rv)
	if err != nil {
		return false, err
	}
	//exhaustive:ignore
	switch e.op {
	case tokEq:
		return cmp == 0, nil
	case tokNeq:
		return cmp != 0, nil
	case tokLT:
		return cmp < 0, nil
	case tokLE:
		return cmp <= 0, nil
	case tokGT:
		return cmp > 0, nil
	case tokGE:
		return cmp >= 0, nil
	default:
		return false, fmt.Errorf("invalid size() comparison operator: %v", e.op)
	}
}

// ---------------------------------------------------------------------------
// Operands (path or value)
// ---------------------------------------------------------------------------

// operand is either a document path reference or a literal value.
type operand interface {
	resolve(item Item) (attrValue, bool)
}

type pathOperand struct {
	path docPath
}

func (o *pathOperand) resolve(item Item) (attrValue, bool) {
	return getByPath(item, o.path)
}

type valueOperand struct {
	val attrValue
}

func (o *valueOperand) resolve(_ Item) (attrValue, bool) {
	return o.val, true
}

type sizeOperand struct {
	path docPath
}

func (o *sizeOperand) resolve(item Item) (attrValue, bool) {
	v, ok := getByPath(item, o.path)
	if !ok {
		return nil, false
	}
	sz, err := attrValueSize(v)
	if err != nil {
		return nil, false
	}
	return numberAttrValue(float64(sz)), true
}

// ---------------------------------------------------------------------------
// Parser
// ---------------------------------------------------------------------------

func parseFilterOr(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	left, err := parseFilterAnd(s, names, values)
	if err != nil {
		return nil, err
	}
	for s.at(tokOR) {
		s.next()
		right, err := parseFilterAnd(s, names, values)
		if err != nil {
			return nil, err
		}
		left = &filterOr{left: left, right: right}
	}
	return left, nil
}

func parseFilterAnd(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	left, err := parseFilterNot(s, names, values)
	if err != nil {
		return nil, err
	}
	for s.at(tokAND) {
		s.next()
		right, err := parseFilterNot(s, names, values)
		if err != nil {
			return nil, err
		}
		left = &filterAnd{left: left, right: right}
	}
	return left, nil
}

func parseFilterNot(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	if s.at(tokNOT) {
		s.next()
		inner, err := parseFilterNot(s, names, values)
		if err != nil {
			return nil, err
		}
		return &filterNot{inner: inner}, nil
	}
	return parseFilterPrimary(s, names, values)
}

func parseFilterPrimary(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	// Parenthesised expression.
	if s.at(tokLParen) {
		s.next()
		inner, err := parseFilterOr(s, names, values)
		if err != nil {
			return nil, err
		}
		if _, err := s.expect(tokRParen); err != nil {
			return nil, err
		}
		return inner, nil
	}

	// Function calls — only when the identifier is followed by '('.
	if s.at(tokIdent) && s.pos+1 < len(s.tokens) && s.tokens[s.pos+1].kind == tokLParen {
		fn := strings.ToLower(s.peek().val)
		switch fn {
		case "attribute_exists":
			return parseAttrExists(s, names, false)
		case "attribute_not_exists":
			return parseAttrExists(s, names, true)
		case "attribute_type":
			return parseAttrType(s, names, values)
		case "begins_with":
			return parseBeginsWith(s, names, values)
		case "contains":
			return parseContainsFn(s, names, values)
		case "size":
			return parseSizeComparison(s, names, values)
		}
	}

	// Comparison, BETWEEN, or IN: parse left operand first.
	left, err := parseOperand(s, names, values)
	if err != nil {
		return nil, err
	}

	// BETWEEN.
	if s.at(tokBETWEEN) {
		s.next()
		lo, err := parseOperand(s, names, values)
		if err != nil {
			return nil, err
		}
		if _, err := s.expect(tokAND); err != nil {
			return nil, fmt.Errorf("BETWEEN requires AND: %w", err)
		}
		hi, err := parseOperand(s, names, values)
		if err != nil {
			return nil, err
		}
		return &filterBetween{operand: left, low: lo, high: hi}, nil
	}

	// IN.
	if s.at(tokIN) {
		s.next()
		if _, err := s.expect(tokLParen); err != nil {
			return nil, fmt.Errorf("IN requires '(': %w", err)
		}
		var list []operand
		for {
			op, err := parseOperand(s, names, values)
			if err != nil {
				return nil, err
			}
			list = append(list, op)
			if !s.at(tokComma) {
				break
			}
			s.next() // consume ','
		}
		if _, err := s.expect(tokRParen); err != nil {
			return nil, err
		}
		return &filterIn{operand: left, list: list}, nil
	}

	// Comparison operator.
	if s.at(tokEq, tokNeq, tokLT, tokLE, tokGT, tokGE) {
		op := s.next()
		right, err := parseOperand(s, names, values)
		if err != nil {
			return nil, err
		}
		return &filterCompare{left: left, right: right, op: op.kind}, nil
	}

	return nil, fmt.Errorf("expected comparison operator, BETWEEN, or IN at position %d, got %q", s.peek().pos, s.peek().val)
}

// parseOperand parses an operand: size(path), :placeholder, or document path.
func parseOperand(s *tokStream, names map[string]string, values map[string]attrValue) (operand, error) {
	// size() function as an operand — only when followed by '('.
	if s.at(tokIdent) && strings.ToLower(s.peek().val) == "size" &&
		s.pos+1 < len(s.tokens) && s.tokens[s.pos+1].kind == tokLParen {
		s.next()
		if _, err := s.expect(tokLParen); err != nil {
			return nil, err
		}
		path, err := parsePath(s, names)
		if err != nil {
			return nil, err
		}
		if _, err := s.expect(tokRParen); err != nil {
			return nil, err
		}
		return &sizeOperand{path: path}, nil
	}

	// :placeholder value.
	if s.at(tokPlaceholder) {
		tok := s.next()
		val, err := resolvePlaceholder(tok.val, values)
		if err != nil {
			return nil, err
		}
		return &valueOperand{val: val}, nil
	}

	// Document path.
	path, err := parsePath(s, names)
	if err != nil {
		return nil, err
	}
	return &pathOperand{path: path}, nil
}

// ---------------------------------------------------------------------------
// Function parsers
// ---------------------------------------------------------------------------

func parseAttrExists(s *tokStream, names map[string]string, negate bool) (filterExpr, error) {
	s.next() // consume function name
	if _, err := s.expect(tokLParen); err != nil {
		return nil, err
	}
	path, err := parsePath(s, names)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokRParen); err != nil {
		return nil, err
	}
	return &filterAttrExists{path: path, negate: negate}, nil
}

func parseAttrType(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	s.next() // consume function name
	if _, err := s.expect(tokLParen); err != nil {
		return nil, err
	}
	path, err := parsePath(s, names)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokComma); err != nil {
		return nil, err
	}
	typeOp, err := parseOperand(s, names, values)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokRParen); err != nil {
		return nil, err
	}
	return &filterAttrType{path: path, typeCode: typeOp}, nil
}

func parseBeginsWith(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	s.next() // consume function name
	if _, err := s.expect(tokLParen); err != nil {
		return nil, err
	}
	pathOp, err := parseOperand(s, names, values)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokComma); err != nil {
		return nil, err
	}
	prefixOp, err := parseOperand(s, names, values)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokRParen); err != nil {
		return nil, err
	}
	return &filterBeginsWith{path: pathOp, prefix: prefixOp}, nil
}

func parseContainsFn(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	s.next() // consume function name
	if _, err := s.expect(tokLParen); err != nil {
		return nil, err
	}
	pathOp, err := parseOperand(s, names, values)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokComma); err != nil {
		return nil, err
	}
	valOp, err := parseOperand(s, names, values)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokRParen); err != nil {
		return nil, err
	}
	return &filterContains{path: pathOp, val: valOp}, nil
}

func parseSizeComparison(s *tokStream, names map[string]string, values map[string]attrValue) (filterExpr, error) {
	s.next() // consume 'size'
	if _, err := s.expect(tokLParen); err != nil {
		return nil, err
	}
	path, err := parsePath(s, names)
	if err != nil {
		return nil, err
	}
	if _, err := s.expect(tokRParen); err != nil {
		return nil, err
	}

	// Must be followed by a comparison operator.
	if !s.at(tokEq, tokNeq, tokLT, tokLE, tokGT, tokGE) {
		return nil, fmt.Errorf("expected comparison operator after size(), got %q", s.peek().val)
	}
	op := s.next()
	right, err := parseOperand(s, names, values)
	if err != nil {
		return nil, err
	}
	return &filterSizeCompare{path: path, op: op.kind, right: right}, nil
}
