package iampolicy

import (
	"strings"
	"testing"
)

// offsetAt converts a 1-based Line/Column pair back into a 0-based byte
// offset into doc, using logic written independently of lineColumn so this
// test doesn't just check the implementation against itself.
func offsetAt(t *testing.T, doc string, pos *Position) int {
	t.Helper()
	if pos == nil {
		t.Fatalf("position is nil")
	}
	line, col := 1, 1
	for i := 0; i < len(doc); i++ {
		if line == pos.Line && col == pos.Column {
			return i
		}
		if doc[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	t.Fatalf("position %+v not found in document", *pos)
	return -1
}

func TestParseDocument_multipleStatementsSameDocument_distinctPositions(t *testing.T) {
	// Given: one document with two statements sharing a document — the shape
	// that made MatchedStatements report two identical entries before this
	// fix, since SourcePolicyId/SourcePolicyType alone cannot tell them apart.
	doc := `{"Version":"2012-10-17","Statement":[
{"Sid":"AllowAll","Effect":"Allow","Action":"s3:*","Resource":"*"},
{"Sid":"DenyDelete","Effect":"Deny","Action":"s3:DeleteObject","Resource":"arn:aws:s3:::iamrc-bucket/*"}]}`

	// When: the document is parsed
	stmts, err := ParseDocument(doc, SourceRef{ID: "PolicyInputList.1", Type: SourceTypeInputPolicy})
	if err != nil {
		t.Fatalf("ParseDocument: unexpected error: %v", err)
	}
	if len(stmts) != 2 {
		t.Fatalf("len(stmts) = %d, want 2", len(stmts))
	}

	// Then: both statements report a position, and the positions differ
	first, second := stmts[0], stmts[1]
	if first.StartPosition == nil || first.EndPosition == nil {
		t.Fatalf("AllowAll: StartPosition/EndPosition = %+v/%+v, want both set", first.StartPosition, first.EndPosition)
	}
	if second.StartPosition == nil || second.EndPosition == nil {
		t.Fatalf("DenyDelete: StartPosition/EndPosition = %+v/%+v, want both set", second.StartPosition, second.EndPosition)
	}
	if *first.StartPosition == *second.StartPosition {
		t.Fatalf("both statements report the same StartPosition %+v — cannot distinguish which decided the outcome",
			*first.StartPosition)
	}

	// And: the positions point at the exact statement text in the document,
	// independently verified by converting back to byte offsets
	firstStart := offsetAt(t, doc, first.StartPosition)
	firstEnd := offsetAt(t, doc, first.EndPosition)
	if doc[firstStart] != '{' || doc[firstEnd] != '}' {
		t.Fatalf("AllowAll span = %q, want it to start with { and end with }", doc[firstStart:firstEnd+1])
	}
	if got := doc[firstStart : firstEnd+1]; !strings.Contains(got, "AllowAll") || strings.Contains(got, "DenyDelete") {
		t.Fatalf("AllowAll span = %q, want it to contain only the AllowAll statement", got)
	}

	secondStart := offsetAt(t, doc, second.StartPosition)
	secondEnd := offsetAt(t, doc, second.EndPosition)
	if doc[secondStart] != '{' || doc[secondEnd] != '}' {
		t.Fatalf("DenyDelete span = %q, want it to start with { and end with }", doc[secondStart:secondEnd+1])
	}
	if got := doc[secondStart : secondEnd+1]; !strings.Contains(got, "DenyDelete") || strings.Contains(got, "AllowAll") {
		t.Fatalf("DenyDelete span = %q, want it to contain only the DenyDelete statement", got)
	}
}

func TestParseDocument_singleStatementNotArray_positionCoversStatement(t *testing.T) {
	// Given: a document whose Statement element is a single object, not a list
	doc := `{"Version":"2012-10-17","Statement":{"Sid":"Solo","Effect":"Allow","Action":"s3:GetObject","Resource":"*"}}`

	// When: it is parsed
	stmts, err := ParseDocument(doc, SourceRef{ID: "solo", Type: SourceTypeIAMPolicy})
	if err != nil {
		t.Fatalf("ParseDocument: unexpected error: %v", err)
	}
	if len(stmts) != 1 {
		t.Fatalf("len(stmts) = %d, want 1", len(stmts))
	}

	// Then: the single statement still gets a position spanning its object
	stmt := stmts[0]
	if stmt.StartPosition == nil || stmt.EndPosition == nil {
		t.Fatalf("StartPosition/EndPosition = %+v/%+v, want both set", stmt.StartPosition, stmt.EndPosition)
	}
	start := offsetAt(t, doc, stmt.StartPosition)
	end := offsetAt(t, doc, stmt.EndPosition)
	if doc[start] != '{' || doc[end] != '}' {
		t.Fatalf("span = %q, want it to start with { and end with }", doc[start:end+1])
	}
}
