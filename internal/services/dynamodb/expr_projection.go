package dynamodb

// expr_projection.go implements DynamoDB ProjectionExpression parsing and
// application.
//
// A ProjectionExpression is a comma-separated list of document paths that
// specify which attributes to include in the response. Only the named
// attributes (and the key attributes, which are always included) are returned.
//
// Examples:
//   - "title, year"
//   - "#t, info.rating"
//   - "tags[0], nested.list[1].name"

import "fmt"

// projection is a compiled projection expression.
type projection struct {
	paths []docPath
}

// compileProjection parses a ProjectionExpression string.
func compileProjection(expr string, names map[string]string) (*projection, error) {
	if expr == "" {
		return nil, nil
	}
	tokens, err := tokenise(expr)
	if err != nil {
		return nil, err
	}
	s := newTokStream(tokens)
	var paths []docPath

	for {
		path, err := parsePath(s, names)
		if err != nil {
			return nil, fmt.Errorf("ProjectionExpression: %w", err)
		}
		paths = append(paths, path)
		if !s.at(tokComma) {
			break
		}
		s.next() // consume ','
	}

	if !s.at(tokEOF) {
		return nil, fmt.Errorf("unexpected token %q at position %d in ProjectionExpression", s.peek().val, s.peek().pos)
	}

	return &projection{paths: paths}, nil
}

// applyProjection filters an item to include only the projected attributes
// plus the key attributes. Returns a new item (does not modify the original).
func applyProjection(item Item, proj *projection, table *Table) Item {
	if proj == nil || len(proj.paths) == 0 {
		return item
	}

	result := make(Item)

	// Always include key attributes.
	hashKey := table.hashKeyName()
	if hashKey != "" {
		if v, ok := item[hashKey]; ok {
			result[hashKey] = v
		}
	}
	sortKey := table.sortKeyName()
	if sortKey != "" {
		if v, ok := item[sortKey]; ok {
			result[sortKey] = v
		}
	}

	// Include projected paths.
	for _, path := range proj.paths {
		v, ok := getByPath(item, path)
		if !ok {
			continue
		}
		if path.isSimple() {
			result[path.topLevel()] = v
		} else {
			// For nested paths, we place the value at the top-level
			// attribute to match DynamoDB's behaviour of reconstructing
			// the nested structure.
			ensureNestedPath(result, item, path)
		}
	}

	return result
}

// ensureNestedPath copies a nested path from src to dst, preserving the
// nesting structure.
func ensureNestedPath(dst, src Item, path docPath) {
	if len(path.elements) == 0 {
		return
	}

	topLevel := path.elements[0].attrName

	// For simple paths, just copy the top-level attribute.
	if path.isSimple() {
		if v, ok := src[topLevel]; ok {
			dst[topLevel] = v
		}
		return
	}

	// For nested paths, we need to reconstruct the nested structure.
	// The simplest correct approach: ensure the top-level attribute
	// exists in dst, then set the value through the nested path.
	v, ok := getByPath(src, path)
	if !ok {
		return
	}

	// If the top-level attribute doesn't exist in dst yet, create it
	// from the source's structure but only with the path we need.
	if _, exists := dst[topLevel]; !exists {
		srcTop, ok := src[topLevel]
		if !ok {
			return
		}
		dst[topLevel] = cloneAttrValueForProjection(srcTop, path.elements[1:])
		return
	}

	// The top-level exists; set the nested value.
	_ = setByPath(dst, path, v)
}

// cloneAttrValueForProjection creates a minimal clone of an attribute value
// that preserves only the structure needed to reach the specified sub-path.
func cloneAttrValueForProjection(val attrValue, remaining []pathElement) attrValue {
	if len(remaining) == 0 {
		return val
	}

	el := remaining[0]
	if el.isIndex {
		list := extractList(val)
		if list == nil || el.index < 0 || el.index >= len(list) {
			return attrValue{"L": []any{}}
		}
		elemAV, ok := anyToAttrValue(list[el.index])
		if !ok {
			return attrValue{"L": []any{}}
		}
		cloned := cloneAttrValueForProjection(elemAV, remaining[1:])
		newList := make([]any, el.index+1)
		for i := 0; i < el.index; i++ {
			newList[i] = map[string]any{"NULL": true}
		}
		newList[el.index] = map[string]any(cloned)
		return attrValue{"L": newList}
	}

	m := extractMap(val)
	if m == nil {
		return attrValue{"M": map[string]any{}}
	}
	raw, ok := m[el.attrName]
	if !ok {
		return attrValue{"M": map[string]any{}}
	}
	elemAV, ok := anyToAttrValue(raw)
	if !ok {
		return attrValue{"M": map[string]any{}}
	}
	cloned := cloneAttrValueForProjection(elemAV, remaining[1:])
	return attrValue{"M": map[string]any{el.attrName: map[string]any(cloned)}}
}
