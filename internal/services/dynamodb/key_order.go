package dynamodb

import "strings"

// keyAttrType returns the declared AttributeType ("S", "N", or "B") for a
// key attribute name, consulting the table's AttributeDefinitions. AWS
// requires every attribute used in any key schema — the base table's own
// hash/sort key, or any GSI/LSI's hash/sort key — to be declared there, so
// this one lookup covers base-table and index key attributes alike from the
// single place *Table already models types (store.go's AttributeDef).
//
// Returns "S" (string ordering) when table is nil, name is "" (no sort
// key), or the attribute isn't declared — a defensive default for
// malformed/legacy table records, per CLAUDE.md's isolation rule: a missing
// or bad declaration degrades to today's string-order behavior instead of
// panicking or 500ing an otherwise-valid read.
func keyAttrType(table *Table, name string) string {
	if typ, ok := declaredKeyAttrType(table, name); ok {
		return typ
	}
	return "S"
}

// declaredKeyAttrType is keyAttrType without the defensive default: it reports
// whether the attribute is declared at all, which is what write-path
// validation needs (key_schema.go). A key attribute with no declaration cannot
// be type-checked against anything, and AWS makes that state unreachable at
// CreateTable, so only a malformed or legacy table record reaches it — the
// caller skips the check rather than inventing a type for it.
func declaredKeyAttrType(table *Table, name string) (string, bool) {
	if table == nil || name == "" {
		return "", false
	}
	for _, d := range table.AttributeDefinitions {
		if d.AttributeName == name {
			return d.AttributeType, true
		}
	}
	return "", false
}

// compareKeyAttr compares two raw key-attribute scalar values (as returned
// by extractKeyValue) per AWS's Query ordering contract: "If the data type
// of the sort key is Number, the results are returned in numeric order;
// otherwise, the results are returned in order of UTF-8 bytes."
//
// String/Binary values already sort correctly via plain Go string
// comparison (UTF-8 byte order) — no transform needed. Number values need
// encodeOrderableNumber because their raw decimal text does not sort in
// numeric order ("10" < "5" lexicographically); encoding both sides and
// comparing the encoded strings also makes this an *equality* comparator
// consistent with the ordering one — two representations of the same
// numeric value ("5" and "5.0") compare equal, matching DynamoDB's
// numeric-equality semantics for key comparisons (not just their order).
//
// If either value fails to parse as a Number (a malformed/legacy record),
// this falls back to a raw string compare rather than erroring — CLAUDE.md's
// isolation rule: one bad persisted value must not break ordering/pagination
// for everything else in the same scan/query.
func compareKeyAttr(typ string, a, b string) int {
	if typ == "N" {
		ea, erra := encodeOrderableNumber(a)
		eb, errb := encodeOrderableNumber(b)
		if erra == nil && errb == nil {
			return strings.Compare(ea, eb)
		}
	}
	return strings.Compare(a, b)
}

// encodeStorageKeyComponent returns the storage-encoded form of a single raw
// key-attribute value: encodeOrderableNumber's output when typ is "N" (so
// the backend's composite/ORDER BY key sorts numerically — see
// docs/plans/dynamodb-gsi-design.md §2), or the raw value unchanged for
// String/Binary (whose raw form already sorts correctly).
//
// Falls back to the raw value on an encoding error rather than failing the
// write/read outright — see resolveStorageKeys' doc comment for why that
// symmetry matters.
func encodeStorageKeyComponent(typ string, raw string) string {
	if typ != "N" {
		return raw
	}
	encoded, err := encodeOrderableNumber(raw)
	if err != nil {
		return raw
	}
	return encoded
}
