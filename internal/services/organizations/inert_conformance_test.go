package organizations

// The §3 conformance gate for Organizations' Tier 1 policy surface.
//
// internal/inert/conformance is docs/plans/inert-tier-rollout.md §3 in
// executable form (Phase I0). This file builds a real Fixture over the real
// service — the actual Dispatch method, the actual codec, the actual store —
// and asserts Check returns zero violations. It is the acceptance gate for
// Phase I2: the runtime and the hand-wiring are only proven if the contract
// they were written against says so.
//
// Which clauses run, and which skip and why (a gate that silently skips half
// the contract is not a gate):
//
//	3.1/create-read          runs
//	3.1/update-merge         runs
//	3.1/delete-then-read     runs
//	3.1/list-stable          runs
//	3.1/list-paginate        runs
//	3.2/roundtrip-fidelity   runs — Content, Description, Name, Type
//	3.2/no-fabrication       runs — AwsManaged, against its modeled @default
//	3.3/not-found            runs — PolicyNotFoundException / 404
//	3.3/already-exists       runs — DuplicatePolicyException / 409
//	3.3/invalid-parameter    runs — InvalidInputException / 400
//	3.3/invalid-token        runs — InvalidInputException / 400
//	3.5/arn                  runs
//	3.6/verb-default         runs — AttachPolicy must stay a 501
//	3.5/timestamps           SKIPS — CreationTimeField is empty because
//	                         Organizations models no timestamp member on
//	                         Policy or PolicySummary. There is nothing on the
//	                         wire for the clause to look at, which is exactly
//	                         the escape hatch the Fixture's empty fields are
//	                         for. The rule itself is not skipped: the record
//	                         does carry CreatedAt/UpdatedAt from the injected
//	                         clock, and TestPolicyTimestampsComeFromTheClock
//	                         (inert_policy_test.go) plus
//	                         TestStore_NowComesFromTheInjectedClock
//	                         (internal/inert) hold it.
//	3.5/idempotency          SKIPS — IdempotencyField is empty because
//	                         CreatePolicyRequest models no ClientToken or
//	                         CallerReference member. §3.5 makes idempotency
//	                         conditional on that member's presence, so there
//	                         is no behaviour to assert.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/inert/conformance"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/state"
)

// TestPolicyResource_SatisfiesTheInertContract is Phase I2's acceptance gate.
func TestPolicyResource_SatisfiesTheInertContract(t *testing.T) {
	conformance.Run(t, newPolicyFixture(t))
}

func newPolicyFixture(t *testing.T) conformance.Fixture {
	t.Helper()
	clk := clock.NewMock()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}

	// The service is rebuilt over a fresh store on Reset, so each clause runs
	// against clean state regardless of the order Check runs them in. The
	// handler closes over the holder rather than over one service, so the
	// rebuild is visible to it.
	var svc *Service
	reset := func() {
		st := state.NewMemoryStore()
		t.Cleanup(func() { _ = st.Close() })
		svc = New(cfg, st, zap.NewNop(), clk)
	}
	reset()

	return conformance.Fixture{
		Service: serviceName,
		Codec:   codec.JSON11,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { svc.Dispatch(w, r) }),
		Resource: conformance.ResourceOps{
			Create: "CreatePolicy",
			Read:   "DescribePolicy",
			Update: "UpdatePolicy",
			Delete: "DeletePolicy",
			List:   "ListPolicies",
			// AttachPolicy is the verb this resource would most tempt someone
			// to fake: it is the whole point of a policy, and faking it would
			// pass every shape test while doing nothing (§3.6).
			Verb: "AttachPolicy",

			IDField:  "PolicyId",
			ArnField: "Arn",
			// See the file header for why the two timestamp fields are empty.
			CreationTimeField: "",
			ModifiedTimeField: "",

			RoundtripFields:  []string{"Content", "Description", "Name", "Type"},
			OutputOnlyFields: []string{"AwsManaged"},
			Defaults:         map[string]any{"AwsManaged": false},

			ItemsField:         "Policies",
			TokenRequestField:  "NextToken",
			TokenResponseField: "NextToken",
			LimitField:         "MaxResults",

			IdempotencyField: "",
		},
		Errors: conformance.ErrorCodes{
			NotFound:               "PolicyNotFoundException",
			NotFoundStatus:         http.StatusNotFound,
			AlreadyExists:          "DuplicatePolicyException",
			AlreadyExistsStatus:    http.StatusConflict,
			InvalidParameter:       "InvalidInputException",
			InvalidParameterStatus: http.StatusBadRequest,
			// Organizations declares no InvalidNextToken-shaped error, so
			// §3.3's selection falls back to invalid-parameter.
			InvalidToken:       "InvalidInputException",
			InvalidTokenStatus: http.StatusBadRequest,
		},
		Input:  policyInput,
		Reset:  reset,
		Clock:  clk,
		Encode: encodePolicyRequest,
		Decode: decodePolicyResponse,
	}
}

