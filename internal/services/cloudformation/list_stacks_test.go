package cloudformation

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

// AWS spells the same instant differently depending on the shape it hangs off:
// StackSummary and Stack both declare LastUpdatedTime, while
// StackResourceSummary declares LastUpdatedTimestamp. The SDKs' generated
// deserialisers match those names literally, so a response that picks the wrong
// one parses as an absent field rather than as an error — which is why the web
// console's "Last updated" column read "—" for every stack no matter how many
// times it had been updated.
//
// The decode targets below carry AWS's names, so an element we spell
// differently lands nowhere and leaves a zero value. That is the assertion; a
// substring match on the body would pass on a name no SDK reads.

type awsStackSummaryMember struct {
	StackName       string `xml:"StackName"`
	StackStatus     string `xml:"StackStatus"`
	CreationTime    string `xml:"CreationTime"`
	LastUpdatedTime string `xml:"LastUpdatedTime"`
	DeletionTime    string `xml:"DeletionTime"`
}

type awsListStacksResponse struct {
	Summaries []awsStackSummaryMember `xml:"ListStacksResult>StackSummaries>member"`
}

type awsStackMember struct {
	StackName       string `xml:"StackName"`
	LastUpdatedTime string `xml:"LastUpdatedTime"`
}

type awsDescribeStacksResponse struct {
	Stacks []awsStackMember `xml:"DescribeStacksResult>Stacks>member"`
}

const (
	testStackCreatedAt = "2026-08-09T10:00:00.000Z"
	testStackUpdatedAt = "2026-08-09T11:30:00.000Z"
	testStackDeletedAt = "2026-08-09T12:15:00.000Z"
)

// newListStacksTestHandler stores an updated stack and a deleted one — the two
// states that give the summary a LastUpdatedTime and a DeletionTime to carry.
func newListStacksTestHandler(t *testing.T) *Handler {
	t.Helper()
	svc := New(&config.Config{Region: "us-east-1", AccountID: "000000000000"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())

	created := mustParseStackTime(t, testStackCreatedAt)
	updated := mustParseStackTime(t, testStackUpdatedAt)
	deleted := mustParseStackTime(t, testStackDeletedAt)

	stacks := []*Stack{
		{
			StackName: "updated-stack", StackID: "updated-stack-id", Region: "us-east-1",
			TemplateBody: `{"Resources":{}}`, Status: StatusUpdateComplete,
			CreatedAt: created, UpdatedAt: &updated,
		},
		{
			StackName: "deleted-stack", StackID: "deleted-stack-id", Region: "us-east-1",
			TemplateBody: `{"Resources":{}}`, Status: StatusDeleteComplete,
			CreatedAt: created, DeletedAt: &deleted,
		},
	}
	for _, s := range stacks {
		if err := svc.handler.store.putStack(context.Background(), s); err != nil {
			t.Fatal(err)
		}
	}
	return svc.handler
}

func mustParseStackTime(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02T15:04:05.000Z", value)
	if err != nil {
		t.Fatalf("parse %q: %v", value, err)
	}
	return parsed
}

// listStacksBodies returns the wire body ListStacks produces on each of its two
// paths. Both build their own summaries, so a field added to one and forgotten
// on the other is a difference only a caller sees.
func listStacksBodies(t *testing.T, h *Handler) map[string][]byte {
	t.Helper()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("Action=ListStacks"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.ListStacks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ListStacks status = %d, want %d", rec.Code, http.StatusOK)
	}

	typed, aerr := h.listStacksTyped(context.Background(), &listStacksReq{})
	if aerr != nil {
		t.Fatalf("listStacksTyped: %v", aerr)
	}
	typedBody, err := xml.Marshal(typed)
	if err != nil {
		t.Fatalf("marshal typed response: %v", err)
	}

	return map[string][]byte{"query": rec.Body.Bytes(), "typed": typedBody}
}

func TestListStacks_carriesLastUpdatedTime(t *testing.T) {
	h := newListStacksTestHandler(t)
	for path, body := range listStacksBodies(t, h) {
		t.Run(path, func(t *testing.T) {
			summary := findStackSummary(t, body, "updated-stack")
			if summary.LastUpdatedTime != testStackUpdatedAt {
				t.Fatalf("LastUpdatedTime = %q, want %q", summary.LastUpdatedTime, testStackUpdatedAt)
			}
		})
	}
}

// A stack that has never been updated has no last-updated time on AWS, so the
// element must be absent rather than present and empty — an empty element
// deserialises to a zero timestamp, which reads as 1 January year 1.
func TestListStacks_omitsLastUpdatedTimeWhenNeverUpdated(t *testing.T) {
	h := newListStacksTestHandler(t)
	for path, body := range listStacksBodies(t, h) {
		t.Run(path, func(t *testing.T) {
			if strings.Contains(string(body), "<LastUpdatedTime></LastUpdatedTime>") {
				t.Fatalf("never-updated stack emitted an empty LastUpdatedTime:\n%s", body)
			}
			summary := findStackSummary(t, body, "deleted-stack")
			if summary.LastUpdatedTime != "" {
				t.Fatalf("LastUpdatedTime = %q, want empty", summary.LastUpdatedTime)
			}
		})
	}
}

func TestListStacks_carriesDeletionTime(t *testing.T) {
	h := newListStacksTestHandler(t)
	for path, body := range listStacksBodies(t, h) {
		t.Run(path, func(t *testing.T) {
			summary := findStackSummary(t, body, "deleted-stack")
			if summary.DeletionTime != testStackDeletedAt {
				t.Fatalf("DeletionTime = %q, want %q", summary.DeletionTime, testStackDeletedAt)
			}
		})
	}
}

// DescribeStacks had the same defect under a different name: Stack.LastUpdatedTime
// was emitted as LastUpdatedTimestamp, which is StackResourceSummary's spelling.
func TestDescribeStacks_carriesLastUpdatedTime(t *testing.T) {
	h := newListStacksTestHandler(t)

	rec := httptest.NewRecorder()
	form := "Action=DescribeStacks&StackName=updated-stack"
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.DescribeStacks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeStacks status = %d, want %d", rec.Code, http.StatusOK)
	}

	body := rec.Body.Bytes()
	if strings.Contains(string(body), "LastUpdatedTimestamp") {
		t.Fatalf("DescribeStacks used StackResourceSummary's element name:\n%s", body)
	}

	var decoded awsDescribeStacksResponse
	if err := xml.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal DescribeStacks response: %v", err)
	}
	if len(decoded.Stacks) != 1 {
		t.Fatalf("stacks = %d, want 1", len(decoded.Stacks))
	}
	if decoded.Stacks[0].LastUpdatedTime != testStackUpdatedAt {
		t.Fatalf("LastUpdatedTime = %q, want %q", decoded.Stacks[0].LastUpdatedTime, testStackUpdatedAt)
	}
}

