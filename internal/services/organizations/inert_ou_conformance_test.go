package organizations

// The §3 conformance gate for Organizations' Tier 1 organizational-unit
// surface — the sibling of inert_conformance_test.go's policy fixture, for
// the resource added by #1813.
//
// Which clauses run, and which skip and why (a gate that silently skips half
// the contract is not a gate):
//
//	3.1/create-read          runs
//	3.1/update-merge         runs
//	3.1/delete-then-read     runs
//	3.1/list-stable          runs
//	3.1/list-paginate        runs
//	3.2/roundtrip-fidelity   runs — Name, the only member a caller sends that
//	                         the output shape also carries
//	3.3/not-found            runs — OrganizationalUnitNotFoundException / 404
//	3.3/already-exists       runs — DuplicateOrganizationalUnitException / 409
//	3.3/invalid-parameter    runs — InvalidInputException / 400
//	3.3/invalid-token        runs — InvalidInputException / 400
//	3.5/arn                  runs
//	3.6/verb-default         runs — MoveAccount must stay a 501
//	3.2/no-fabrication       SKIPS — OutputOnlyFields is empty. Every member
//	                         of the OrganizationalUnit shape a caller does
//	                         not send (Id, Arn, Path) is a §3.5 derivation
//	                         from state the caller did supply, not a status
//	                         field with a modeled default, so there is
//	                         nothing this clause could assert about them.
//	                         The three are pinned instead by 3.5/arn and by
//	                         inert_ou_test.go, which checks each against the
//	                         model's own pattern.
//	3.5/timestamps           SKIPS — CreationTimeField is empty because the
//	                         OrganizationalUnit shape models no timestamp
//	                         member, so there is nothing on the wire for the
//	                         clause to look at. The rule itself is not
//	                         skipped: the record carries CreatedAt/UpdatedAt
//	                         from the injected clock, and
//	                         TestOrganizationalUnitTimestampsComeFromTheClock
//	                         holds that.
//	3.5/idempotency          SKIPS — IdempotencyField is empty because
//	                         CreateOrganizationalUnitRequest models no
//	                         ClientToken or CallerReference member.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/inert/conformance"
	"github.com/overcast-sh/overcast/internal/protocol/codec"
	"github.com/overcast-sh/overcast/internal/state"
)

// TestOrganizationalUnitResource_SatisfiesTheInertContract runs the whole §3
// contract against the OU lifecycle.
func TestOrganizationalUnitResource_SatisfiesTheInertContract(t *testing.T) {
	conformance.Run(t, newOrganizationalUnitFixture(t))
}

func newOrganizationalUnitFixture(t *testing.T) conformance.Fixture {
	t.Helper()
	clk := clock.NewMock()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}

	// The service is rebuilt over a fresh store on Reset, so each clause runs
	// against clean state regardless of the order Check runs them in. The
	// root survives the rebuild because it is derived from the account id
	// rather than stored — which is exactly why every clause can name it
	// without first reading it back.
	var svc *Service
	reset := func() {
		st := state.NewMemoryStore()
		t.Cleanup(func() { _ = st.Close() })
		svc = New(cfg, st, zap.NewNop(), clk)
	}
	reset()
	root := rootID(organizationID(cfg.AccountID))

	return conformance.Fixture{
		Service: serviceName,
		Codec:   codec.JSON11,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { svc.Dispatch(w, r) }),
		Resource: conformance.ResourceOps{
			Create: "CreateOrganizationalUnit",
			Read:   "DescribeOrganizationalUnit",
			Update: "UpdateOrganizationalUnit",
			Delete: "DeleteOrganizationalUnit",
			List:   "ListOrganizationalUnitsForParent",
			// MoveAccount is the verb this resource would most tempt someone
			// to fake: an OU exists to hold accounts, and faking the move
			// would pass every shape test while placing nothing anywhere
			// (§3.6).
			Verb: "MoveAccount",

			IDField:  "OrganizationalUnitId",
			ArnField: "Arn",
			// See the file header for why the two timestamp fields and
			// OutputOnlyFields are empty.
			CreationTimeField: "",
			ModifiedTimeField: "",

			RoundtripFields:  []string{"Name"},
			OutputOnlyFields: nil,

			ItemsField:         "OrganizationalUnits",
			TokenRequestField:  "NextToken",
			TokenResponseField: "NextToken",
			LimitField:         "MaxResults",

			IdempotencyField: "",
		},
		Errors: conformance.ErrorCodes{
			NotFound:               "OrganizationalUnitNotFoundException",
			NotFoundStatus:         http.StatusNotFound,
			AlreadyExists:          "DuplicateOrganizationalUnitException",
			AlreadyExistsStatus:    http.StatusConflict,
			InvalidParameter:       "InvalidInputException",
			InvalidParameterStatus: http.StatusBadRequest,
			// Organizations declares no InvalidNextToken-shaped error, so
			// §3.3's selection falls back to invalid-parameter.
			InvalidToken:       "InvalidInputException",
			InvalidTokenStatus: http.StatusBadRequest,
		},
		Input:  organizationalUnitInput(root),
		Reset:  reset,
		Clock:  clk,
		Encode: encodeOrganizationalUnitRequest(root),
		Decode: decodeOrganizationalUnitResponse,
	}
}

