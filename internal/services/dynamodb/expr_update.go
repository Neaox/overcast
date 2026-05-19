package dynamodb

// expr_update.go implements DynamoDB UpdateExpression compilation and execution.
//
// Supported clauses:
//   - SET path = value | SET path = path + :val | SET path = path - :val
//     SET path = if_not_exists(path, :val)
//     SET path = list_append(list1, list2)
//   - REMOVE path [, path ...]
//   - ADD path :val  (numbers and sets)
//   - DELETE path :val  (sets only)
//
// Multiple clauses may appear in one expression:
//   "SET #n = :v, price = price + :inc REMOVE old_field ADD tags :newtags"

import (
	"fmt"
	"strconv"
	"strings"
)

// updateAction is one discrete mutation produced by parsing an UpdateExpression.
type updateAction interface {
	apply(item Item) error
}

// applyUpdateExpression parses and applies an UpdateExpression to an item.
func applyUpdateExpression(item Item, expr string,
	attrNames map[string]string, attrValues map[string]attrValue) error {

	actions, err := compileUpdate(expr, attrNames, attrValues)
	if err != nil {
		return err
	}
	for _, a := range actions {
		if err := a.apply(item); err != nil {
			return err
		}
	}
	return nil
}

// compileUpdate parses an UpdateExpression into a sequence of actions.
func compileUpdate(expr string, names map[string]string, values map[string]attrValue) ([]updateAction, error) {
	tokens, err := tokenise(expr)
	if err != nil {
		return nil, err
	}
	s := newTokStream(tokens)
	var actions []updateAction

	for !s.at(tokEOF) {
		switch {
		case s.at(tokSET):
			s.next()
			a, err := parseSetClause(s, names, values)
			if err != nil {
				return nil, err
			}
			actions = append(actions, a...)
		case s.at(tokREMOVE):
			s.next()
			a, err := parseRemoveClause(s, names)
			if err != nil {
				return nil, err
			}
			actions = append(actions, a...)
		case s.at(tokADD):
			s.next()
			a, err := parseAddClause(s, names, values)
			if err != nil {
				return nil, err
			}
			actions = append(actions, a...)
		case s.at(tokDELETE):
			s.next()
			a, err := parseDeleteClause(s, names, values)
			if err != nil {
				return nil, err
			}
			actions = append(actions, a...)
		default:
			return nil, fmt.Errorf("expected SET, REMOVE, ADD, or DELETE, got %q at position %d", s.peek().val, s.peek().pos)
		}
	}
	return actions, nil
}

// ---------------------------------------------------------------------------
// SET clause
// ---------------------------------------------------------------------------

// setAction assigns a value to a path.
type setAction struct {
	path    docPath
	valExpr updateValExpr
}

func (a *setAction) apply(item Item) error {
	val, err := a.valExpr.evaluate(item)
	if err != nil {
		return err
	}
	return setByPath(item, a.path, val)
}

// updateValExpr is the right-hand side of a SET assignment.
type updateValExpr interface {
	evaluate(item Item) (attrValue, error)
}

// literalValExpr returns a fixed value.
type literalValExpr struct{ val attrValue }

func (e *literalValExpr) evaluate(_ Item) (attrValue, error) { return e.val, nil }

// pathValExpr reads from a document path.
type pathValExpr struct{ path docPath }

func (e *pathValExpr) evaluate(item Item) (attrValue, error) {
	v, ok := getByPath(item, e.path)
	if !ok {
		return nil, fmt.Errorf("attribute %s does not exist", e.path.String())
	}
	return v, nil
}

// arithmeticExpr handles path + :val or path - :val.
type arithmeticExpr struct {
	left  updateValExpr
	right updateValExpr
	op    tokenKind // tokPlus or tokMinus
}

func (e *arithmeticExpr) evaluate(item Item) (attrValue, error) {
	lv, err := e.left.evaluate(item)
	if err != nil {
		return nil, err
	}
	rv, err := e.right.evaluate(item)
	if err != nil {
		return nil, err
	}
	ln, err := strconv.ParseFloat(extractScalar(lv), 64)
	if err != nil {
		return nil, fmt.Errorf("SET arithmetic: left operand is not a number")
	}
	rn, err := strconv.ParseFloat(extractScalar(rv), 64)
	if err != nil {
		return nil, fmt.Errorf("SET arithmetic: right operand is not a number")
	}
	var result float64
	if e.op == tokPlus {
		result = ln + rn
	} else {
		result = ln - rn
	}
	return numberAttrValue(result), nil
}

// ifNotExistsExpr handles if_not_exists(path, value).
type ifNotExistsExpr struct {
	path     docPath
	fallback updateValExpr
}

