package rds

// handler_cluster_envelope_test.go — every DB cluster operation answering in its
// own envelope.
//
// StopDBCluster and StartDBCluster both returned an xmlCreateDBClusterResponse,
// so the wire carried <CreateDBClusterResponse><CreateDBClusterResult> for a
// stop and for a start. The state change itself was correct — the cluster really
// did go to "stopping" — so every Go test that asserted on the stored record
// passed, and the emulator's own tests are all there was: nothing outside them
// looked at a cluster. An AWS SDK does look, and refuses the answer outright:
//
//	deserialization failed, failed to decode response body,
//	StopDBClusterResult node not found
//
// which is a hard client-side error, not a wrong field. ModifyDBCluster had the
// same defect and was fixed by giving it xmlModifyDBClusterResponse; Stop and
// Start were left behind because the raw Query handlers in handler_aurora.go
// declared the right element names locally, and reading those made the typed
// path look correct too.
//
// The test covers all six operations rather than the two that were broken: the
// envelope is the one thing no assertion on the stored record can catch, and a
// seventh cluster operation added later should fail here rather than in a
// compat run.

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// makeClusterAvailable moves a freshly seeded cluster to "available" without
// waiting on the scheduled creating -> available transition, so a state
// precondition does not turn into a sleep.
func makeClusterAvailable(t *testing.T, h *Handler, id string) {
	t.Helper()
	if _, aerr := h.mutateCluster(context.Background(), id, func(cluster *DBCluster) *protocol.AWSError {
		cluster.Status = "available"
		return nil
	}); aerr != nil {
		t.Fatalf("could not make cluster %s available: %s: %s", id, aerr.Code, aerr.Message)
	}
}

// TestClusterResponses_useTheirOwnEnvelope marshals each cluster operation's
// response exactly as the Query encoder does and asserts the two element names
// an SDK deserializer looks for.
func TestClusterResponses_useTheirOwnEnvelope(t *testing.T) {
	ctx := context.Background()

	cases := []struct {
		op string
		// call returns the response value the handler would hand to the Query
		// encoder, with the cluster prepared for whatever state the operation
		// requires.
		call func(t *testing.T, h *Handler, id string) any
	}{
		{
			op: "CreateDBCluster",
			call: func(t *testing.T, h *Handler, id string) any {
				resp, aerr := h.createDBClusterTyped(ctx, &createDBClusterReq{
					DBClusterIdentifier: id,
					Engine:              "aurora-mysql",
					MasterUsername:      "admin",
					MasterUserPassword:  "Password1!",
				})
				if aerr != nil {
					t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
				}
				return resp
			},
		},
		{
			op: "DescribeDBClusters",
			call: func(t *testing.T, h *Handler, id string) any {
				seedCluster(t, h, id)
				resp, aerr := h.describeDBClustersTyped(ctx, &describeDBClustersReq{
					DBClusterIdentifier: id,
				})
				if aerr != nil {
					t.Fatalf("DescribeDBClusters: %s: %s", aerr.Code, aerr.Message)
				}
				return resp
			},
		},
		{
			op: "ModifyDBCluster",
			call: func(t *testing.T, h *Handler, id string) any {
				seedCluster(t, h, id)
				resp, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
					DBClusterIdentifier:   id,
					BackupRetentionPeriod: intPtr(7),
				})
				if aerr != nil {
					t.Fatalf("ModifyDBCluster: %s: %s", aerr.Code, aerr.Message)
				}
				return resp
			},
		},
		{
			op: "StopDBCluster",
			call: func(t *testing.T, h *Handler, id string) any {
				seedCluster(t, h, id)
				makeClusterAvailable(t, h, id)
				resp, aerr := h.stopDBClusterTyped(ctx, &stopDBClusterReq{
					DBClusterIdentifier: id,
				})
				if aerr != nil {
					t.Fatalf("StopDBCluster: %s: %s", aerr.Code, aerr.Message)
				}
				return resp
			},
		},
		{
			op: "StartDBCluster",
			call: func(t *testing.T, h *Handler, id string) any {
				seedCluster(t, h, id)
				if _, aerr := h.mutateCluster(ctx, id, func(cluster *DBCluster) *protocol.AWSError {
					cluster.Status = "stopped"
					return nil
				}); aerr != nil {
					t.Fatalf("could not stop cluster %s: %s: %s", id, aerr.Code, aerr.Message)
				}
				resp, aerr := h.startDBClusterTyped(ctx, &startDBClusterReq{
					DBClusterIdentifier: id,
				})
				if aerr != nil {
					t.Fatalf("StartDBCluster: %s: %s", aerr.Code, aerr.Message)
				}
				return resp
			},
		},
		{
			op: "DeleteDBCluster",
			call: func(t *testing.T, h *Handler, id string) any {
				seedCluster(t, h, id)
				resp, aerr := h.deleteDBClusterTyped(ctx, &deleteDBClusterReq{
					DBClusterIdentifier: id,
				})
				if aerr != nil {
					t.Fatalf("DeleteDBCluster: %s: %s", aerr.Code, aerr.Message)
				}
				return resp
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			h := newClusterTestHandler(t)
			body, err := xml.Marshal(tc.call(t, h, "cl"))
			if err != nil {
				t.Fatalf("marshal %s response: %v", tc.op, err)
			}
			got := string(body)

			// The root element and the result element are the two names an SDK
			// deserializer navigates by. Matching the opening tag exactly rather
			// than a substring, so CreateDBClusterResponse cannot satisfy a
			// check for DBClusterResponse.
			wantRoot := "<" + tc.op + "Response "
			if !strings.Contains(got, wantRoot) {
				t.Errorf("%s: response root is not %sResponse\ngot: %s",
					tc.op, tc.op, truncateXML(got))
			}
			wantResult := "<" + tc.op + "Result>"
			if !strings.Contains(got, wantResult) {
				t.Errorf("%s: result element is not %sResult\ngot: %s",
					tc.op, tc.op, truncateXML(got))
			}
		})
	}
}

func truncateXML(s string) string {
	const max = 240
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