func findStackSummary(t *testing.T, body []byte, stackName string) awsStackSummaryMember {
	t.Helper()
	var decoded awsListStacksResponse
	if err := xml.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal ListStacks response: %v", err)
	}
	for _, s := range decoded.Summaries {
		if s.StackName == stackName {
			return s
		}
	}
	t.Fatalf("stack %q missing from summaries:\n%s", stackName, body)
	return awsStackSummaryMember{}
}

// ── StackStatusFilter ──────────────────────────────────────────────────────

// seedFilterStacks plants one stack per status the filter tests select over,
// including a deleted one — ListStacks keeps those, unlike DescribeStacks.
// Each is in a distinct status, so asserting on names alone pins which
// statuses came back.
func seedFilterStacks(t *testing.T, st *cfnStore) {
	t.Helper()
	seedStack(t, st, "created", StatusCreateComplete)
	seedStack(t, st, "updated", StatusUpdateComplete)
	seedStack(t, st, "rolled-back", StatusRollbackComplete)
	seedStack(t, st, "deleted", StatusDeleteComplete)
}

func listedStackNames(t *testing.T, body []byte) []string {
	t.Helper()
	var decoded awsListStacksResponse
	if err := xml.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unmarshal ListStacks response: %v; body: %s", err, body)
	}
	names := make([]string, 0, len(decoded.Summaries))
	for _, s := range decoded.Summaries {
		names = append(names, s.StackName)
	}
	slices.Sort(names)
	return names
}

func typedStackNames(summaries []stackSummaryXML) []string {
	names := make([]string, 0, len(summaries))
	for _, s := range summaries {
		names = append(names, s.StackName)
	}
	slices.Sort(names)
	return names
}

// StackStatusFilter is a list of stack statuses; when present, ListStacks
// returns only stacks in one of them. Overcast advertised the filter in its
// capability table but never read the parameter, so a client asking for the
// CREATE_COMPLETE stacks got every stack in the region back — including the
// deleted ones — and had to filter client-side or trust a wrong answer.
//
// https://docs.aws.amazon.com/AWSCloudFormation/latest/APIReference/API_ListStacks.html
func TestListStacks_filtersByStackStatus(t *testing.T) {
	// Given: four stacks, each in a different status
	h, st := newRollbackTestHandler(t)
	seedFilterStacks(t, st)

	// When: two statuses are named in the filter
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ListStacks", map[string]string{
		"StackStatusFilter.member.1": StatusCreateComplete,
		"StackStatusFilter.member.2": StatusUpdateComplete,
	}))

	// Then: only the stacks in those statuses come back
	got := listedStackNames(t, rec.Body.Bytes())
	want := []string{"created", "updated"}
	if !slices.Equal(got, want) {
		t.Errorf("filtered stacks = %v, want %v", got, want)
	}
}

