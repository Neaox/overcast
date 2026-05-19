package lambda

import (
	"testing"
)

// filterCriteria is a helper to build a *FilterCriteria from a single JSON pattern string.
func filterCriteria(patterns ...string) *FilterCriteria {
	fc := &FilterCriteria{}
	for _, p := range patterns {
		fc.Filters = append(fc.Filters, Filter{Pattern: p})
	}
	return fc
}

// ─── matchesFilterCriteria ────────────────────────────────────────────────────

func TestMatchesFilterCriteria_nilPassesAll(t *testing.T) {
	record := map[string]any{"eventName": "INSERT"}
	if !matchesFilterCriteria(nil, record) {
		t.Fatal("nil FilterCriteria must pass all records")
	}
}

func TestMatchesFilterCriteria_emptyPassesAll(t *testing.T) {
	record := map[string]any{"eventName": "INSERT"}
	if !matchesFilterCriteria(&FilterCriteria{}, record) {
		t.Fatal("empty FilterCriteria must pass all records")
	}
}

// ─── Exact string match ───────────────────────────────────────────────────────

func TestMatchesFilterCriteria_exactMatch_pass(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT"]}`)
	record := map[string]any{"eventName": "INSERT"}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("exact match should pass")
	}
}

func TestMatchesFilterCriteria_exactMatch_fail(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT"]}`)
	record := map[string]any{"eventName": "MODIFY"}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("exact match should fail when value differs")
	}
}

func TestMatchesFilterCriteria_exactMatch_missingField(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT"]}`)
	record := map[string]any{}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("exact match should fail when field is absent")
	}
}

// ─── Null match ───────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_null_fieldAbsent(t *testing.T) {
	fc := filterCriteria(`{"errorCode": [null]}`)
	record := map[string]any{}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("null rule should pass when field is absent")
	}
}

func TestMatchesFilterCriteria_null_explicitNil(t *testing.T) {
	fc := filterCriteria(`{"errorCode": [null]}`)
	record := map[string]any{"errorCode": nil}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("null rule should pass when field value is null")
	}
}

func TestMatchesFilterCriteria_null_nonNull(t *testing.T) {
	fc := filterCriteria(`{"errorCode": [null]}`)
	record := map[string]any{"errorCode": "THROTTLING"}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("null rule should fail when field has non-null value")
	}
}

// ─── Array OR (multiple values in one rule array) ────────────────────────────

func TestMatchesFilterCriteria_arrayOR_matchFirst(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT", "MODIFY"]}`)
	record := map[string]any{"eventName": "INSERT"}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("array OR: first element should match")
	}
}

func TestMatchesFilterCriteria_arrayOR_matchSecond(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT", "MODIFY"]}`)
	record := map[string]any{"eventName": "MODIFY"}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("array OR: second element should match")
	}
}

func TestMatchesFilterCriteria_arrayOR_noMatch(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT", "MODIFY"]}`)
	record := map[string]any{"eventName": "REMOVE"}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("array OR: no element matches — should fail")
	}
}

// ─── Multiple Filters (top-level OR) ─────────────────────────────────────────

func TestMatchesFilterCriteria_multipleFilters_OR(t *testing.T) {
	// Two separate filters: record passes if EITHER one matches.
	fc := filterCriteria(
		`{"eventName": ["INSERT"]}`,
		`{"eventName": ["REMOVE"]}`,
	)
	for _, name := range []string{"INSERT", "REMOVE"} {
		record := map[string]any{"eventName": name}
		if !matchesFilterCriteria(fc, record) {
			t.Fatalf("multipleFilters OR: %q should match one of the filters", name)
		}
	}
	record := map[string]any{"eventName": "MODIFY"}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("multipleFilters OR: MODIFY should not match either filter")
	}
}

// ─── AND within one filter ────────────────────────────────────────────────────

func TestMatchesFilterCriteria_AND_allMatch(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT"], "eventSource": ["aws:dynamodb"]}`)
	record := map[string]any{"eventName": "INSERT", "eventSource": "aws:dynamodb"}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("AND: both fields match — should pass")
	}
}

func TestMatchesFilterCriteria_AND_partialMatch(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT"], "eventSource": ["aws:dynamodb"]}`)
	record := map[string]any{"eventName": "MODIFY", "eventSource": "aws:dynamodb"}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("AND: one field mismatch — should fail")
	}
}

// ─── Nested object drill-down ─────────────────────────────────────────────────

