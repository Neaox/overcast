package scheduler

// The wire shape of Scheduler's tag members, exercised as an SDK sends them.
//
// AWS models `Tags` on TagResource and CreateScheduleGroup as a TagList — a
// list of `{Key, Value}` structures — and returns the same list from
// ListTagsForResource. Overcast decoded it as a JSON object, so every generated
// client's request body failed to parse with a SerializationException and no
// caller could tag a schedule at all. Decoding a Go struct that happens to
// round-trip would not have caught that; these tests go through the HTTP
// handlers with the bytes the AWS CLI puts on the wire.

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

// newTagsWireRouter builds the routing surface an SDK actually reaches: the
// service's own REST-JSON bindings plus the shared /tags path space the main
// router mounts TagsRouter behind.
func newTagsWireRouter(t *testing.T) (*Service, chi.Router) {
	t.Helper()
	s := New(
		&config.Config{Region: "us-east-1", AccountID: "000000000000"},
		state.NewMemoryStore(),
		zap.NewNop(),
		clock.NewMock(),
	)
	r := chi.NewRouter()
	s.RegisterRoutes(r)
	r.Mount("/tags", s.TagsRouter())
	return s, r
}

// tagsWireDo sends one request and returns the status and body. Requests are
// built from a raw path so the ARN stays percent-encoded in a single segment,
// which is how SDK clients address /tags/{ResourceArn}.
func tagsWireDo(t *testing.T, r chi.Router, method, path, body string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

// tagsPath renders the /tags/{ResourceArn} path with the ARN escaped into one
// segment, as an SDK does.
func tagsPath(arn string) string { return "/tags/" + url.PathEscape(arn) }

// wireTags decodes a ListTagsForResource body into the modeled list shape. A
// response that renders Tags as an object fails here, which is the response-side
// half of the same bug.
func wireTags(t *testing.T, body string) []struct{ Key, Value string } {
	t.Helper()
	var out struct {
		Tags []struct{ Key, Value string } `json:"Tags"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil {
		t.Fatalf("ListTagsForResource body %s is not the modeled TagList shape: %v", body, err)
	}
	return out.Tags
}

// seedWireSchedule creates a schedule through its REST binding and returns its
// ARN.
func seedWireSchedule(t *testing.T, s *Service, r chi.Router, name string) string {
	t.Helper()
	code, body := tagsWireDo(t, r, http.MethodPost, "/schedules/"+name, `{
		"ScheduleExpression": "rate(1 hour)",
		"FlexibleTimeWindow": {"Mode": "OFF"},
		"Target": {
			"Arn": "arn:aws:lambda:us-east-1:000000000000:function:noop",
			"RoleArn": "arn:aws:iam::000000000000:role/noop"
		}
	}`)
	if code != http.StatusOK {
		t.Fatalf("CreateSchedule = %d %s", code, body)
	}
	return s.scheduleARN("us-east-1", defaultGroup, name)
}

// TestTagResource_acceptsModeledTagList: the request body every AWS SDK and
// `aws scheduler tag-resource` sends is a JSON array of {Key, Value}. Decoding
// it as a map answered SerializationException, so tagging a schedule was
// impossible from outside.
func TestTagResource_acceptsModeledTagList(t *testing.T) {
	// Given: a schedule
	s, r := newTagsWireRouter(t)
	arn := seedWireSchedule(t, s, r, "nightly")

	// When: it is tagged with the modeled list shape
	code, body := tagsWireDo(t, r, http.MethodPost, tagsPath(arn),
		`{"Tags":[{"Key":"env","Value":"prod"},{"Key":"team","Value":"platform"}]}`)

	// Then: the request parses and succeeds
	if code != http.StatusOK {
		t.Fatalf("TagResource = %d %s, want 200", code, body)
	}

	// And: ListTagsForResource answers with the same list shape
	code, body = tagsWireDo(t, r, http.MethodGet, tagsPath(arn), "")
	if code != http.StatusOK {
		t.Fatalf("ListTagsForResource = %d %s, want 200", code, body)
	}
	tags := wireTags(t, body)
	if len(tags) != 2 {
		t.Fatalf("Tags = %s, want 2 entries", body)
	}
	// TagsToList orders by key, so the order is the response's contract.
	if tags[0].Key != "env" || tags[0].Value != "prod" {
		t.Fatalf("Tags[0] = %+v, want env=prod", tags[0])
	}
	if tags[1].Key != "team" || tags[1].Value != "platform" {
		t.Fatalf("Tags[1] = %+v, want team=platform", tags[1])
	}
}

// TestListTagsForResource_untaggedRendersEmptyList: an untagged resource answers
// an empty list, not an empty object — a client decoding into TagList must not
// see `{}` where it expects `[]`.
func TestListTagsForResource_untaggedRendersEmptyList(t *testing.T) {
	s, r := newTagsWireRouter(t)
	arn := seedWireSchedule(t, s, r, "untagged")

	code, body := tagsWireDo(t, r, http.MethodGet, tagsPath(arn), "")
	if code != http.StatusOK {
		t.Fatalf("ListTagsForResource = %d %s, want 200", code, body)
	}
	if !strings.Contains(body, `"Tags":[]`) {
		t.Fatalf("body = %s, want an empty TagList", body)
	}
}

// TestUntagResource_removesByTagKeys: UntagResource carries TagKeys as a
// querystring list of plain strings, which is the one member of the three that
// was already the modeled shape. It is covered here so the round trip a caller
// actually performs — tag, read, untag, read — is tested end to end.
func TestUntagResource_removesByTagKeys(t *testing.T) {
	// Given: a schedule carrying two tags
	s, r := newTagsWireRouter(t)
	arn := seedWireSchedule(t, s, r, "chores")
	if code, body := tagsWireDo(t, r, http.MethodPost, tagsPath(arn),
		`{"Tags":[{"Key":"env","Value":"prod"},{"Key":"team","Value":"platform"}]}`); code != http.StatusOK {
		t.Fatalf("TagResource = %d %s", code, body)
	}

	// When: one key is removed
	code, body := tagsWireDo(t, r, http.MethodDelete, tagsPath(arn)+"?TagKeys=env", "")
	if code != http.StatusOK {
		t.Fatalf("UntagResource = %d %s, want 200", code, body)
	}

	// Then: only the other tag is left
	code, body = tagsWireDo(t, r, http.MethodGet, tagsPath(arn), "")
	if code != http.StatusOK {
		t.Fatalf("ListTagsForResource = %d %s, want 200", code, body)
	}
	tags := wireTags(t, body)
	if len(tags) != 1 || tags[0].Key != "team" {
		t.Fatalf("Tags = %s, want only team", body)
	}
}

// TestCreateScheduleGroup_acceptsInlineTagList: CreateScheduleGroup takes the
// same TagList inline. Its handler ignores a body it cannot decode — Tags is the
// only body member and it is optional — so the wrong shape dropped the tags
// silently rather than erroring.
func TestCreateScheduleGroup_acceptsInlineTagList(t *testing.T) {
	// Given/When: a group is created with inline tags in the modeled shape
	s, r := newTagsWireRouter(t)
	code, body := tagsWireDo(t, r, http.MethodPost, "/schedule-groups/batch",
		`{"Tags":[{"Key":"env","Value":"prod"}]}`)
	if code != http.StatusOK {
		t.Fatalf("CreateScheduleGroup = %d %s, want 200", code, body)
	}

	// Then: the tags were kept
	code, body = tagsWireDo(t, r, http.MethodGet, tagsPath(s.groupARN("us-east-1", "batch")), "")
	if code != http.StatusOK {
		t.Fatalf("ListTagsForResource = %d %s, want 200", code, body)
	}
	tags := wireTags(t, body)
	if len(tags) != 1 || tags[0].Key != "env" || tags[0].Value != "prod" {
		t.Fatalf("Tags = %s, want env=prod", body)
	}
}

// TestCreateSchedule_hasNoTagsMember records a deliberate non-change.
//
// The issue reported inline Tags being dropped on CreateSchedule too, but AWS's
// model (scheduler-2021-06-30) gives CreateScheduleInput no Tags member at all —
// only CreateScheduleGroup takes tags inline. Accepting one here would be an
// emulator invention, so a Tags member on CreateSchedule is ignored, exactly as
// AWS ignores any unmodeled member, and must not become a serialization error.
func TestCreateSchedule_hasNoTagsMember(t *testing.T) {
	s, r := newTagsWireRouter(t)

	code, body := tagsWireDo(t, r, http.MethodPost, "/schedules/tagged", `{
		"ScheduleExpression": "rate(1 hour)",
		"FlexibleTimeWindow": {"Mode": "OFF"},
		"Target": {
			"Arn": "arn:aws:lambda:us-east-1:000000000000:function:noop",
			"RoleArn": "arn:aws:iam::000000000000:role/noop"
		},
		"Tags": [{"Key": "env", "Value": "prod"}]
	}`)
	if code != http.StatusOK {
		t.Fatalf("CreateSchedule = %d %s, want 200", code, body)
	}

	code, body = tagsWireDo(t, r, http.MethodGet, tagsPath(s.scheduleARN("us-east-1", defaultGroup, "tagged")), "")
	if code != http.StatusOK {
		t.Fatalf("ListTagsForResource = %d %s, want 200", code, body)
	}
	if !strings.Contains(body, `"Tags":[]`) {
		t.Fatalf("body = %s, want an empty TagList — CreateSchedule has no Tags member", body)
	}
}
