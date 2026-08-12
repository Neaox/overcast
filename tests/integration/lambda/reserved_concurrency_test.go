package lambda_test

// reserved_concurrency_test.go — GetFunctionConcurrency's answer for a function
// that has no reservation.
//
// AWS returns 200 with an empty body. `ReservedConcurrentExecutions` is not a
// required member of the response
// (https://docs.aws.amazon.com/lambda/latest/api/API_GetFunctionConcurrency.html),
// and the operation's ResourceNotFoundException is documented as "the resource
// specified in the request does not exist" — the *function*, not its
// reservation. Overcast answered 404 instead, so an SDK client asking "does
// this function reserve concurrency?" got an exception rather than an answer.
//
// The empty body has to stay empty rather than becoming a zero. Zero is a real
// reservation with real behaviour — AWS's documented way to switch a function
// off, and the one place Overcast deliberately throttles (see AGENTS.md
// § Non-goals) — so reporting an unset reservation as 0 would be a worse bug
// than the 404 it replaced.

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

func reservedConcurrencyURL(srv *helpers.TestServer, name string) string {
	return srv.URL + "/2019-09-30/functions/" + name + "/concurrency"
}

func putReservedConcurrencyURL(srv *helpers.TestServer, name string) string {
	return srv.URL + "/2017-10-31/functions/" + name + "/concurrency"
}

// rawConcurrency keeps the member as a pointer so an absent one and an explicit
// zero stay distinguishable — which is the whole point of these cases.
type rawConcurrency struct {
	ReservedConcurrentExecutions *int `json:"ReservedConcurrentExecutions"`
}

func TestGetFunctionConcurrency_withoutAReservationReturnsAnEmptyBody(t *testing.T) {
	// Given: a function that has never had reserved concurrency set.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-unset-fn")

	// When: its concurrency is read.
	resp := doJSON(t, http.MethodGet, reservedConcurrencyURL(srv, "concurrency-unset-fn"), nil)

	// Then: AWS's 200 with an empty document, not a 404.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got rawConcurrency
	decodeJSON(t, resp, &got)
	if got.ReservedConcurrentExecutions != nil {
		t.Errorf("ReservedConcurrentExecutions = %d for a function with no reservation, want the member absent",
			*got.ReservedConcurrentExecutions)
	}
}

func TestGetFunctionConcurrency_reservationOfZeroIsReportedNotOmitted(t *testing.T) {
	// Given: a function switched off with a zero reservation — AWS's documented
	// idiom, and a real throttle here.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-zero-fn")
	put := doJSON(t, http.MethodPut, putReservedConcurrencyURL(srv, "concurrency-zero-fn"), map[string]any{
		"ReservedConcurrentExecutions": 0,
	})
	helpers.AssertStatus(t, put, http.StatusOK)
	put.Body.Close()

	// When: its concurrency is read.
	resp := doJSON(t, http.MethodGet, reservedConcurrencyURL(srv, "concurrency-zero-fn"), nil)

	// Then: the zero is reported. A caller cannot tell "off" from "unset"
	// otherwise, and those mean opposite things.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got rawConcurrency
	decodeJSON(t, resp, &got)
	if got.ReservedConcurrentExecutions == nil {
		t.Fatal("ReservedConcurrentExecutions is absent for a function reserved to 0, want 0")
	}
	if *got.ReservedConcurrentExecutions != 0 {
		t.Errorf("ReservedConcurrentExecutions = %d, want 0", *got.ReservedConcurrentExecutions)
	}
}

func TestGetFunctionConcurrency_reportsTheReservationThatWasSet(t *testing.T) {
	// Given: a function with a reservation.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-set-fn")
	put := doJSON(t, http.MethodPut, putReservedConcurrencyURL(srv, "concurrency-set-fn"), map[string]any{
		"ReservedConcurrentExecutions": 5,
	})
	helpers.AssertStatus(t, put, http.StatusOK)
	put.Body.Close()

	// When: its concurrency is read.
	resp := doJSON(t, http.MethodGet, reservedConcurrencyURL(srv, "concurrency-set-fn"), nil)

	// Then: the reservation comes back.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got rawConcurrency
	decodeJSON(t, resp, &got)
	if got.ReservedConcurrentExecutions == nil || *got.ReservedConcurrentExecutions != 5 {
		t.Errorf("ReservedConcurrentExecutions = %v, want 5", got.ReservedConcurrentExecutions)
	}
}

func TestGetFunctionConcurrency_afterDeleteReturnsAnEmptyBodyAgain(t *testing.T) {
	// Given: a function whose reservation has been removed.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-deleted-fn")
	put := doJSON(t, http.MethodPut, putReservedConcurrencyURL(srv, "concurrency-deleted-fn"), map[string]any{
		"ReservedConcurrentExecutions": 3,
	})
	helpers.AssertStatus(t, put, http.StatusOK)
	put.Body.Close()
	del := doJSON(t, http.MethodDelete, putReservedConcurrencyURL(srv, "concurrency-deleted-fn"), nil)
	helpers.AssertStatus(t, del, http.StatusNoContent)
	del.Body.Close()

	// When: its concurrency is read.
	resp := doJSON(t, http.MethodGet, reservedConcurrencyURL(srv, "concurrency-deleted-fn"), nil)

	// Then: it is unset again, not 0 and not a 404.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got rawConcurrency
	decodeJSON(t, resp, &got)
	if got.ReservedConcurrentExecutions != nil {
		t.Errorf("ReservedConcurrentExecutions = %d after delete, want the member absent",
			*got.ReservedConcurrentExecutions)
	}
}

func TestGetFunctionConcurrency_missingFunctionIsStillNotFound(t *testing.T) {
	// Given: a running server with no such function. The operation does model
	// ResourceNotFoundException — for the function, which is the case that must
	// keep returning it.
	srv := helpers.NewTestServer(t)

	// When: concurrency is read for a function that does not exist.
	resp := doJSON(t, http.MethodGet, reservedConcurrencyURL(srv, "concurrency-absent-fn"), nil)

	// Then: 404, as AWS answers.
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

// TestGetFunctionConcurrency_emptyBodyIsAnObject pins the wire shape rather
// than the decoded struct: an SDK expects `{}`, and a handler that wrote no
// bytes at all would satisfy every assertion above while breaking clients that
// parse the body.
func TestGetFunctionConcurrency_emptyBodyIsAnObject(t *testing.T) {
	// Given: a function with no reservation.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "concurrency-shape-fn")

	// When: its concurrency is read.
	resp := doJSON(t, http.MethodGet, reservedConcurrencyURL(srv, "concurrency-shape-fn"), nil)
	helpers.AssertStatus(t, resp, http.StatusOK)
	defer resp.Body.Close()

	// Then: the body is a JSON object, and an empty one.
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode body as a JSON object: %v", err)
	}
	if len(body) != 0 {
		t.Errorf("body = %v, want an empty object", body)
	}
}