func TestMatchesFilterCriteria_nestedObject_pass(t *testing.T) {
	// DynamoDB stream pattern: filter on the New Image city field.
	fc := filterCriteria(`{"dynamodb": {"NewImage": {"City": {"S": ["Seattle"]}}}}`)
	record := map[string]any{
		"dynamodb": map[string]any{
			"NewImage": map[string]any{
				"City": map[string]any{"S": "Seattle"},
			},
		},
	}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("nested object drill-down: should pass")
	}
}

func TestMatchesFilterCriteria_nestedObject_fail(t *testing.T) {
	fc := filterCriteria(`{"dynamodb": {"NewImage": {"City": {"S": ["Seattle"]}}}}`)
	record := map[string]any{
		"dynamodb": map[string]any{
			"NewImage": map[string]any{
				"City": map[string]any{"S": "Portland"},
			},
		},
	}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("nested object drill-down: city mismatch should fail")
	}
}

// ─── prefix ───────────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_prefix_pass(t *testing.T) {
	fc := filterCriteria(`{"awsRegion": [{"prefix": "us-"}]}`)
	if !matchesFilterCriteria(fc, map[string]any{"awsRegion": "us-east-1"}) {
		t.Fatal("prefix: us-east-1 should start with us-")
	}
}

func TestMatchesFilterCriteria_prefix_fail(t *testing.T) {
	fc := filterCriteria(`{"awsRegion": [{"prefix": "us-"}]}`)
	if matchesFilterCriteria(fc, map[string]any{"awsRegion": "eu-west-1"}) {
		t.Fatal("prefix: eu-west-1 should not start with us-")
	}
}

// ─── suffix ───────────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_suffix_pass(t *testing.T) {
	fc := filterCriteria(`{"key": [{"suffix": ".png"}]}`)
	if !matchesFilterCriteria(fc, map[string]any{"key": "photo.png"}) {
		t.Fatal("suffix: photo.png should end with .png")
	}
}

func TestMatchesFilterCriteria_suffix_fail(t *testing.T) {
	fc := filterCriteria(`{"key": [{"suffix": ".png"}]}`)
	if matchesFilterCriteria(fc, map[string]any{"key": "photo.jpg"}) {
		t.Fatal("suffix: photo.jpg should not end with .png")
	}
}

// ─── equals-ignore-case ───────────────────────────────────────────────────────

func TestMatchesFilterCriteria_equalsIgnoreCase_pass(t *testing.T) {
	fc := filterCriteria(`{"status": [{"equals-ignore-case": "active"}]}`)
	for _, v := range []string{"active", "Active", "ACTIVE", "aCtIvE"} {
		if !matchesFilterCriteria(fc, map[string]any{"status": v}) {
			t.Fatalf("equals-ignore-case: %q should match 'active'", v)
		}
	}
}

func TestMatchesFilterCriteria_equalsIgnoreCase_fail(t *testing.T) {
	fc := filterCriteria(`{"status": [{"equals-ignore-case": "active"}]}`)
	if matchesFilterCriteria(fc, map[string]any{"status": "inactive"}) {
		t.Fatal("equals-ignore-case: 'inactive' should not match 'active'")
	}
}

// ─── exists ───────────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_exists_true_pass(t *testing.T) {
	fc := filterCriteria(`{"errorCode": [{"exists": true}]}`)
	if !matchesFilterCriteria(fc, map[string]any{"errorCode": "THROTTLE"}) {
		t.Fatal("exists true: field is present — should pass")
	}
}

func TestMatchesFilterCriteria_exists_true_fail(t *testing.T) {
	fc := filterCriteria(`{"errorCode": [{"exists": true}]}`)
	if matchesFilterCriteria(fc, map[string]any{}) {
		t.Fatal("exists true: field absent — should fail")
	}
}

func TestMatchesFilterCriteria_exists_false_pass(t *testing.T) {
	fc := filterCriteria(`{"errorCode": [{"exists": false}]}`)
	if !matchesFilterCriteria(fc, map[string]any{}) {
		t.Fatal("exists false: field absent — should pass")
	}
}

func TestMatchesFilterCriteria_exists_false_fail(t *testing.T) {
	fc := filterCriteria(`{"errorCode": [{"exists": false}]}`)
	if matchesFilterCriteria(fc, map[string]any{"errorCode": "THROTTLE"}) {
		t.Fatal("exists false: field is present — should fail")
	}
}

