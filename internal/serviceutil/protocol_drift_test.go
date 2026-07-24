package serviceutil_test

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func TestAllowProtocolDrift_DeclaredProtocol_NoDriftNoLog(t *testing.T) {
	// Given: the claimed protocol is one the service already declares.
	core, logs := observer.New(zapcore.DebugLevel)
	log := serviceutil.NewServiceLogger(zap.New(core), "iam")

	// When: checking drift for a fully-declared protocol.
	allowed := serviceutil.AllowProtocolDrift(&config.Config{}, log, "CreateRole", codec.QueryXML, []codec.Codec{codec.QueryXML})

	// Then: allowed, and nothing is logged — there is no drift to report.
	if !allowed {
		t.Fatal("expected AllowProtocolDrift to return true for a declared protocol")
	}
	if logs.Len() != 0 {
		t.Fatalf("expected no log entries for a declared protocol, got %d: %+v", logs.Len(), logs.All())
	}
}

func TestAllowProtocolDrift_Lenient_AllowsAndWarns(t *testing.T) {
	// Given: the claimed protocol (JSON10) is not declared by a Query-only
	// service, and strict mode is off (the default).
	core, logs := observer.New(zapcore.DebugLevel)
	log := serviceutil.NewServiceLogger(zap.New(core), "iam")
	cfg := &config.Config{ProtocolStrict: false}

	// When: checking drift.
	allowed := serviceutil.AllowProtocolDrift(cfg, log, "CreateRole", codec.JSON10, []codec.Codec{codec.QueryXML})

	// Then: the request proceeds (attempt-anyway posture)...
	if !allowed {
		t.Fatal("expected AllowProtocolDrift to return true in lenient mode")
	}
	// ...and a structured warning is logged, naming the operation and the
	// protocol that showed up unannounced.
	entries := logs.FilterMessage("protocol drift: claimed protocol not declared by service").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one drift warning, got %d: %+v", len(entries), logs.All())
	}
	entry := entries[0]
	if entry.Level != zapcore.WarnLevel {
		t.Errorf("expected WARN level, got %v", entry.Level)
	}
	fields := entry.ContextMap()
	if fields["operation"] != "CreateRole" {
		t.Errorf("operation field = %v, want CreateRole", fields["operation"])
	}
	if fields["claimedProtocol"] != codec.JSON10.Name() {
		t.Errorf("claimedProtocol field = %v, want %v", fields["claimedProtocol"], codec.JSON10.Name())
	}
	if fields["service"] != "iam" {
		t.Errorf("service field = %v, want iam (from ServiceLogger scoping)", fields["service"])
	}
}

func TestAllowProtocolDrift_Strict_Rejects(t *testing.T) {
	// Given: strict mode is enabled (OVERCAST_PROTOCOL_STRICT=1 equivalent).
	core, logs := observer.New(zapcore.DebugLevel)
	log := serviceutil.NewServiceLogger(zap.New(core), "iam")
	cfg := &config.Config{ProtocolStrict: true}

	// When: checking drift for an undeclared protocol.
	allowed := serviceutil.AllowProtocolDrift(cfg, log, "CreateRole", codec.JSON10, []codec.Codec{codec.QueryXML})

	// Then: rejected, restoring the old hard-fail behaviour, and nothing is
	// logged (the caller writes its own UnsupportedProtocol error).
	if allowed {
		t.Fatal("expected AllowProtocolDrift to return false in strict mode for an undeclared protocol")
	}
	if logs.Len() != 0 {
		t.Fatalf("expected no drift warning in strict mode (caller handles the rejection), got %d: %+v", logs.Len(), logs.All())
	}
}

func TestAllowProtocolDrift_NilCfg_TreatedAsLenient(t *testing.T) {
	// A nil *config.Config (e.g. a hand-built test service) must not panic
	// and must default to the safer, lenient posture.
	allowed := serviceutil.AllowProtocolDrift(nil, nil, "CreateRole", codec.JSON10, []codec.Codec{codec.QueryXML})
	if !allowed {
		t.Fatal("expected nil cfg to default to lenient (allowed)")
	}
}

func TestAllowProtocolDrift_JSON10_JSON11_Equivalence_NoDrift(t *testing.T) {
	// codec.Supports treats JSON 1.0/1.1 as interchangeable; that
	// equivalence must hold here too so a JSON 1.0-declared service isn't
	// flagged for ordinary 1.1 traffic.
	core, logs := observer.New(zapcore.DebugLevel)
	log := serviceutil.NewServiceLogger(zap.New(core), "sqs")

	allowed := serviceutil.AllowProtocolDrift(&config.Config{}, log, "SendMessage", codec.JSON11, []codec.Codec{codec.JSON10})
	if !allowed {
		t.Fatal("expected JSON10/JSON11 equivalence to avoid drift")
	}
	if logs.Len() != 0 {
		t.Fatalf("expected no drift warning for JSON10/JSON11 equivalence, got %d: %+v", logs.Len(), logs.All())
	}
}
