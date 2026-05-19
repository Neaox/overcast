package dynamodb

// expr_path.go implements DynamoDB document path parsing and navigation.
//
// A document path is a sequence of attribute name dereferences and list index
// lookups:
//   - top_level_attr
//   - #alias.nested_attr
//   - list_attr[0].name
//   - #a.#b[2].c
//
// Paths are used in FilterExpression, ConditionExpression, ProjectionExpression,
// UpdateExpression (SET lhs, REMOVE targets), and KeyConditionExpression.

import (
	"fmt"
	"strconv"
	"strings"
)

// pathElement is one step in a document path.
type pathElement struct {
	// Exactly one of these is set.
	attrName string // e.g. "foo"
	index    int    // list index (valid when isIndex is true)
	isIndex  bool
}

// docPath is a parsed document path (e.g. "a.b[0].c" → 4 elements).
type docPath struct {
	elements []pathElement
}

// String returns a human-readable representation of the path.
func (p docPath) String() string {
	var b strings.Builder
	for i, el := range p.elements {
		if el.isIndex {
			b.WriteString("[")
			b.WriteString(strconv.Itoa(el.index))
			b.WriteString("]")
		} else {
			if i > 0 {
				b.WriteString(".")
			}
			b.WriteString(el.attrName)
		}
	}
	return b.String()
}

// topLevel returns the top-level attribute name.
func (p docPath) topLevel() string {
	if len(p.elements) == 0 {
		return ""
	}
	return p.elements[0].attrName
}

// isSimple returns true if the path is a single top-level attribute name.
func (p docPath) isSimple() bool {
	return len(p.elements) == 1 && !p.elements[0].isIndex
}

// parsePath parses a document path from a token stream.
// It consumes tokens until it sees something that is not part of a path
// (e.g. an operator, comma, EOF).
func parsePath(s *tokStream, names map[string]string) (docPath, error) {
	var elements []pathElement

	// First element: must be an identifier or alias.
	name, err := parsePathName(s, names)
	if err != nil {
		return docPath{}, err
	}
	elements = append(elements, pathElement{attrName: name})

	// Subsequent elements: .name or [index]
	for {
		if s.at(tokDot) {
			s.next() // consume '.'
			name, err := parsePathName(s, names)
			if err != nil {
				return docPath{}, err
			}
			elements = append(elements, pathElement{attrName: name})
		} else if s.at(tokLBracket) {
			s.next() // consume '['
			idxTok, err := s.expect(tokNumber)
			if err != nil {
				return docPath{}, fmt.Errorf("expected list index: %w", err)
			}
			idx, err := strconv.Atoi(idxTok.val)
			if err != nil {
				return docPath{}, fmt.Errorf("invalid list index %q", idxTok.val)
			}
			if _, err := s.expect(tokRBracket); err != nil {
				return docPath{}, err
			}
			elements = append(elements, pathElement{index: idx, isIndex: true})
		} else {
			break
		}
	}

	return docPath{elements: elements}, nil
}

// parsePathName reads an identifier or alias and resolves it.
func parsePathName(s *tokStream, names map[string]string) (string, error) {
	t := s.peek()
	//exhaustive:ignore
	switch t.kind {
	case tokIdent:
		s.next()
		return t.val, nil
	case tokAlias:
		s.next()
		return resolveAlias(t.val, names)
	default:
		// Allow keywords to be used as attribute names (DynamoDB allows this
		// in practice when using ExpressionAttributeNames, but also in
		// projections etc. when unambiguous).
		if isKeywordToken(t.kind) {
			s.next()
			return t.val, nil
		}
		return "", fmt.Errorf("expected attribute name, got %q at position %d", t.val, t.pos)
	}
}

// isKeywordToken returns true if the token kind is a keyword that could
// double as an attribute name in some contexts.
func isKeywordToken(k tokenKind) bool {
	//exhaustive:ignore
	switch k {
	case tokSET, tokREMOVE, tokADD, tokDELETE, tokIN, tokBETWEEN, tokAND, tokOR, tokNOT:
		return true
	default:
		return false
	}
}

// ---------------------------------------------------------------------------
// Path resolution against items
// ---------------------------------------------------------------------------

// getByPath retrieves a value from an Item following the document path.
// Returns (nil, false) if the path does not exist.
func getByPath(item Item, path docPath) (attrValue, bool) {
	if len(path.elements) == 0 {
		return nil, false
	}

	// First element is always a top-level attribute name.
	first := path.elements[0]
	val, ok := item[first.attrName]
	if !ok {
		return nil, false
	}

	// Walk remaining elements.
	for _, el := range path.elements[1:] {
		if el.isIndex {
			list := extractList(val)
			if list == nil || el.index < 0 || el.index >= len(list) {
				return nil, false
			}
			av, ok := anyToAttrValue(list[el.index])
			if !ok {
				return nil, false
			}
			val = av
		} else {
			m := extractMap(val)
			if m == nil {
				return nil, false
			}
			raw, ok := m[el.attrName]
			if !ok {
				return nil, false
			}
			av, ok := anyToAttrValue(raw)
			if !ok {
				return nil, false
			}
			val = av
		}
	}
	return val, true
}

