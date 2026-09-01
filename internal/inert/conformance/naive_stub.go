package conformance

import (
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// naiveWidget is the record the naive stub in this file actually keeps.
// This is the fixture the meta-test (meta_test.go) runs Check against to
// demonstrate the suite bites: naiveLogic is a deliberately sloppy
// hand-rolled emulator of the kind §2.4 of the plan describes — it commits
// exactly the sins that section calls out.
type naiveWidget struct {
	Name             string
	Description      string
	Size             string
	Status           string
	Arn              string
	CreationTime     string
	LastModifiedTime string
}

// naiveLogic is the protocol-agnostic business logic shared by every naive
// stub Fixture in this package (see naive_json.go, naive_query.go). Only
// wire encoding/decoding differs between protocols; the bugs below are
// identical everywhere, which is what lets the meta-test assert one
// expected violation set for every protocol family it exercises.
//
// The sins, each one deliberate and each one named in
// docs/plans/inert-tier-rollout.md §2.4 / the I0 brief:
//
//   - dropWidgetFields: only Description and Size survive Create — every
//     other field the caller sends is silently discarded (§3.2/roundtrip-fidelity).
//   - fabricated Status: always "READY", never the modeled default, never
//     absent (§3.2/no-fabrication).
//   - generic errors for missing records: "InternalError"/500 instead of
//     the modeled not-found code (§3.3/not-found, §3.1/delete-then-read).
//   - no duplicate detection: a second Create with the same identifier
//     silently overwrites (§3.3/already-exists).
//   - no required-field validation: Create never checks Size is set
//     (§3.3/invalid-parameter).
//   - invalid pagination tokens silently restart at page 1 instead of
//     erroring — exactly the pagination-plan H1/G3 failure mode
//     (§3.3/invalid-token).
//   - time.Now() instead of the injected clock.Clock (§3.5/timestamps).
//   - the idempotency token is never consulted, so a repeat Create without
//     an explicit name always mints a new record (§3.5/idempotency).
//   - the Ping verb operation fabricates a shape-correct success instead of
//     staying Tier 0 (§3.6/verb-default).
//
// What it gets right, on purpose, so the meta-test also proves the suite
// does not just fail everything: identifiers round-trip, List is sorted
// and genuinely paginates, Update does a real partial merge and refreshes
// LastModifiedTime, and the ARN template is correct.
type naiveLogic struct {
	service string
	region  string
	account string
	clk     *clock.Mock

	widgets map[string]*naiveWidget
	seq     int
}

func newNaiveLogic(service string, clk *clock.Mock) *naiveLogic {
	n := &naiveLogic{
		service: service,
		region:  "us-east-1",
		account: "000000000000",
		clk:     clk,
	}
	n.reset()
	return n
}

func (n *naiveLogic) reset() {
	n.widgets = map[string]*naiveWidget{}
	n.seq = 0
}

// timeNow is the naive stub's central §3.5 sin: it never touches n.clk.
func (n *naiveLogic) timeNow() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func (n *naiveLogic) arn(name string) string {
	return fmt.Sprintf("arn:aws:%s:%s:%s:widget/%s", n.service, n.region, n.account, name)
}

func stringField(fields map[string]any, key string) string {
	s, _ := fields[key].(string)
	return s
}

func (n *naiveLogic) project(w *naiveWidget) map[string]any {
	return map[string]any{
		"Name":             w.Name,
		"Description":      w.Description,
		"Size":             w.Size,
		"Status":           w.Status,
		"Arn":              w.Arn,
		"CreationTime":     w.CreationTime,
		"LastModifiedTime": w.LastModifiedTime,
	}
}

func (n *naiveLogic) create(fields map[string]any) (map[string]any, *protocol.AWSError) {
	name := stringField(fields, "Name")
	if name == "" {
		// Sin: no identifier supplied, and no ClientToken consulted either
		// (§3.5/idempotency) — every such call mints a fresh sequential id.
		n.seq++
		name = fmt.Sprintf("widget-%d", n.seq)
	}
	// Sin: no @required validation (Size is required in the model this
	// resource pretends to have) and no already-exists detection — a
	// second Create with the same Name silently overwrites below.
	now := n.timeNow()
	w := &naiveWidget{
		Name: name,
		// Sin: only two of the caller's fields survive — everything else
		// Input(InputFull, ...) sends (Owner, Notes, Category, Priority) is
		// dropped on the floor, mirroring transfer's 2-of-15-fields bug.
		Description:      stringField(fields, "Description"),
		Size:             stringField(fields, "Size"),
		Status:           "READY", // Sin: fabricated; never declared as a model default.
		Arn:              n.arn(name),
		CreationTime:     now,
		LastModifiedTime: now,
	}
	n.widgets[name] = w
	return n.project(w), nil
}

func (n *naiveLogic) read(fields map[string]any) (map[string]any, *protocol.AWSError) {
	w, ok := n.widgets[stringField(fields, "Name")]
	if !ok {
		// Sin: a generic error instead of the modeled not-found code — the
		// emulator-fidelity failure mode §3.3 exists to catch.
		return nil, &protocol.AWSError{Code: "InternalError", Message: "not found", HTTPStatus: http.StatusInternalServerError}
	}
	return n.project(w), nil
}

func (n *naiveLogic) update(fields map[string]any) (map[string]any, *protocol.AWSError) {
	w, ok := n.widgets[stringField(fields, "Name")]
	if !ok {
		return nil, &protocol.AWSError{Code: "InternalError", Message: "not found", HTTPStatus: http.StatusInternalServerError}
	}
	// This part is honest: a real partial merge, touching only fields the
	// caller actually sent, and refreshing LastModifiedTime.
	if v, ok := fields["Description"]; ok {
		w.Description = fmt.Sprint(v)
	}
	if v, ok := fields["Size"]; ok {
		w.Size = fmt.Sprint(v)
	}
	w.LastModifiedTime = n.timeNow()
	return n.project(w), nil
}

func (n *naiveLogic) delete(fields map[string]any) (map[string]any, *protocol.AWSError) {
	name := stringField(fields, "Name")
	if _, ok := n.widgets[name]; !ok {
		return nil, &protocol.AWSError{Code: "InternalError", Message: "not found", HTTPStatus: http.StatusInternalServerError}
	}
	delete(n.widgets, name)
	return map[string]any{}, nil
}

func (n *naiveLogic) list(fields map[string]any) (map[string]any, *protocol.AWSError) {
	names := make([]string, 0, len(n.widgets))
	for name := range n.widgets {
		names = append(names, name)
	}
	sort.Strings(names) // honest: stable-sorted by identifier.
	items := make([]*naiveWidget, 0, len(names))
	for _, name := range names {
		items = append(items, n.widgets[name])
	}

	limit := 0
	if v, ok := fields["MaxResults"]; ok {
		limit = toInt(v)
	}
	token := stringField(fields, "NextToken")

	page, err := serviceutil.Paginate(items, limit, token, serviceutil.PaginateOptions{DefaultLimit: 50, MaxLimit: 50})
	if err != nil {
		// Sin: an invalid continuation token silently restarts at page 1
		// instead of erroring — pagination-plan H1/G3.
		page, _ = serviceutil.Paginate(items, limit, "", serviceutil.PaginateOptions{DefaultLimit: 50, MaxLimit: 50})
	}
	widgets := make([]any, 0, len(page.Items))
	for _, w := range page.Items {
		widgets = append(widgets, n.project(w))
	}
	out := map[string]any{"Widgets": widgets}
	if page.NextToken != "" {
		out["NextToken"] = page.NextToken
	}
	return out, nil
}

// ping is the naive stub's Verb-classified operation. A faithful Tier 1
// implementation leaves it at Tier 0 (§3.6); this stub does the thing the
// contract exists to forbid: it fabricates a shape-correct success.
func (n *naiveLogic) ping(map[string]any) (map[string]any, *protocol.AWSError) {
	return map[string]any{"Result": "PONG"}, nil
}

func toInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case string:
		var n int
		if _, err := fmt.Sscanf(t, "%d", &n); err == nil {
			return n
		}
	}
	return 0
}

