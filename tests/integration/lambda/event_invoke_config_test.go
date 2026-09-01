package lambda_test

// event_invoke_config_test.go — the FunctionEventInvokeConfig family.
//
// This is the API that configures what happens around an asynchronous
// invocation: how many times it is retried, how long an event may sit before it
// is discarded, and where a record of the outcome is sent. Overcast implemented
// the *effects* of AWS's defaults before it implemented the API that changes
// them, so until now every function got two retries and no destinations, with
// no way to say otherwise.
//
// Paths are AWS's, and they are not the function API's /2015-03-31 base:
// PUT/POST/GET/DELETE on /2019-09-25/functions/{name}/event-invoke-config, and
// the list on .../event-invoke-config/list. internal/router's model-binding
// check enforces that against the pinned model, so a wrong path here fails
// there rather than merely 404ing.

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func eventInvokeURL(srv *helpers.TestServer, name string) string {
	return srv.URL + "/2019-09-25/functions/" + name + "/event-invoke-config"
}

func eventInvokeListURL(srv *helpers.TestServer, name string) string {
	return srv.URL + "/2019-09-25/functions/" + name + "/event-invoke-config/list"
}

// eventInvokeConfigResp is AWS's FunctionEventInvokeConfig shape. Every member
// is a pointer or a slice so an absent one stays distinguishable from a zero —
// MaximumRetryAttempts of 0 is a real setting, meaning "do not retry".
type eventInvokeConfigResp struct {
	FunctionArn              string `json:"FunctionArn"`
	LastModified             *int64 `json:"LastModified"`
	MaximumRetryAttempts     *int   `json:"MaximumRetryAttempts"`
	MaximumEventAgeInSeconds *int   `json:"MaximumEventAgeInSeconds"`
	DestinationConfig        *struct {
		OnSuccess *struct {
			Destination string `json:"Destination"`
		} `json:"OnSuccess"`
		OnFailure *struct {
			Destination string `json:"Destination"`
		} `json:"OnFailure"`
	} `json:"DestinationConfig"`
}

func TestPutFunctionEventInvokeConfig_storesAndEchoesTheWholeConfiguration(t *testing.T) {
	// Given: a function.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-put-fn")

	// When: an event-invoke configuration is put on it.
	resp := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-put-fn"), map[string]any{
		"MaximumRetryAttempts":     1,
		"MaximumEventAgeInSeconds": 120,
		"DestinationConfig": map[string]any{
			"OnSuccess": map[string]any{"Destination": "arn:aws:sqs:us-east-1:000000000000:eic-success"},
			"OnFailure": map[string]any{"Destination": "arn:aws:sqs:us-east-1:000000000000:eic-failure"},
		},
	})

	// Then: it comes back in full, with the function's ARN and a timestamp.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got eventInvokeConfigResp
	decodeJSON(t, resp, &got)
	assertRetryPolicy(t, "PutFunctionEventInvokeConfig", got, 1, 120)
	if got.FunctionArn == "" {
		t.Error("FunctionArn is empty, want the function's ARN")
	}
	// LastModified is a Unix-seconds number here, not the RFC 3339 string
	// FunctionConfiguration uses. Decoding it as an int64 is the assertion.
	if got.LastModified == nil || *got.LastModified <= 0 {
		t.Errorf("LastModified = %v, want Unix seconds", got.LastModified)
	}
	assertDestination(t, "OnSuccess", destinationOf(got, true), "arn:aws:sqs:us-east-1:000000000000:eic-success")
	assertDestination(t, "OnFailure", destinationOf(got, false), "arn:aws:sqs:us-east-1:000000000000:eic-failure")

	// And: GetFunctionEventInvokeConfig reports the same thing.
	fetched := doJSON(t, http.MethodGet, eventInvokeURL(srv, "eic-put-fn"), nil)
	helpers.AssertStatus(t, fetched, http.StatusOK)
	var read eventInvokeConfigResp
	decodeJSON(t, fetched, &read)
	assertRetryPolicy(t, "GetFunctionEventInvokeConfig", read, 1, 120)
}