func conformancePolicyName(seed int) string { return fmt.Sprintf("conformance-policy-%d", seed) }

func policyInput(kind conformance.InputKind, seed int) map[string]any {
	name := conformancePolicyName(seed)
	content := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"s%d","Effect":"Deny","Action":"s3:DeleteBucket","Resource":"*"}]}`, seed)
	switch kind {
	case conformance.InputFull:
		return map[string]any{
			"Name":        name,
			"Description": fmt.Sprintf("description-%d", seed),
			"Content":     content,
			"Type":        "SERVICE_CONTROL_POLICY",
		}
	case conformance.InputMinimal:
		// CreatePolicyRequest marks all four of Content, Description, Name
		// and Type @required, so the minimal input is the full one minus the
		// optional Tags member. AwsManaged is the only output field no caller
		// can ever set, which is why it is the only OutputOnlyFields entry.
		return map[string]any{
			"Name":        name,
			"Description": fmt.Sprintf("description-%d", seed),
			"Content":     content,
			"Type":        "SERVICE_CONTROL_POLICY",
		}
	case conformance.InputInvalid:
		// Content omitted.
		return map[string]any{
			"Name":        name,
			"Description": fmt.Sprintf("description-%d", seed),
			"Type":        "SERVICE_CONTROL_POLICY",
		}
	case conformance.InputUpdate:
		// The identifier is derived from the name (see policyID), which is
		// what lets a caller — here, the contract suite — address a record it
		// created without having read the response back.
		return map[string]any{
			"PolicyId":    policyID(name),
			"Description": "updated-description",
		}
	case conformance.InputIdempotent:
		// Unreachable: the resource models no idempotency token, so the
		// clause is skipped before Input is called.
		return map[string]any{}
	default:
		return map[string]any{}
	}
}

// encodePolicyRequest turns a logical field map into the AWS JSON 1.1 request
// an SDK would send.
func encodePolicyRequest(opName string, fields map[string]any) *http.Request {
	if opName == "ListPolicies" {
		// Filter is @required on ListPoliciesRequest and has no default, so
		// every real caller sends one; the contract suite calls List with a
		// bare field map because most services' List takes no required
		// member. Supplying it here is the fixture doing what an SDK does,
		// not the service relaxing a check — TestListPolicies_RequiresFilter
		// covers the missing-Filter path directly.
		if _, ok := fields["Filter"]; !ok {
			next := make(map[string]any, len(fields)+1)
			for k, v := range fields {
				next[k] = v
			}
			next["Filter"] = "SERVICE_CONTROL_POLICY"
			fields = next
		}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		panic("organizations conformance: marshalling " + opName + ": " + err.Error())
	}
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", targetPrefix+opName)
	return req
}

// decodePolicyResponse turns a wire response back into the flat logical field
// map the contract's clauses operate on.
//
// Organizations nests a policy under Policy.PolicySummary, and every clause
// addresses fields by a single name, so the nesting is flattened here. This
// is the Fixture's job by design: Encode and Decode are the only
// protocol-specific — and, as here, shape-specific — parts of a Fixture,
// which is what lets one set of clause implementations exercise every service
// and every protocol family.
func decodePolicyResponse(resp *http.Response) (map[string]any, *conformance.WireError) {
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		var env struct {
			Type string `json:"__type"`
		}
		_ = json.Unmarshal(body, &env)
		return nil, &conformance.WireError{Code: env.Type, HTTPStatus: resp.StatusCode}
	}

	var raw map[string]any
	if len(body) > 0 {
		_ = json.Unmarshal(body, &raw)
	}
	if policy, ok := raw["Policy"].(map[string]any); ok {
		return flattenPolicy(policy), nil
	}
	if summaries, ok := raw["Policies"].([]any); ok {
		items := make([]any, 0, len(summaries))
		for _, entry := range summaries {
			summary, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			items = append(items, flattenSummary(summary))
		}
		out := map[string]any{"Policies": items}
		if token, ok := raw["NextToken"].(string); ok && token != "" {
			out["NextToken"] = token
		}
		return out, nil
	}
	return raw, nil
}

func flattenPolicy(policy map[string]any) map[string]any {
	out := map[string]any{}
	if content, ok := policy["Content"]; ok {
		out["Content"] = content
	}
	if summary, ok := policy["PolicySummary"].(map[string]any); ok {
		for k, v := range flattenSummary(summary) {
			out[k] = v
		}
	}
	return out
}

// flattenSummary renames PolicySummary.Id to the PolicyId the request shapes
// (and so the contract's IDField) use.
func flattenSummary(summary map[string]any) map[string]any {
	out := map[string]any{}
	for _, field := range []string{"Arn", "AwsManaged", "Description", "Name", "Type"} {
		if v, ok := summary[field]; ok {
			out[field] = v
		}
	}
	if id, ok := summary["Id"]; ok {
		out["PolicyId"] = id
	}
	return out
}