// A filter naming a status nothing is in is not an error — it is an empty
// list. This is the assertion that separates "the filter works" from "the
// filter is ignored": an ignored filter returns every stack here.
func TestListStacks_filterMatchingNothingReturnsNoStacks(t *testing.T) {
	// Given: four stacks, none of which failed to delete
	h, st := newRollbackTestHandler(t)
	seedFilterStacks(t, st)

	// When: the filter names only DELETE_FAILED
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ListStacks", map[string]string{
		"StackStatusFilter.member.1": StatusDeleteFailed,
	}))

	// Then: nothing is returned
	if got := listedStackNames(t, rec.Body.Bytes()); len(got) != 0 {
		t.Errorf("filtered stacks = %v, want none", got)
	}
}

// AWS: "If no StackStatusFilter is specified, summary information for all
// stacks is returned (including existing stacks and stacks that have been
// deleted)." DELETE_COMPLETE surviving is the part worth pinning — every
// other list in this service drops it.
func TestListStacks_withoutFilterReturnsEveryStack(t *testing.T) {
	// Given: four stacks, one of them deleted
	h, st := newRollbackTestHandler(t)
	seedFilterStacks(t, st)

	// When: ListStacks is called with no filter
	rec := httptest.NewRecorder()
	h.dispatch(rec, cfnPost("ListStacks", nil))

	// Then: all four come back
	got := listedStackNames(t, rec.Body.Bytes())
	want := []string{"created", "deleted", "rolled-back", "updated"}
	if !slices.Equal(got, want) {
		t.Errorf("stacks = %v, want %v", got, want)
	}
}

// The Query dispatch path and the typed path decode the request separately, so
// a parameter honoured by one can be dropped by the other.
func TestListStacksTyped_filtersByStackStatus(t *testing.T) {
	// Given: four stacks, each in a different status
	h, st := newRollbackTestHandler(t)
	seedFilterStacks(t, st)

	// When: two statuses are named in the filter
	resp, aerr := h.listStacksTyped(context.Background(), &listStacksReq{
		StackStatusFilter: []string{StatusCreateComplete, StatusUpdateComplete},
	})
	if aerr != nil {
		t.Fatalf("listStacksTyped: %v", aerr)
	}

	// Then: only the stacks in those statuses come back
	got := typedStackNames(resp.Result.StackSummaries)
	want := []string{"created", "updated"}
	if !slices.Equal(got, want) {
		t.Errorf("filtered stacks = %v, want %v", got, want)
	}
}

func TestListStacksTyped_withoutFilterReturnsEveryStack(t *testing.T) {
	// Given: four stacks, one of them deleted
	h, st := newRollbackTestHandler(t)
	seedFilterStacks(t, st)

	// When: the filter is absent
	resp, aerr := h.listStacksTyped(context.Background(), &listStacksReq{})
	if aerr != nil {
		t.Fatalf("listStacksTyped: %v", aerr)
	}

	// Then: all four come back
	got := typedStackNames(resp.Result.StackSummaries)
	want := []string{"created", "deleted", "rolled-back", "updated"}
	if !slices.Equal(got, want) {
		t.Errorf("stacks = %v, want %v", got, want)
	}
}

// The typed path is reached over the wire through the Query codec, which is
// what turns StackStatusFilter.member.N into the request struct's slice. A
// field the codec cannot populate looks identical to an ignored parameter.
func TestListStacksTyped_decodesStackStatusFilterFromQueryForm(t *testing.T) {
	// Given: four stacks and a wire-form request naming one status
	h, st := newRollbackTestHandler(t)
	seedFilterStacks(t, st)
	req := cfnPost("ListStacks", map[string]string{
		"StackStatusFilter.member.1": StatusRollbackComplete,
	})

	// When: the typed operation decodes and runs it
	rec := httptest.NewRecorder()
	h.typedOp["ListStacks"].Invoke(rec, req, codec.QueryXML)

	// Then: only the rolled-back stack comes back
	got := listedStackNames(t, rec.Body.Bytes())
	want := []string{"rolled-back"}
	if !slices.Equal(got, want) {
		t.Errorf("filtered stacks = %v, want %v", got, want)
	}
}
