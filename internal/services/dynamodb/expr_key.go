package dynamodb

// expr_key.go implements DynamoDB KeyConditionExpression parsing.
//
// A KeyConditionExpression must always include an equality condition on the
// partition key, and may optionally include a condition on the sort key:
//
//   hashAttr = :h
//   hashAttr = :h AND sortAttr = :s
//   hashAttr = :h AND sortAttr < :s
//   hashAttr = :h AND sortAttr <= :s
//   hashAttr = :h AND sortAttr > :s
//   hashAttr = :h AND sortAttr >= :s
//   hashAttr = :h AND sortAttr BETWEEN :lo AND :hi
//   hashAttr = :h AND begins_with(sortAttr, :prefix)

import (
	"fmt"
	"strings"
)

// keyCond is a parsed key condition.
type keyCond struct {
	hashAttr string
	hashVal  attrValue

	// Sort key condition (nil when no sort key condition).
	sortCond *sortKeyCond
}

// sortKeyCondKind classifies the sort key condition type.
type sortKeyCondKind int

const (
	sortKeyEq sortKeyCondKind = iota
	sortKeyLT
	sortKeyLE
	sortKeyGT
	sortKeyGE
	sortKeyBetween
	sortKeyBeginsWith
)

// sortKeyCond is a parsed sort key condition within a KeyConditionExpression.
type sortKeyCond struct {
	attr string
	kind sortKeyCondKind
	val  attrValue // for single-value conditions
	lo   attrValue // for BETWEEN
	hi   attrValue // for BETWEEN
}

// matchItem returns true if an item's sort key matches this condition.
func (c *sortKeyCond) matchItem(item Item) bool {
	av, ok := item[c.attr]
	if !ok {
		return false
	}
	switch c.kind {
	case sortKeyEq:
		return attrValueEqual(av, c.val)
	case sortKeyLT:
		cmp, err := attrValueCompare(av, c.val)
		return err == nil && cmp < 0
	case sortKeyLE:
		cmp, err := attrValueCompare(av, c.val)
		return err == nil && cmp <= 0
	case sortKeyGT:
		cmp, err := attrValueCompare(av, c.val)
		return err == nil && cmp > 0
	case sortKeyGE:
		cmp, err := attrValueCompare(av, c.val)
		return err == nil && cmp >= 0
	case sortKeyBetween:
		cmpLo, err1 := attrValueCompare(av, c.lo)
		cmpHi, err2 := attrValueCompare(av, c.hi)
		return err1 == nil && err2 == nil && cmpLo >= 0 && cmpHi <= 0
	case sortKeyBeginsWith:
		return strings.HasPrefix(extractScalar(av), extractScalar(c.val))
	}
	return false
}

// compileKeyCondition parses a KeyConditionExpression.
func compileKeyCondition(
	expr string,
	names map[string]string,
	values map[string]attrValue,
) (*keyCond, error) {
	tokens, err := tokenise(expr)
	if err != nil {
		return nil, err
	}
	s := newTokStream(tokens)

	// Parse hash key equality: hashAttr = :val
	hashPath, err := parsePath(s, names)
	if err != nil {
		return nil, fmt.Errorf("KeyConditionExpression: %w", err)
	}
	if !hashPath.isSimple() {
		return nil, fmt.Errorf("KeyConditionExpression: hash key must be a simple attribute name")
	}
	if _, err := s.expect(tokEq); err != nil {
		return nil, fmt.Errorf("KeyConditionExpression: hash key must use = operator")
	}
	if !s.at(tokPlaceholder) {
		return nil, fmt.Errorf("KeyConditionExpression: expected :placeholder for hash key value")
	}
	hashPlaceholder := s.next()
	hashVal, err := resolvePlaceholder(hashPlaceholder.val, values)
	if err != nil {
		return nil, err
	}

	result := &keyCond{
		hashAttr: hashPath.topLevel(),
		hashVal:  hashVal,
	}

	// Optional sort key condition.
	if s.at(tokAND) {
		s.next()
		sortCond, err := parseSortKeyCondition(s, names, values)
		if err != nil {
			return nil, err
		}
		result.sortCond = sortCond
	}

	if !s.at(tokEOF) {
		return nil, fmt.Errorf("KeyConditionExpression: unexpected token %q at position %d", s.peek().val, s.peek().pos)
	}

	return result, nil
}

