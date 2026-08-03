package iampolicy

import (
	"encoding/json"
	"strings"
)

// Position identifies a row/column location within a policy document. It
// maps onto AWS's Position data type, used by Statement.StartPosition and
// Statement.EndPosition in the SimulatePrincipalPolicy/SimulateCustomPolicy
// MatchedStatements response.
//
// Line and Column are 1-based, matching AWS's convention.
type Position struct {
	Line   int
	Column int
}

// positionPair is a statement's start/end Position within its source
// document.
type positionPair struct {
	Start Position
	End   Position
}

// computeStatementPositions locates each statement's raw JSON object within
// doc and returns its start (opening `{`) and end (closing `}`) Position, in
// order. raws must be the exact byte slices captured for each statement via
// json.RawMessage — that guarantee is what lets a straightforward substring
// search find each one reliably, searching forward from the end of the
// previous match so that byte-identical statements resolve to distinct,
// successive positions rather than all landing on the first occurrence.
//
// If a statement's bytes cannot be found (not expected: RawMessage always
// holds the literal source bytes for the value it captured), that entry's
// Start.Line is left at 0, which callers treat as "position unavailable"
// rather than fabricating one.
func computeStatementPositions(doc string, raws []json.RawMessage) []positionPair {
	out := make([]positionPair, len(raws))
	cursor := 0
	for i, raw := range raws {
		text := string(raw)
		if text == "" {
			continue
		}
		idx := strings.Index(doc[cursor:], text)
		if idx < 0 {
			// Defensive fallback: search the whole document. Still correct,
			// just no longer immune to duplicate-text ordering.
			idx = strings.Index(doc, text)
			if idx < 0 {
				continue
			}
		} else {
			idx += cursor
		}
		end := idx + len(text) - 1
		out[i] = positionPair{Start: lineColumn(doc, idx), End: lineColumn(doc, end)}
		cursor = idx + len(text)
	}
	return out
}

// lineColumn converts a 0-based byte offset into doc to a 1-based Line/Column
// pair, the same convention AWS's Position type uses.
func lineColumn(doc string, offset int) Position {
	line, col := 1, 1
	limit := offset
	if limit > len(doc) {
		limit = len(doc)
	}
	for i := 0; i < limit; i++ {
		if doc[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	return Position{Line: line, Column: col}
}
