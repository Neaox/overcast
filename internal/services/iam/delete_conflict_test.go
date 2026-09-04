package iam

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/state"
)

// newTestHandler returns an IAM handler over empty in-memory state.
func newTestHandler(t *testing.T) *Handler {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	return New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New()).handler
}

// noCodecDelete invokes a delete operation through the no-codec dispatch
// fallback (Handler.dispatch, see service.go), the path DispatchQuery falls
// back to whenever the protocol middleware has not identified a codec for
// the request (e.g. the AWS Query GET form). Every entry in Handler.ops
// reaches its operation through typedHandler (see initOps), so this
// exercises the same typed implementation as the codec-identified path —
// there is no separate legacy implementation left to drift from it — but it
// still guards the wire-level XML this fallback produces.
func noCodecDelete(t *testing.T, fn http.HandlerFunc, params url.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(params.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	fn(rec, req)
	return rec
}

// TestDeleteConflict_typedAndNoCodecAgree runs each delete through both the
// typed Go path and the no-codec dispatch fallback against identical state,
// and asserts they return the same status and the same AWS message. Both
// paths invoke the same typed implementation (#671 removed the separate
// legacy handlers), so this pins the wire-level XML the fallback produces
// rather than guarding against drift between two implementations.
func TestDeleteConflict_typedAndNoCodecAgree(t *testing.T) {
	policyArn := "arn:aws:iam::000000000000:policy/managed"

	cases := []struct {
		name    string
		seed    func(t *testing.T, h *Handler)
		typed   func(ctx context.Context, h *Handler) *protocol.AWSError
		action  string
		params  url.Values
		message string
	}{
		{
			name: "DeleteUser with an access key",
			seed: func(t *testing.T, h *Handler) {
				seedUser(t, h, &User{UserName: "alice", AccessKeys: []AccessKey{{AccessKeyId: "AKIA1"}}})
			},
			typed: func(ctx context.Context, h *Handler) *protocol.AWSError {
				_, aerr := h.deleteUserTyped(ctx, &deleteUserReq{UserName: "alice"})
				return aerr
			},
			action:  "DeleteUser",
			params:  url.Values{"UserName": {"alice"}},
			message: "Cannot delete entity, must delete access keys first.",
		},
		{
			name: "DeleteRole with an attached policy",
			seed: func(t *testing.T, h *Handler) {
				seedRole(t, h, &Role{RoleName: "app", AttachedPolicies: []AttachedPolicy{{PolicyArn: policyArn}}})
			},
			typed: func(ctx context.Context, h *Handler) *protocol.AWSError {
				_, aerr := h.deleteRoleTyped(ctx, &deleteRoleReq{RoleName: "app"})
				return aerr
			},
			action:  "DeleteRole",
			params:  url.Values{"RoleName": {"app"}},
			message: "Cannot delete entity, must detach all policies first.",
		},
		{
			name: "DeleteGroup with a member",
			seed: func(t *testing.T, h *Handler) {
				seedGroup(t, h, &Group{GroupName: "devs", Members: []string{"alice"}})
			},
			typed: func(ctx context.Context, h *Handler) *protocol.AWSError {
				_, aerr := h.deleteGroupTyped(ctx, &deleteGroupReq{GroupName: "devs"})
				return aerr
			},
			action:  "DeleteGroup",
			params:  url.Values{"GroupName": {"devs"}},
			message: "Cannot delete entity, must remove users from group first.",
		},
		{
			name: "DeletePolicy attached to a user",
			seed: func(t *testing.T, h *Handler) {
				seedPolicy(t, h, &Policy{PolicyName: "managed", Arn: policyArn})
				seedUser(t, h, &User{UserName: "alice", AttachedPolicies: []AttachedPolicy{{PolicyArn: policyArn}}})
			},
			typed: func(ctx context.Context, h *Handler) *protocol.AWSError {
				_, aerr := h.deletePolicyTyped(ctx, &deletePolicyReq{PolicyArn: policyArn})
				return aerr
			},
			action:  "DeletePolicy",
			params:  url.Values{"PolicyArn": {policyArn}},
			message: "Cannot delete a policy attached to entities.",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Typed path
			typedHandlerUnderTest := newTestHandler(t)
			tc.seed(t, typedHandlerUnderTest)
			aerr := tc.typed(context.Background(), typedHandlerUnderTest)
			if aerr == nil {
				t.Fatal("typed path: expected DeleteConflict, got success")
			}
			if aerr.Code != "DeleteConflict" || aerr.HTTPStatus != http.StatusConflict {
				t.Errorf("typed path error = %s/%d, want DeleteConflict/409", aerr.Code, aerr.HTTPStatus)
			}
			if aerr.Message != tc.message {
				t.Errorf("typed path message = %q, want %q", aerr.Message, tc.message)
			}

			// No-codec dispatch fallback, over identical state
			fallbackHandler := newTestHandler(t)
			tc.seed(t, fallbackHandler)
			rec := noCodecDelete(t, fallbackHandler.typedHandler(tc.action), tc.params)
			if rec.Code != http.StatusConflict {
				t.Fatalf("no-codec path status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "<Code>DeleteConflict</Code>") {
				t.Errorf("no-codec path body missing DeleteConflict code: %s", body)
			}
			if !strings.Contains(body, "<Message>"+tc.message+"</Message>") {
				t.Errorf("no-codec path body missing message %q: %s", tc.message, body)
			}
		})
	}
}

func seedUser(t *testing.T, h *Handler, u *User) {
	t.Helper()
	if aerr := h.store.putUser(context.Background(), u); aerr != nil {
		t.Fatalf("seed user: %v", aerr)
	}
}

func seedRole(t *testing.T, h *Handler, r *Role) {
	t.Helper()
	if aerr := h.store.putRole(context.Background(), r); aerr != nil {
		t.Fatalf("seed role: %v", aerr)
	}
}

func seedGroup(t *testing.T, h *Handler, g *Group) {
	t.Helper()
	if aerr := h.store.putGroup(context.Background(), g); aerr != nil {
		t.Fatalf("seed group: %v", aerr)
	}
}

func seedPolicy(t *testing.T, h *Handler, p *Policy) {
	t.Helper()
	if aerr := h.store.putPolicy(context.Background(), p); aerr != nil {
		t.Fatalf("seed policy: %v", aerr)
	}
}
