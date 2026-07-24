package serviceutil

import (
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/codec"
)

// AllowProtocolDrift decides what a service's Dispatch/DispatchQuery method
// should do when the wire protocol identified for a request (claimed) is
// not one of the protocols the service declares support for
// (declared, from its SupportedProtocols()).
//
// This implements the "claimed-but-undeclared" reactive posture from
// docs/plans/level2-codegen.md Track 1.2: since April 2026, AWS SDKs may
// switch a service's wire protocol without notice (the CloudWatch metrics
// protocol switch is the precedent). Hard-rejecting an undeclared protocol
// turns a still-decodable request into an unnecessary 415 the moment AWS
// ships that kind of change. The default, lenient posture instead logs a
// loud "protocol drift" warning and tells the caller to attempt the
// decode/dispatch anyway — a silent SDK protocol switch becomes a working
// request plus a signal, not a mystery outage.
//
// Strict mode (cfg.ProtocolStrict, env OVERCAST_PROTOCOL_STRICT) restores
// the old reject-on-mismatch behaviour for environments that want hard
// protocol-fidelity gating (e.g. a CI job asserting no service has drifted).
//
// Returns true when the caller should proceed with normal dispatch (either
// claimed is already declared — the common case, no log — or lenient mode
// allowed the drift through). Returns false only in strict mode with an
// undeclared claim; the caller is expected to write its existing
// UnsupportedProtocol error in that case.
//
// log may be nil (drift is simply not logged); cfg may be nil (treated as
// lenient — the safer default).
func AllowProtocolDrift(cfg *config.Config, log *ServiceLogger, operation string, claimed codec.Codec, declared []codec.Codec) bool {
	if codec.Supports(declared, claimed) {
		return true
	}
	if cfg != nil && cfg.ProtocolStrict {
		return false
	}
	if log != nil {
		log.Warn("protocol drift: claimed protocol not declared by service",
			zap.String("operation", operation),
			zap.String("claimedProtocol", claimed.Name()),
		)
	}
	return true
}