// ─── anything-but ─────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_anythingBut_arrayExclusion(t *testing.T) {
	fc := filterCriteria(`{"weather": [{"anything-but": ["Raining", "Cloudy"]}]}`)

	// should pass for values NOT in the list
	for _, v := range []string{"Sunny", "Windy"} {
		if !matchesFilterCriteria(fc, map[string]any{"weather": v}) {
			t.Fatalf("anything-but: %q is not in exclusion list — should pass", v)
		}
	}
	// should fail for values IN the list
	for _, v := range []string{"Raining", "Cloudy"} {
		if matchesFilterCriteria(fc, map[string]any{"weather": v}) {
			t.Fatalf("anything-but: %q is in exclusion list — should fail", v)
		}
	}
}

func TestMatchesFilterCriteria_anythingBut_singleValue(t *testing.T) {
	fc := filterCriteria(`{"status": [{"anything-but": "DISABLED"}]}`)
	if !matchesFilterCriteria(fc, map[string]any{"status": "ACTIVE"}) {
		t.Fatal("anything-but single: ACTIVE should pass")
	}
	if matchesFilterCriteria(fc, map[string]any{"status": "DISABLED"}) {
		t.Fatal("anything-but single: DISABLED should fail")
	}
}

// ─── numeric ─────────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_numeric_equal(t *testing.T) {
	fc := filterCriteria(`{"price": [{"numeric": ["=", 100]}]}`)
	if !matchesFilterCriteria(fc, map[string]any{"price": float64(100)}) {
		t.Fatal("numeric =: 100 == 100 should pass")
	}
	if matchesFilterCriteria(fc, map[string]any{"price": float64(99)}) {
		t.Fatal("numeric =: 99 == 100 should fail")
	}
}

func TestMatchesFilterCriteria_numeric_range(t *testing.T) {
	// price > 10 AND price <= 20
	fc := filterCriteria(`{"price": [{"numeric": [">", 10, "<=", 20]}]}`)

	passing := []float64{11, 15, 20}
	failing := []float64{10, 21, 0}
	for _, v := range passing {
		if !matchesFilterCriteria(fc, map[string]any{"price": v}) {
			t.Fatalf("numeric range: %v should pass (> 10, <= 20)", v)
		}
	}
	for _, v := range failing {
		if matchesFilterCriteria(fc, map[string]any{"price": v}) {
			t.Fatalf("numeric range: %v should fail (> 10, <= 20)", v)
		}
	}
}

func TestMatchesFilterCriteria_numeric_greaterThan(t *testing.T) {
	fc := filterCriteria(`{"count": [{"numeric": [">", 5]}]}`)
	if !matchesFilterCriteria(fc, map[string]any{"count": float64(6)}) {
		t.Fatal("numeric >: 6 > 5 should pass")
	}
	if matchesFilterCriteria(fc, map[string]any{"count": float64(5)}) {
		t.Fatal("numeric >: 5 > 5 should fail (strict greater-than)")
	}
}

// ─── $or operator ────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_dollar_or(t *testing.T) {
	// Location is "New York" OR Day is "Monday".
	fc := filterCriteria(`{"$or": [{"Location": ["New York"]}, {"Day": ["Monday"]}]}`)

	if !matchesFilterCriteria(fc, map[string]any{"Location": "New York"}) {
		t.Fatal("$or: Location=New York should pass")
	}
	if !matchesFilterCriteria(fc, map[string]any{"Day": "Monday"}) {
		t.Fatal("$or: Day=Monday should pass")
	}
	if matchesFilterCriteria(fc, map[string]any{"Location": "Boston", "Day": "Friday"}) {
		t.Fatal("$or: neither condition matches — should fail")
	}
}

// ─── SQS body JSON string parsing ────────────────────────────────────────────

func TestMatchesFilterCriteria_sqsBodyParsed(t *testing.T) {
	// Filter on a nested JSON field inside the SQS message body (a string).
	fc := filterCriteria(`{"body": {"City": ["Seattle"]}}`)

	// body is a JSON string that contains City=Seattle → should pass
	record := map[string]any{
		"body": `{"City":"Seattle","Temp":72}`,
	}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("SQS body JSON filter: City=Seattle should pass")
	}

	// City=Portland → should fail
	record["body"] = `{"City":"Portland","Temp":60}`
	if matchesFilterCriteria(fc, record) {
		t.Fatal("SQS body JSON filter: City=Portland should fail")
	}
}

func TestMatchesFilterCriteria_sqsBodyNotJSON(t *testing.T) {
	// A body filter pattern with nested object requires the body to be valid
	// JSON. A plain string body cannot be matched against a nested pattern.
	fc := filterCriteria(`{"body": {"City": ["Seattle"]}}`)
	record := map[string]any{
		"body": "just a plain string",
	}
	if matchesFilterCriteria(fc, record) {
		t.Fatal("non-JSON body should not match a nested object pattern")
	}
}