func TestPutFunctionEventInvokeConfig_omittedMembersAreRemoved(t *testing.T) {
	// Given: a function with a full configuration.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-overwrite-fn")
	first := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-overwrite-fn"), map[string]any{
		"MaximumRetryAttempts":     0,
		"MaximumEventAgeInSeconds": 300,
		"DestinationConfig": map[string]any{
			"OnFailure": map[string]any{"Destination": "arn:aws:sqs:us-east-1:000000000000:eic-failure"},
		},
	})
	helpers.AssertStatus(t, first, http.StatusOK)
	first.Body.Close()

	// When: a second Put names only one member. AWS: "If you exclude any
	// settings, they are removed."
	resp := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-overwrite-fn"), map[string]any{
		"MaximumRetryAttempts": 2,
	})

	// Then: the others are gone rather than retained.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got eventInvokeConfigResp
	decodeJSON(t, resp, &got)
	if got.MaximumRetryAttempts == nil || *got.MaximumRetryAttempts != 2 {
		t.Errorf("MaximumRetryAttempts = %v, want 2", got.MaximumRetryAttempts)
	}
	if got.MaximumEventAgeInSeconds != nil {
		t.Errorf("MaximumEventAgeInSeconds = %d after a Put that omitted it, want it removed", *got.MaximumEventAgeInSeconds)
	}
	if destinationOf(got, false) != "" {
		t.Errorf("OnFailure = %q after a Put that omitted it, want it removed", destinationOf(got, false))
	}
}

func TestUpdateFunctionEventInvokeConfig_leavesOmittedMembersAlone(t *testing.T) {
	// Given: a function with a full configuration. This is the difference
	// between Update and Put, and the reason both exist.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-update-fn")
	first := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-update-fn"), map[string]any{
		"MaximumRetryAttempts":     1,
		"MaximumEventAgeInSeconds": 300,
		"DestinationConfig": map[string]any{
			"OnFailure": map[string]any{"Destination": "arn:aws:sqs:us-east-1:000000000000:eic-failure"},
		},
	})
	helpers.AssertStatus(t, first, http.StatusOK)
	first.Body.Close()

	// When: an Update names only one member.
	resp := doJSON(t, http.MethodPost, eventInvokeURL(srv, "eic-update-fn"), map[string]any{
		"MaximumRetryAttempts": 2,
	})

	// Then: the others survive.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got eventInvokeConfigResp
	decodeJSON(t, resp, &got)
	assertRetryPolicy(t, "UpdateFunctionEventInvokeConfig", got, 2, 300)
	if want := "arn:aws:sqs:us-east-1:000000000000:eic-failure"; destinationOf(got, false) != want {
		t.Errorf("OnFailure = %q after a partial update, want it kept as %q", destinationOf(got, false), want)
	}
}

func TestGetFunctionEventInvokeConfig_withoutOneIsNotFound(t *testing.T) {
	// Given: a function that has never been configured. Unlike
	// GetFunctionConcurrency, whose response member is optional, this
	// configuration either exists or does not — AWS answers
	// ResourceNotFoundException when it does not.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-absent-fn")

	// When: its configuration is read.
	resp := doJSON(t, http.MethodGet, eventInvokeURL(srv, "eic-absent-fn"), nil)

	// Then: 404.
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestPutFunctionEventInvokeConfig_onAMissingFunctionIsNotFound(t *testing.T) {
	// Given: a server with no such function.
	srv := helpers.NewTestServer(t)

	// When: a configuration is put on it.
	resp := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-no-such-fn"), map[string]any{
		"MaximumRetryAttempts": 1,
	})

	// Then: 404 — the configuration cannot outlive its function.
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertJSONError(t, resp, "ResourceNotFoundException")
}

