package router_test

// trace_search_test.go — the deep-search endpoint, end to end.
//
// The unit tests in internal/trace prove the scan; this proves the wire: that a
// real request's real body is reachable through /_overcast/debug/traces/search, which is
// the whole claim being made to anyone using the trace viewer.

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

type deepSearchResponse struct {
	Matches []struct {
		RequestID string `json:"requestId"`
		Field     string `json:"field"`
		HopID     string `json:"hopId"`
		Label     string `json:"label"`
		Before    string `json:"before"`
		Text      string `json:"text"`
		After     string `json:"after"`
		Summary   struct {
			Path    string `json:"path"`
			Service string `json:"service"`
		} `json:"summary"`
	} `json:"matches"`
	NextCursor string `json:"nextCursor"`
	Done       bool   `json:"done"`
	Scanned    int    `json:"scanned"`
}

func deepSearch(t *testing.T, srv *helpers.TestServer, query string) deepSearchResponse {
	t.Helper()
	resp, err := http.Get(srv.URL + "/_overcast/debug/traces/search?q=" + url.QueryEscape(query))
	if err != nil {
		t.Fatalf("deep search: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out deepSearchResponse
	helpers.DecodeJSON(t, resp, &out)
	return out
}

func TestTraceSearch_findsATraceByItsResponseBody(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	// Given: a request that failed, so the emulator's own error text is in its
	// response body and nowhere in its path, service or ID.
	body := `Action=GetQueueUrl&Version=2012-11-05&QueueName=no-such-queue-anywhere`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("seed request: %v", err)
	}
	defer resp.Body.Close()
	requestID := resp.Header.Get("x-amzn-requestid")
	if requestID == "" {
		t.Fatal("seed request carried no request ID")
	}

	// When: the error's own wording is searched for — which is what someone
	// pastes in from a failing SDK call.
	result := deepSearch(t, srv, "NonExistentQueue")

	// Then: the trace comes back, naming where the match was.
	var found bool
	for _, m := range result.Matches {
		if m.RequestID != requestID {
			continue
		}
		found = true
		if m.Field == "" {
			t.Error("match does not say which field it hit")
		}
		if m.Before+m.Text+m.After == "" {
			t.Error("match carries no excerpt, so it says only that an answer exists somewhere")
		}
		if m.Summary.Path == "" {
			t.Error("match carries no summary, so the caller cannot render its row")
		}
	}
	if !found {
		t.Errorf("deep search did not find the trace by its response body; matches=%+v", result.Matches)
	}

	// And: the cheap search cannot — which is why this endpoint exists.
	listResp, err := http.Get(srv.URL + "/_overcast/debug/traces?search=" + url.QueryEscape("NonExistentQueue"))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	defer listResp.Body.Close()
	var list struct {
		Traces []struct {
			RequestID string `json:"requestId"`
		} `json:"traces"`
	}
	helpers.DecodeJSON(t, listResp, &list)
	for _, tr := range list.Traces {
		if tr.RequestID == requestID {
			t.Error("the list's own search reached the response body; the two searches have merged")
		}
	}
}

// A query of one or two characters matches nearly every body retained. Saying
// so beats returning an empty page a caller would read as "nothing matched".
func TestTraceSearch_refusesAQueryTooShortToMeanAnything(t *testing.T) {
	srv := helpers.NewTestServer(t, helpers.WithDebug(true))

	resp, err := http.Get(srv.URL + "/_overcast/debug/traces/search?q=ab")
	if err != nil {
		t.Fatalf("deep search: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusBadRequest)

	var out struct {
		Error     string `json:"error"`
		MinLength int    `json:"minLength"`
	}
	helpers.DecodeJSON(t, resp, &out)
	if out.MinLength == 0 {
		t.Error("the refusal does not say how long a query has to be")
	}
}

// Debug off means no ring buffer, and the endpoint has to say that rather than
// answer "no matches" — which would read as "your query is wrong".
func TestTraceSearch_saysWhenTracingIsOff(t *testing.T) {
	srv := helpers.NewTestServer(t)

	resp, err := http.Get(srv.URL + "/_overcast/debug/traces/search?q=anything")
	if err != nil {
		t.Fatalf("deep search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Errorf("deep search answered 200 with debug off; want a refusal that explains itself")
	}
}