func (e *ifNotExistsExpr) evaluate(item Item) (attrValue, error) {
	v, ok := getByPath(item, e.path)
	if ok {
		return v, nil
	}
	return e.fallback.evaluate(item)
}

// listAppendExpr handles list_append(list1, list2).
type listAppendExpr struct {
	left  updateValExpr
	right updateValExpr
}

func (e *listAppendExpr) evaluate(item Item) (attrValue, error) {
	lv, err := e.left.evaluate(item)
	if err != nil {
		return nil, err
	}
	rv, err := e.right.evaluate(item)
	if err != nil {
		return nil, err
	}
	ll := extractList(lv)
	rl := extractList(rv)
	if ll == nil {
		ll = []any{}
	}
	if rl == nil {
		rl = []any{}
	}
	combined := make([]any, 0, len(ll)+len(rl))
	combined = append(combined, ll...)
	combined = append(combined, rl...)
	return attrValue{"L": combined}, nil
}

func parseSetClause(s *tokStream, names map[string]string, values map[string]attrValue) ([]updateAction, error) {
	var actions []updateAction
	for {
		path, err := parsePath(s, names)
		if err != nil {
			return nil, fmt.Errorf("SET: %w", err)
		}
		if _, err := s.expect(tokEq); err != nil {
			return nil, fmt.Errorf("SET: %w", err)
		}
		valExpr, err := parseSetValue(s, names, values)
		if err != nil {
			return nil, fmt.Errorf("SET: %w", err)
		}
		actions = append(actions, &setAction{path: path, valExpr: valExpr})
		if !s.at(tokComma) {
			break
		}
		s.next() // consume ','
	}
	return actions, nil
}

func parseSetValue(s *tokStream, names map[string]string, values map[string]attrValue) (updateValExpr, error) {
	left, err := parseSetAtom(s, names, values)
	if err != nil {
		return nil, err
	}

	// Check for arithmetic: + or -
	if s.at(tokPlus, tokMinus) {
		op := s.next()
		right, err := parseSetAtom(s, names, values)
		if err != nil {
			return nil, err
		}
		return &arithmeticExpr{left: left, right: right, op: op.kind}, nil
	}
	return left, nil
}

func parseSetAtom(s *tokStream, names map[string]string, values map[string]attrValue) (updateValExpr, error) {
	// :placeholder
	if s.at(tokPlaceholder) {
		tok := s.next()
		val, err := resolvePlaceholder(tok.val, values)
		if err != nil {
			return nil, err
		}
		return &literalValExpr{val: val}, nil
	}

	// Function: if_not_exists or list_append
	if s.at(tokIdent) {
		fn := strings.ToLower(s.peek().val)
		switch fn {
		case "if_not_exists":
			s.next()
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
			fallback, err := parseSetAtom(s, names, values)
			if err != nil {
				return nil, err
			}
			if _, err := s.expect(tokRParen); err != nil {
				return nil, err
			}
			return &ifNotExistsExpr{path: path, fallback: fallback}, nil
		case "list_append":
			s.next()
			if _, err := s.expect(tokLParen); err != nil {
				return nil, err
			}
			left, err := parseSetAtom(s, names, values)
			if err != nil {
				return nil, err
			}
			if _, err := s.expect(tokComma); err != nil {
				return nil, err
			}
			right, err := parseSetAtom(s, names, values)
			if err != nil {
				return nil, err
			}
			if _, err := s.expect(tokRParen); err != nil {
				return nil, err
			}
			return &listAppendExpr{left: left, right: right}, nil
		}
	}

	// Document path (attribute reference).
	path, err := parsePath(s, names)
	if err != nil {
		return nil, err
	}
	return &pathValExpr{path: path}, nil
}

// ---------------------------------------------------------------------------
// REMOVE clause
// ---------------------------------------------------------------------------

type removeAction struct {
	path docPath
}

func (a *removeAction) apply(item Item) error {
	removeByPath(item, a.path)
	return nil
}

func parseRemoveClause(s *tokStream, names map[string]string) ([]updateAction, error) {
	var actions []updateAction
	for {
		path, err := parsePath(s, names)
		if err != nil {
			return nil, fmt.Errorf("REMOVE: %w", err)
		}
		actions = append(actions, &removeAction{path: path})
		if !s.at(tokComma) {
			break
		}
		s.next() // consume ','
	}
	return actions, nil
}

// ---------------------------------------------------------------------------
// ADD clause
// ---------------------------------------------------------------------------

// addAction handles ADD: adds a number to a number attribute, or adds
// elements to a set attribute.
type addAction struct {
	path docPath
	val  attrValue
}