func TestDeleteFunctionEventInvokeConfig_removesItAndAnswers204(t *testing.T) {
	// Given: a function with a configuration.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-delete-fn")
	put := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-delete-fn"), map[string]any{
		"MaximumRetryAttempts": 1,
	})
	helpers.AssertStatus(t, put, http.StatusOK)
	put.Body.Close()

	// When: it is deleted.
	resp := doJSON(t, http.MethodDelete, eventInvokeURL(srv, "eic-delete-fn"), nil)

	// Then: 204 with no body, and the configuration is gone.
	helpers.AssertStatus(t, resp, http.StatusNoContent)
	resp.Body.Close()
	get := doJSON(t, http.MethodGet, eventInvokeURL(srv, "eic-delete-fn"), nil)
	helpers.AssertStatus(t, get, http.StatusNotFound)
	get.Body.Close()
}

func TestListFunctionEventInvokeConfigs_returnsTheFunctionsConfigurations(t *testing.T) {
	// Given: a function configured unqualified and again on an alias, which is
	// what makes this a list rather than a single read.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-list-fn")
	publish := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/eic-list-fn/versions"), map[string]any{})
	helpers.AssertStatus(t, publish, http.StatusCreated)
	publish.Body.Close()
	alias := doJSON(t, http.MethodPost, lambdaURL(srv, "/functions/eic-list-fn/aliases"), map[string]any{
		"Name": "live", "FunctionVersion": "1",
	})
	helpers.AssertStatus(t, alias, http.StatusCreated)
	alias.Body.Close()

	for _, qualifier := range []string{"", "live"} {
		url := eventInvokeURL(srv, "eic-list-fn")
		if qualifier != "" {
			url += "?Qualifier=" + qualifier
		}
		put := doJSON(t, http.MethodPut, url, map[string]any{"MaximumRetryAttempts": 1})
		helpers.AssertStatus(t, put, http.StatusOK)
		put.Body.Close()
	}

	// When: the configurations are listed.
	resp := doJSON(t, http.MethodGet, eventInvokeListURL(srv, "eic-list-fn"), nil)

	// Then: both come back, under AWS's member name.
	helpers.AssertStatus(t, resp, http.StatusOK)
	var got struct {
		FunctionEventInvokeConfigs []eventInvokeConfigResp `json:"FunctionEventInvokeConfigs"`
		NextMarker                 *string                 `json:"NextMarker"`
	}
	decodeJSON(t, resp, &got)
	if len(got.FunctionEventInvokeConfigs) != 2 {
		t.Fatalf("FunctionEventInvokeConfigs has %d entries, want 2: %+v", len(got.FunctionEventInvokeConfigs), got.FunctionEventInvokeConfigs)
	}
	// A qualified configuration is reported against the qualified ARN, which is
	// the only thing distinguishing the two entries.
	var qualified int
	for _, cfg := range got.FunctionEventInvokeConfigs {
		if len(cfg.FunctionArn) > len(":live") && cfg.FunctionArn[len(cfg.FunctionArn)-len(":live"):] == ":live" {
			qualified++
		}
	}
	if qualified != 1 {
		t.Errorf("got %d qualified ARNs among %+v, want exactly 1", qualified, got.FunctionEventInvokeConfigs)
	}
}

func TestPutFunctionEventInvokeConfig_rejectsValuesOutsideAWSsRanges(t *testing.T) {
	// Given: a function.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-range-fn")

	// When/Then: each modeled range is enforced. Being laxer than AWS here is
	// the dangerous direction — a retry policy that deploys locally and is
	// rejected by the account is exactly the surprise to avoid.
	for _, tc := range []struct {
		name string
		body map[string]any
	}{
		{"retries above the maximum", map[string]any{"MaximumRetryAttempts": 3}},
		{"retries below the minimum", map[string]any{"MaximumRetryAttempts": -1}},
		{"age below the minimum", map[string]any{"MaximumEventAgeInSeconds": 59}},
		{"age above the maximum", map[string]any{"MaximumEventAgeInSeconds": 21601}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resp := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-range-fn"), tc.body)
			helpers.AssertStatus(t, resp, http.StatusBadRequest)
			helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
		})
	}
}