// ─── DynamoDB realistic scenario ─────────────────────────────────────────────

func TestMatchesFilterCriteria_dynamoDB_insertOnly(t *testing.T) {
	fc := filterCriteria(`{"eventName": ["INSERT"]}`)

	insertRecord := map[string]any{
		"eventID":        "abc123",
		"eventName":      "INSERT",
		"eventSource":    "aws:dynamodb",
		"eventVersion":   "1.1",
		"eventSourceARN": "arn:aws:dynamodb:us-east-1:123456789012:table/MyTable/stream/2024-01-01T00:00:00.000",
		"awsRegion":      "us-east-1",
		"dynamodb": map[string]any{
			"Keys":           map[string]any{"PK": map[string]any{"S": "row1"}},
			"NewImage":       map[string]any{"PK": map[string]any{"S": "row1"}, "data": map[string]any{"S": "hello"}},
			"SequenceNumber": "000001",
			"StreamViewType": "NEW_AND_OLD_IMAGES",
		},
	}

	modifyRecord := map[string]any{
		"eventID":     "def456",
		"eventName":   "MODIFY",
		"eventSource": "aws:dynamodb",
	}

	removeRecord := map[string]any{
		"eventID":     "ghi789",
		"eventName":   "REMOVE",
		"eventSource": "aws:dynamodb",
	}

	if !matchesFilterCriteria(fc, insertRecord) {
		t.Fatal("DynamoDB INSERT record should pass INSERT-only filter")
	}
	if matchesFilterCriteria(fc, modifyRecord) {
		t.Fatal("DynamoDB MODIFY record should not pass INSERT-only filter")
	}
	if matchesFilterCriteria(fc, removeRecord) {
		t.Fatal("DynamoDB REMOVE record should not pass INSERT-only filter")
	}
}

func TestMatchesFilterCriteria_dynamoDB_newImageValue(t *testing.T) {
	// Filter on a specific attribute value in the NewImage.
	fc := filterCriteria(`{"dynamodb": {"NewImage": {"status": {"S": ["active"]}}}}`)

	matching := map[string]any{
		"dynamodb": map[string]any{
			"NewImage": map[string]any{
				"status": map[string]any{"S": "active"},
			},
		},
	}
	notMatching := map[string]any{
		"dynamodb": map[string]any{
			"NewImage": map[string]any{
				"status": map[string]any{"S": "inactive"},
			},
		},
	}

	if !matchesFilterCriteria(fc, matching) {
		t.Fatal("should pass for NewImage.status.S=active")
	}
	if matchesFilterCriteria(fc, notMatching) {
		t.Fatal("should fail for NewImage.status.S=inactive")
	}
}

// TestMatchesFilterCriteria_dynamoDB_newImageTypedItem is a regression test for
// a bug where toEventMap could not convert map[string]map[string]any to
// map[string]any. buildDynamoDBRecord stores payload.NewImage — whose concrete
// type is Item = map[string]map[string]any — directly in the record map. When
// matchField tried to drill into NewImage via toEventMap, the type assertion
// v.(map[string]any) failed (wrong concrete type), causing every filter that
// referenced dynamodb.NewImage to silently drop all records.
func TestMatchesFilterCriteria_dynamoDB_newImageTypedItem(t *testing.T) {
	// Given: a filter pattern matching INSERT records with ItemType.S = "MESSAGE".
	fc := filterCriteria(`{"eventName":["INSERT"],"dynamodb":{"NewImage":{"ItemType":{"S":["MESSAGE"]}}}}`)

	// NewImage is typed as map[string]map[string]any — exactly what
	// buildDynamoDBRecord puts in the record (Item = map[string]map[string]any).
	var newImage map[string]map[string]any = map[string]map[string]any{
		"PK":       {"S": "item#123"},
		"ItemType": {"S": "MESSAGE"},
		"Body":     {"S": "hello"},
	}

	matching := map[string]any{
		"eventName":   "INSERT",
		"eventSource": "aws:dynamodb",
		"dynamodb": map[string]any{
			"NewImage": newImage,
		},
	}
	// Same image but wrong ItemType value.
	var newImageWrong map[string]map[string]any = map[string]map[string]any{
		"PK":       {"S": "item#456"},
		"ItemType": {"S": "NOTIFICATION"},
	}
	notMatching := map[string]any{
		"eventName":   "INSERT",
		"eventSource": "aws:dynamodb",
		"dynamodb": map[string]any{
			"NewImage": newImageWrong,
		},
	}

	// Then: matching record should pass the filter.
	if !matchesFilterCriteria(fc, matching) {
		t.Fatal("INSERT record with NewImage.ItemType.S=MESSAGE should match; toEventMap likely failed to handle map[string]map[string]any")
	}
	// And: non-matching record should be dropped.
	if matchesFilterCriteria(fc, notMatching) {
		t.Fatal("INSERT record with NewImage.ItemType.S=NOTIFICATION should not match the MESSAGE filter")
	}
}