// setByPath sets a value in an Item at the given document path.
// Creates intermediate maps/lists as needed. Returns an error if the
// path structure conflicts with existing data.
func setByPath(item Item, path docPath, val attrValue) error {
	if len(path.elements) == 0 {
		return fmt.Errorf("empty path")
	}

	// Simple case: top-level attribute.
	if path.isSimple() {
		item[path.elements[0].attrName] = val
		return nil
	}

	// Navigate to the parent, creating intermediates as needed.
	first := path.elements[0]
	current, ok := item[first.attrName]
	if !ok {
		// Need to create the intermediate structure.
		if len(path.elements) >= 2 && path.elements[1].isIndex {
			current = attrValue{"L": []any{}}
		} else {
			current = attrValue{"M": map[string]any{}}
		}
		item[first.attrName] = current
	}

	parent := path.elements[:len(path.elements)-1]
	last := path.elements[len(path.elements)-1]

	// Walk to the parent of the last element.
	for i := 1; i < len(parent); i++ {
		el := parent[i]
		if el.isIndex {
			list := extractList(current)
			if list == nil || el.index < 0 || el.index >= len(list) {
				return fmt.Errorf("list index %d out of bounds at %s", el.index, path.String())
			}
			av, ok := anyToAttrValue(list[el.index])
			if !ok {
				return fmt.Errorf("non-map element at index %d", el.index)
			}
			current = av
		} else {
			m := extractMap(current)
			if m == nil {
				return fmt.Errorf("expected map at %s", el.attrName)
			}
			raw, ok := m[el.attrName]
			if !ok {
				// Create intermediate.
				next := i + 1
				if next < len(path.elements) && path.elements[next].isIndex {
					m[el.attrName] = map[string]any{"L": []any{}}
				} else {
					m[el.attrName] = map[string]any{"M": map[string]any{}}
				}
				raw = m[el.attrName]
			}
			av, ok := anyToAttrValue(raw)
			if !ok {
				return fmt.Errorf("non-map element at %s", el.attrName)
			}
			current = av
		}
	}

	// Set the final element.
	if last.isIndex {
		list := extractList(current)
		if list == nil {
			return fmt.Errorf("cannot index into non-list at %s", path.String())
		}
		// Extend list if needed.
		for len(list) <= last.index {
			list = append(list, map[string]any{"NULL": true})
		}
		list[last.index] = map[string]any(val)
		current["L"] = list
	} else {
		m := extractMap(current)
		if m == nil {
			return fmt.Errorf("cannot set attribute on non-map at %s", path.String())
		}
		m[last.attrName] = map[string]any(val)
	}

	return nil
}

// removeByPath removes a value from an Item at the given document path.
// Returns true if the path existed and was removed.
func removeByPath(item Item, path docPath) bool {
	if len(path.elements) == 0 {
		return false
	}

	// Simple case: top-level attribute.
	if path.isSimple() {
		_, ok := item[path.elements[0].attrName]
		if ok {
			delete(item, path.elements[0].attrName)
		}
		return ok
	}

	// Navigate to parent.
	first := path.elements[0]
	current, ok := item[first.attrName]
	if !ok {
		return false
	}

	parent := path.elements[:len(path.elements)-1]
	last := path.elements[len(path.elements)-1]

	for i := 1; i < len(parent); i++ {
		el := parent[i]
		if el.isIndex {
			list := extractList(current)
			if list == nil || el.index < 0 || el.index >= len(list) {
				return false
			}
			av, ok := anyToAttrValue(list[el.index])
			if !ok {
				return false
			}
			current = av
		} else {
			m := extractMap(current)
			if m == nil {
				return false
			}
			raw, ok := m[el.attrName]
			if !ok {
				return false
			}
			av, ok := anyToAttrValue(raw)
			if !ok {
				return false
			}
			current = av
		}
	}

	if last.isIndex {
		list := extractList(current)
		if list == nil || last.index < 0 || last.index >= len(list) {
			return false
		}
		// DynamoDB doesn't actually shift list elements on REMOVE; it sets to NULL.
		// Actually, it does remove the element and shift. Let's match real AWS.
		newList := make([]any, 0, len(list)-1)
		for i, v := range list {
			if i != last.index {
				newList = append(newList, v)
			}
		}
		current["L"] = newList
		return true
	}

	m := extractMap(current)
	if m == nil {
		return false
	}
	_, ok = m[last.attrName]
	if ok {
		delete(m, last.attrName)
	}
	return ok
}