func (a *addAction) apply(item Item) error {
	existing, ok := getByPath(item, a.path)

	valType := attrType(a.val)

	if !ok {
		// Attribute doesn't exist: create it.
		// For numbers, the initial value is 0 + val = val.
		// For sets, just assign the set.
		return setByPath(item, a.path, a.val)
	}

	existingType := attrType(existing)

	switch {
	case valType == "N" && existingType == "N":
		// Add numbers.
		en, err := strconv.ParseFloat(extractScalar(existing), 64)
		if err != nil {
			return fmt.Errorf("ADD: existing value is not a valid number")
		}
		vn, err := strconv.ParseFloat(extractScalar(a.val), 64)
		if err != nil {
			return fmt.Errorf("ADD: provided value is not a valid number")
		}
		return setByPath(item, a.path, numberAttrValue(en+vn))

	case (valType == "SS" || valType == "NS" || valType == "BS") && existingType == valType:
		// Union sets.
		return setByPath(item, a.path, unionSets(existing, a.val, valType))

	default:
		return fmt.Errorf("ADD: type mismatch between existing (%s) and provided (%s)", existingType, valType)
	}
}

// unionSets merges two sets of the same type.
func unionSets(a, b attrValue, setType string) attrValue {
	existing := make(map[string]bool)
	var result []any

	for _, raw := range a {
		if arr, ok := raw.([]any); ok {
			for _, elem := range arr {
				if s, ok := elem.(string); ok {
					existing[s] = true
					result = append(result, s)
				}
			}
		}
	}
	for _, raw := range b {
		if arr, ok := raw.([]any); ok {
			for _, elem := range arr {
				if s, ok := elem.(string); ok {
					if !existing[s] {
						result = append(result, s)
					}
				}
			}
		}
	}
	return attrValue{setType: result}
}

func parseAddClause(s *tokStream, names map[string]string, values map[string]attrValue) ([]updateAction, error) {
	var actions []updateAction
	for {
		path, err := parsePath(s, names)
		if err != nil {
			return nil, fmt.Errorf("ADD: %w", err)
		}
		if !s.at(tokPlaceholder) {
			return nil, fmt.Errorf("ADD: expected :placeholder value")
		}
		tok := s.next()
		val, err := resolvePlaceholder(tok.val, values)
		if err != nil {
			return nil, fmt.Errorf("ADD: %w", err)
		}
		actions = append(actions, &addAction{path: path, val: val})
		if !s.at(tokComma) {
			break
		}
		s.next()
	}
	return actions, nil
}

// ---------------------------------------------------------------------------
// DELETE clause
// ---------------------------------------------------------------------------

// deleteAction removes elements from a set.
type deleteAction struct {
	path docPath
	val  attrValue
}

func (a *deleteAction) apply(item Item) error {
	existing, ok := getByPath(item, a.path)
	if !ok {
		return nil // Nothing to delete from.
	}

	valType := attrType(a.val)
	existingType := attrType(existing)
	if existingType != valType {
		return nil // Type mismatch: no-op in DynamoDB.
	}

	// Build set of values to remove.
	toRemove := make(map[string]bool)
	for _, raw := range a.val {
		if arr, ok := raw.([]any); ok {
			for _, elem := range arr {
				if s, ok := elem.(string); ok {
					toRemove[s] = true
				}
			}
		}
	}

	// Filter existing set.
	var result []any
	for _, raw := range existing {
		if arr, ok := raw.([]any); ok {
			for _, elem := range arr {
				if s, ok := elem.(string); ok {
					if !toRemove[s] {
						result = append(result, s)
					}
				}
			}
		}
	}

	if len(result) == 0 {
		// DynamoDB removes the attribute entirely when the set becomes empty.
		removeByPath(item, a.path)
		return nil
	}

	return setByPath(item, a.path, attrValue{valType: result})
}

func parseDeleteClause(s *tokStream, names map[string]string, values map[string]attrValue) ([]updateAction, error) {
	var actions []updateAction
	for {
		path, err := parsePath(s, names)
		if err != nil {
			return nil, fmt.Errorf("DELETE: %w", err)
		}
		if !s.at(tokPlaceholder) {
			return nil, fmt.Errorf("DELETE: expected :placeholder value")
		}
		tok := s.next()
		val, err := resolvePlaceholder(tok.val, values)
		if err != nil {
			return nil, fmt.Errorf("DELETE: %w", err)
		}
		actions = append(actions, &deleteAction{path: path, val: val})
		if !s.at(tokComma) {
			break
		}
		s.next()
	}
	return actions, nil
}