// naiveResourceOps is the ResourceOps every naive stub Fixture shares —
// only Encode/Decode/Handler differ per protocol.
func naiveResourceOps() ResourceOps {
	return ResourceOps{
		Create: "CreateWidget",
		Read:   "GetWidget",
		Update: "UpdateWidget",
		Delete: "DeleteWidget",
		List:   "ListWidgets",
		Verb:   "PingWidget",

		IDField:           "Name",
		ArnField:          "Arn",
		CreationTimeField: "CreationTime",
		ModifiedTimeField: "LastModifiedTime",

		RoundtripFields:  []string{"Description", "Size", "Owner", "Notes", "Category", "Priority"},
		OutputOnlyFields: []string{"Status"},
		Defaults:         map[string]any{}, // Status has no declared default: any value is fabrication.

		ItemsField:         "Widgets",
		TokenRequestField:  "NextToken",
		TokenResponseField: "NextToken",
		LimitField:         "MaxResults",
		IdempotencyField:   "ClientToken",
	}
}

// naiveErrorCodes is the modeled error surface every naive stub Fixture
// declares (§3.3) — what the stub *should* return, which is exactly what
// checkNotFound et al. assert against and the naive stub's generic errors
// fail to match.
func naiveErrorCodes() ErrorCodes {
	return ErrorCodes{
		NotFound:               "WidgetNotFoundException",
		NotFoundStatus:         http.StatusNotFound,
		AlreadyExists:          "WidgetAlreadyExistsException",
		AlreadyExistsStatus:    http.StatusConflict,
		InvalidParameter:       "InvalidParameterException",
		InvalidParameterStatus: http.StatusBadRequest,
		InvalidToken:           "InvalidNextTokenException",
		InvalidTokenStatus:     http.StatusBadRequest,
	}
}

func naiveInput(kind InputKind, seed int) map[string]any {
	switch kind {
	case InputFull:
		return map[string]any{
			"Name":        fmt.Sprintf("widget-%d", seed),
			"Description": fmt.Sprintf("desc-%d", seed),
			"Size":        "LARGE",
			"Owner":       fmt.Sprintf("owner-%d", seed),
			"Notes":       "some notes",
			"Category":    "general",
			"Priority":    "P1",
		}
	case InputMinimal:
		return map[string]any{
			"Name": fmt.Sprintf("widget-%d", seed),
			"Size": "LARGE",
		}
	case InputInvalid:
		// Missing the required Size field.
		return map[string]any{
			"Name": fmt.Sprintf("widget-%d", seed),
		}
	case InputUpdate:
		return map[string]any{
			"Name":        fmt.Sprintf("widget-%d", seed),
			"Description": "updated-description",
		}
	case InputIdempotent:
		// Deliberately no Name: the service must derive the identifier and
		// a repeat call with the same ClientToken must return it again.
		return map[string]any{
			"Size":        "LARGE",
			"ClientToken": fmt.Sprintf("token-%d", seed),
		}
	default:
		return map[string]any{}
	}
}