func conformanceOUName(seed int) string { return fmt.Sprintf("conformance-ou-%d", seed) }

func organizationalUnitInput(root string) func(conformance.InputKind, int) map[string]any {
	return func(kind conformance.InputKind, seed int) map[string]any {
		name := conformanceOUName(seed)
		switch kind {
		case conformance.InputFull:
			return map[string]any{"Name": name, "ParentId": root}
		case conformance.InputMinimal:
			// Both of Name and ParentId are @required, so the minimal input
			// is the full one minus the optional Tags member.
			return map[string]any{"Name": name, "ParentId": root}
		case conformance.InputInvalid:
			// Name omitted.
			return map[string]any{"ParentId": root}
		case conformance.InputUpdate:
			// The identifier is derived from the parent and the name (see
			// organizationalUnitID), which is what lets the contract suite
			// address a record it created without reading the response back.
			return map[string]any{
				"OrganizationalUnitId": organizationalUnitID(root, root, name),
				"Name":                 fmt.Sprintf("conformance-ou-renamed-%d", seed),
			}
		case conformance.InputIdempotent:
			// Unreachable: the resource models no idempotency token, so the
			// clause is skipped before Input is called.
			return map[string]any{}
		default:
			return map[string]any{}
		}
	}
}

// encodeOrganizationalUnitRequest turns a logical field map into the AWS JSON
// 1.1 request an SDK would send.
func encodeOrganizationalUnitRequest(root string) func(string, map[string]any) *http.Request {
	return func(opName string, fields map[string]any) *http.Request {
		if opName == "ListOrganizationalUnitsForParent" {
			// ParentId is @required on the request and has no default, so
			// every real caller sends one; the contract suite calls List with
			// a bare field map because most services' List takes no required
			// member. Supplying it is the fixture doing what an SDK does, not
			// the service relaxing a check —
			// TestListOrganizationalUnitsForParent_RejectsAnUnknownParent
			// covers the parent-resolution path directly.
			if _, ok := fields["ParentId"]; !ok {
				next := make(map[string]any, len(fields)+1)
				for k, v := range fields {
					next[k] = v
				}
				next["ParentId"] = root
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
}

// decodeOrganizationalUnitResponse turns a wire response back into the flat
// logical field map the contract's clauses operate on: the nesting under
// OrganizationalUnit is flattened, and Id is renamed to the
// OrganizationalUnitId the request shapes (and so the contract's IDField)
// use.
func decodeOrganizationalUnitResponse(resp *http.Response) (map[string]any, *conformance.WireError) {
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
	if ou, ok := raw["OrganizationalUnit"].(map[string]any); ok {
		return flattenOrganizationalUnit(ou), nil
	}
	if units, ok := raw["OrganizationalUnits"].([]any); ok {
		items := make([]any, 0, len(units))
		for _, entry := range units {
			unit, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			items = append(items, flattenOrganizationalUnit(unit))
		}
		out := map[string]any{"OrganizationalUnits": items}
		if token, ok := raw["NextToken"].(string); ok && token != "" {
			out["NextToken"] = token
		}
		return out, nil
	}
	return raw, nil
}

func flattenOrganizationalUnit(unit map[string]any) map[string]any {
	out := map[string]any{}
	for _, field := range []string{"Arn", "Name", "Path"} {
		if v, ok := unit[field]; ok {
			out[field] = v
		}
	}
	if id, ok := unit["Id"]; ok {
		out["OrganizationalUnitId"] = id
	}
	return out
}