// TestMatchesFilterCriteria_dynamoDB_oldImageTypedItem is a regression test for
// the same toEventMap type-mismatch bug as the NewImage variant above, applied
// to OldImage (present on MODIFY and REMOVE events).
func TestMatchesFilterCriteria_dynamoDB_oldImageTypedItem(t *testing.T) {
	fc := filterCriteria(`{"eventName":["REMOVE"],"dynamodb":{"OldImage":{"Status":{"S":["DELETED"]}}}}`)

	var oldImage map[string]map[string]any = map[string]map[string]any{
		"PK":     {"S": "item#789"},
		"Status": {"S": "DELETED"},
	}
	matching := map[string]any{
		"eventName": "REMOVE",
		"dynamodb": map[string]any{
			"OldImage": oldImage,
		},
	}

	if !matchesFilterCriteria(fc, matching) {
		t.Fatal("REMOVE record with OldImage.Status.S=DELETED should match; toEventMap likely failed to handle map[string]map[string]any")
	}
}

// TestMatchesFilterCriteria_dynamoDB_keysTypedItem is a regression test for
// the same toEventMap type-mismatch bug applied to the Keys field.
func TestMatchesFilterCriteria_dynamoDB_keysTypedItem(t *testing.T) {
	fc := filterCriteria(`{"dynamodb":{"Keys":{"PK":{"S":["item#123"]}}}}`)

	var keys map[string]map[string]any = map[string]map[string]any{
		"PK": {"S": "item#123"},
	}
	matching := map[string]any{
		"eventName": "INSERT",
		"dynamodb": map[string]any{
			"Keys": keys,
		},
	}

	if !matchesFilterCriteria(fc, matching) {
		t.Fatal("record with Keys.PK.S=item#123 should match; toEventMap likely failed to handle map[string]map[string]any")
	}
}

// ─── Edge cases ───────────────────────────────────────────────────────────────

func TestMatchesFilterCriteria_emptyPattern_passAll(t *testing.T) {
	fc := &FilterCriteria{Filters: []Filter{{Pattern: ""}}}
	record := map[string]any{"anything": "at all"}
	if !matchesFilterCriteria(fc, record) {
		t.Fatal("empty pattern string should pass all records")
	}
}

func TestMatchesFilterCriteria_invalidJSON_drops(t *testing.T) {
	fc := &FilterCriteria{Filters: []Filter{{Pattern: "not json"}}}
	record := map[string]any{"anything": "at all"}
	// An unparseable pattern should NOT match (fail safe).
	if matchesFilterCriteria(fc, record) {
		t.Fatal("invalid JSON pattern should not match any record")
	}
}

// ─── SQS attributes subfield filtering ───────────────────────────────────────

// TestMatchesFilterCriteria_sqsAttributes_subfield exercises the path where the
// filter drills into the SQS `attributes` map. The record's attributes value
// must be map[string]any (not map[string]string) for toEventMap to succeed.
// This guards against a regression where attrs were stored as map[string]string
// and nested attribute matching silently failed.
func TestMatchesFilterCriteria_sqsAttributes_subfield(t *testing.T) {
	fc := filterCriteria(`{"attributes": {"ApproximateReceiveCount": ["1"]}}`)

	// Simulate what filterAndDeleteSQS now puts in the record: map[string]any.
	passing := map[string]any{
		"body": "hello",
		"attributes": map[string]any{
			"ApproximateReceiveCount": "1",
			"SentTimestamp":           "1545082649183",
		},
	}
	failing := map[string]any{
		"body": "hello",
		"attributes": map[string]any{
			"ApproximateReceiveCount": "5",
		},
	}

	if !matchesFilterCriteria(fc, passing) {
		t.Fatal("attributes subfield filter: ApproximateReceiveCount=1 should pass")
	}
	if matchesFilterCriteria(fc, failing) {
		t.Fatal("attributes subfield filter: ApproximateReceiveCount=5 should fail")
	}
}