func TestPutFunctionEventInvokeConfig_acceptsTheRangeBoundaries(t *testing.T) {
	// Given: a function. The boundaries are inclusive on AWS, and an
	// off-by-one here would reject a configuration the account accepts.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-bounds-fn")

	for _, tc := range []struct{ retries, age int }{{0, 60}, {2, 21600}} {
		t.Run(fmt.Sprintf("retries=%d age=%d", tc.retries, tc.age), func(t *testing.T) {
			resp := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-bounds-fn"), map[string]any{
				"MaximumRetryAttempts":     tc.retries,
				"MaximumEventAgeInSeconds": tc.age,
			})
			helpers.AssertStatus(t, resp, http.StatusOK)
			var got eventInvokeConfigResp
			decodeJSON(t, resp, &got)
			assertRetryPolicy(t, "PutFunctionEventInvokeConfig", got, tc.retries, tc.age)
		})
	}
}

func TestPutFunctionEventInvokeConfig_refusesAnS3Destination(t *testing.T) {
	// Given: a function. AWS allows an S3 bucket as an on-failure destination;
	// Overcast does not write the invocation record to S3, so it refuses rather
	// than accepting the configuration and silently dropping every record.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-s3-fn")

	// When: an S3 bucket is named as the on-failure destination.
	resp := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-s3-fn"), map[string]any{
		"DestinationConfig": map[string]any{
			"OnFailure": map[string]any{"Destination": "arn:aws:s3:::eic-records"},
		},
	})

	// Then: an honest 501, and nothing is stored.
	assertLambdaUnsupported(t, resp)
	get := doJSON(t, http.MethodGet, eventInvokeURL(srv, "eic-s3-fn"), nil)
	helpers.AssertStatus(t, get, http.StatusNotFound)
	get.Body.Close()
}

func TestPutFunctionEventInvokeConfig_refusesAnOnSuccessS3Destination(t *testing.T) {
	// Given: a function. AWS refuses this one outright — S3 is on-failure only
	// — so refusing it is fidelity rather than an emulator gap.
	srv := helpers.NewTestServer(t)
	createFunction(t, srv, "eic-s3-success-fn")

	// When: an S3 bucket is named as the on-success destination.
	resp := doJSON(t, http.MethodPut, eventInvokeURL(srv, "eic-s3-success-fn"), map[string]any{
		"DestinationConfig": map[string]any{
			"OnSuccess": map[string]any{"Destination": "arn:aws:s3:::eic-records"},
		},
	})

	// Then: rejected as an invalid parameter, not refused as unimplemented.
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterValueException")
}

// ---- helpers ---------------------------------------------------------------

func destinationOf(cfg eventInvokeConfigResp, onSuccess bool) string {
	if cfg.DestinationConfig == nil {
		return ""
	}
	if onSuccess {
		if cfg.DestinationConfig.OnSuccess == nil {
			return ""
		}
		return cfg.DestinationConfig.OnSuccess.Destination
	}
	if cfg.DestinationConfig.OnFailure == nil {
		return ""
	}
	return cfg.DestinationConfig.OnFailure.Destination
}

func assertDestination(t *testing.T, which, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("DestinationConfig.%s.Destination = %q, want %q", which, got, want)
	}
}

func assertRetryPolicy(t *testing.T, operation string, cfg eventInvokeConfigResp, retries, age int) {
	t.Helper()
	if cfg.MaximumRetryAttempts == nil || *cfg.MaximumRetryAttempts != retries {
		t.Errorf("%s: MaximumRetryAttempts = %v, want %d", operation, cfg.MaximumRetryAttempts, retries)
	}
	if cfg.MaximumEventAgeInSeconds == nil || *cfg.MaximumEventAgeInSeconds != age {
		t.Errorf("%s: MaximumEventAgeInSeconds = %v, want %d", operation, cfg.MaximumEventAgeInSeconds, age)
	}
}
