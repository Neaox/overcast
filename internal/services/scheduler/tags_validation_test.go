package scheduler

// What Scheduler refuses to store as a tag.
//
// Every other service that models tags runs them through
// serviceutil.ValidateTags, which enforces the constraints AWS documents
// identically across services: no reserved `aws:` key prefix, keys of at most
// 128 characters, values of at most 256, and a bounded count. Scheduler reached
// its tag store directly and applied none of them, so it accepted tags AWS
// rejects and a template that worked here failed on deploy.
//
// That gap was invisible until the wire shape was fixed (#1045), because before
// that no tag could be set through an SDK at all.
//
// These go through the HTTP handlers rather than calling the validator, because
// the bug was never in the validator — it was that nothing called it.

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// jsonString renders a Go string as a JSON string literal, so a case can carry
// a key with a quote or a backslash in it without hand-escaping.
func jsonString(s string) string { return strconv.Quote(s) }

// tagValidationCase is one tag AWS refuses, and why.
type tagValidationCase struct {
	name string
	key  string
	val  string
	why  string
}

var tagValidationCases = []tagValidationCase{
	{
		name: "reserved aws prefix",
		key:  "aws:cloudformation:stack-name",
		val:  "x",
		why:  "AWS reserves the aws: prefix for its own tags and refuses a caller writing one",
	},
	{
		name: "key over 128 characters",
		key:  strings.Repeat("k", 129),
		val:  "x",
		why:  "AWS caps a tag key at 128 characters",
	},
	{
		name: "value over 256 characters",
		key:  "size",
		val:  strings.Repeat("v", 257),
		why:  "AWS caps a tag value at 256 characters",
	},
	{
		name: "empty key",
		key:  "",
		val:  "x",
		why:  "a tag with no key cannot be addressed by UntagResource",
	},
	{
		name: "illegal character in key",
		key:  "bad%key",
		val:  "x",
		why:  "AWS documents the tag charset as letters, digits, spaces and _ . : / = + - @",
	},
}

// TestTagResource_refusesTagsAWSRefuses: the write path an SDK reaches.
func TestTagResource_refusesTagsAWSRefuses(t *testing.T) {
	for _, tc := range tagValidationCases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a schedule
			s, r := newTagsWireRouter(t)
			arn := seedWireSchedule(t, s, r, "nightly")

			// When: it is tagged with something AWS would refuse
			code, body := tagsWireDo(t, r, http.MethodPost, tagsPath(arn),
				`{"Tags":[{"Key":`+jsonString(tc.key)+`,"Value":`+jsonString(tc.val)+`}]}`)

			// Then: the call is refused, not silently stored
			if code != http.StatusBadRequest {
				t.Fatalf("TagResource = %d %s, want 400 — %s", code, body, tc.why)
			}
			if !strings.Contains(body, "ValidationException") {
				t.Errorf("error body = %s, want a ValidationException — the code Scheduler "+
					"uses for every other rejected input", body)
			}

			// And: nothing was written, so a refused call leaves no trace
			code, body = tagsWireDo(t, r, http.MethodGet, tagsPath(arn), "")
			if code != http.StatusOK {
				t.Fatalf("ListTagsForResource = %d %s", code, body)
			}
			if got := wireTags(t, body); len(got) != 0 {
				t.Errorf("tags after a refused TagResource = %v, want none stored", got)
			}
		})
	}
}

// TestCreateScheduleGroup_refusesTagsAWSRefuses: the other write path. Inline
// Tags on CreateScheduleGroup reach the store without passing TagResource, so
// validating one and not the other would leave the rule half-applied.
func TestCreateScheduleGroup_refusesTagsAWSRefuses(t *testing.T) {
	for _, tc := range tagValidationCases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a fresh service
			s, r := newTagsWireRouter(t)

			// When: a group is created carrying a tag AWS would refuse
			code, body := tagsWireDo(t, r, http.MethodPost, "/schedule-groups/batch",
				`{"Tags":[{"Key":`+jsonString(tc.key)+`,"Value":`+jsonString(tc.val)+`}]}`)

			// Then: the create is refused — %s
			if code != http.StatusBadRequest {
				t.Fatalf("CreateScheduleGroup = %d %s, want 400 — %s", code, body, tc.why)
			}

			// And: no group was created, so the refusal is not partial
			code, body = tagsWireDo(t, r, http.MethodGet, "/schedule-groups/batch", "")
			if code == http.StatusOK {
				t.Errorf("the group exists after a refused create: %s — a rejected "+
					"request must not leave the resource behind", body)
			}
			_ = s
		})
	}
}

// TestTagResource_acceptsWhatAWSAccepts guards the other side: validation that
// refuses legitimate tags would be worse than none, since it would break
// callers that work against AWS today.
func TestTagResource_acceptsWhatAWSAccepts(t *testing.T) {
	// Given: a schedule
	s, r := newTagsWireRouter(t)
	arn := seedWireSchedule(t, s, r, "nightly")

	// When: it is tagged with the awkward end of the legal charset — spaces,
	// the punctuation AWS allows, a 128-character key and a 256-character value
	code, body := tagsWireDo(t, r, http.MethodPost, tagsPath(arn),
		`{"Tags":[`+
			`{"Key":"cost centre","Value":"eng/platform+core"},`+
			`{"Key":"owner.email","Value":"a@b.example"},`+
			`{"Key":"path","Value":"a/b:c=d-e_f"},`+
			`{"Key":"`+strings.Repeat("k", 128)+`","Value":"`+strings.Repeat("v", 256)+`"}`+
			`]}`)

	// Then: all of them are stored
	if code != http.StatusOK {
		t.Fatalf("TagResource = %d %s, want 200 — these are all tags AWS accepts", code, body)
	}
	code, body = tagsWireDo(t, r, http.MethodGet, tagsPath(arn), "")
	if code != http.StatusOK {
		t.Fatalf("ListTagsForResource = %d %s", code, body)
	}
	if got := wireTags(t, body); len(got) != 4 {
		t.Errorf("stored %d tags, want 4: %v", len(got), got)
	}
}