func parseSortKeyCondition(s *tokStream, names map[string]string, values map[string]attrValue) (*sortKeyCond, error) {
	// begins_with(sortAttr, :prefix) — function form
	if s.at(tokIdent) && strings.ToLower(s.peek().val) == "begins_with" {
		s.next()
		if _, err := s.expect(tokLParen); err != nil {
			return nil, err
		}
		sortPath, err := parsePath(s, names)
		if err != nil {
			return nil, err
		}
		if !sortPath.isSimple() {
			return nil, fmt.Errorf("KeyConditionExpression: sort key must be a simple attribute name")
		}
		if _, err := s.expect(tokComma); err != nil {
			return nil, err
		}
		if !s.at(tokPlaceholder) {
			return nil, fmt.Errorf("KeyConditionExpression: expected :placeholder for begins_with prefix")
		}
		prefixTok := s.next()
		prefixVal, err := resolvePlaceholder(prefixTok.val, values)
		if err != nil {
			return nil, err
		}
		if _, err := s.expect(tokRParen); err != nil {
			return nil, err
		}
		return &sortKeyCond{attr: sortPath.topLevel(), kind: sortKeyBeginsWith, val: prefixVal}, nil
	}

	// sortAttr op :val | sortAttr BETWEEN :lo AND :hi
	sortPath, err := parsePath(s, names)
	if err != nil {
		return nil, fmt.Errorf("KeyConditionExpression: %w", err)
	}
	if !sortPath.isSimple() {
		return nil, fmt.Errorf("KeyConditionExpression: sort key must be a simple attribute name")
	}

	// BETWEEN
	if s.at(tokBETWEEN) {
		s.next()
		if !s.at(tokPlaceholder) {
			return nil, fmt.Errorf("KeyConditionExpression: expected :placeholder for BETWEEN low value")
		}
		loTok := s.next()
		loVal, err := resolvePlaceholder(loTok.val, values)
		if err != nil {
			return nil, err
		}
		if _, err := s.expect(tokAND); err != nil {
			return nil, fmt.Errorf("KeyConditionExpression: BETWEEN requires AND")
		}
		if !s.at(tokPlaceholder) {
			return nil, fmt.Errorf("KeyConditionExpression: expected :placeholder for BETWEEN high value")
		}
		hiTok := s.next()
		hiVal, err := resolvePlaceholder(hiTok.val, values)
		if err != nil {
			return nil, err
		}
		return &sortKeyCond{attr: sortPath.topLevel(), kind: sortKeyBetween, lo: loVal, hi: hiVal}, nil
	}

	// Comparison operator
	if !s.at(tokEq, tokLT, tokLE, tokGT, tokGE) {
		return nil, fmt.Errorf("KeyConditionExpression: expected comparison operator for sort key, got %q", s.peek().val)
	}
	opTok := s.next()
	if !s.at(tokPlaceholder) {
		return nil, fmt.Errorf("KeyConditionExpression: expected :placeholder for sort key value")
	}
	valTok := s.next()
	sortVal, err := resolvePlaceholder(valTok.val, values)
	if err != nil {
		return nil, err
	}

	var kind sortKeyCondKind
	//exhaustive:ignore
	switch opTok.kind {
	case tokEq:
		kind = sortKeyEq
	case tokLT:
		kind = sortKeyLT
	case tokLE:
		kind = sortKeyLE
	case tokGT:
		kind = sortKeyGT
	case tokGE:
		kind = sortKeyGE
	default:
		return nil, fmt.Errorf("KeyConditionExpression: unsupported sort key operator %q", opTok.val)
	}
	return &sortKeyCond{attr: sortPath.topLevel(), kind: kind, val: sortVal}, nil
}
