package cloudformation

// handler_emulator.go — Emulator-only endpoints behind the
// /_overcast/cloudformation/ prefix. These are NOT part of the AWS API surface.
//
// Being off the AWS surface is the reason this file can exist at all. The
// deploy-diagnostics journal is Overcast's own account of why a deploy failed —
// container exit codes, stopped-task records, the text a container printed
// before it died — and none of it is expressible in CloudFormation's
// vocabulary. Put on an AWS-shaped response it would be a fabrication; served
// here, under a path no SDK will ever call, it is Overcast speaking as itself.
// That is the same line internal/services/ecs/handler_emulator.go and the RDS
// retained-logs endpoint sit on, and it is argued out in
// docs/dev/architecture.md § "Two things called a bus".
//
// The corollary is that nothing here may leak back the other way. The journal
// is read through this endpoint and nowhere else: DescribeStackEvents,
// DescribeStacks and ListStackResources are byte-identical whether or not a
// journal exists, and a test pins that.

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

// GetStackDiagnostics returns the deploy-diagnostics journal for a stack.
// This is an emulator-only endpoint.
//
// A stack with no journal is a 404 carrying a message, not an error condition:
// it is what a healthy stack looks like, and it is the far commoner answer.
// The console hides its Diagnostics tab on that 404 rather than showing an
// empty one, so the message is for a human reading the response by hand.
func (h *Handler) GetStackDiagnostics(w http.ResponseWriter, r *http.Request) {
	stackName := chi.URLParam(r, "stackName")

	// Resolved by name or ARN, as every stack-scoped operation accepts both.
	// The journal itself is keyed by name; going through the stack record is
	// what makes an ARN work and what keeps a stale ARN from a
	// deleted-and-recreated stack resolving to the new stack's diagnosis.
	stack, aerr := h.store.getStackByNameOrARN(r.Context(), stackName)
	if aerr != nil {
		writeCFNEmulatorError(w, http.StatusInternalServerError, "could not read the stack")
		return
	}
	if stack == nil {
		writeCFNEmulatorError(w, http.StatusNotFound, "no such stack: "+stackName)
		return
	}

	diagnostics, err := h.store.getDeployDiagnostics(r.Context(), stack.StackName)
	if err != nil {
		h.log.WithRecorder(r.Context()).Warn("cfn: could not read deploy diagnostics",
			zap.String("stack", stack.StackName), zap.Error(err))
		writeCFNEmulatorError(w, http.StatusInternalServerError, "could not read the deploy diagnostics")
		return
	}
	if diagnostics == nil {
		writeCFNEmulatorError(w, http.StatusNotFound,
			"no deploy diagnostics for "+stack.StackName+": no deploy of this stack has failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(diagnostics)
}

// writeCFNEmulatorError writes a simple JSON error for emulator-only
// endpoints, in the shape every other /_overcast/ endpoint uses.
func writeCFNEmulatorError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}
