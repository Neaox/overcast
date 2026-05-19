package sts

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var stsOps = []string{
	"GetCallerIdentity", "GetSessionToken", "GetFederationToken",
	"AssumeRole", "AssumeRoleWithWebIdentity",
}

func TestTypedOps_matchLegacyRegistry(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.handler.typedOp) != len(s.handler.ops) {
		t.Fatalf("typed op count = %d, want legacy count %d", len(s.handler.typedOp), len(s.handler.ops))
	}
	for _, name := range stsOps {
		operation, ok := s.handler.typedOp[name]
		if !ok {
			t.Fatalf("missing typed operation %s", name)
		}
		if operation.Name() != name {
			t.Fatalf("typed operation %s reports name %s", name, operation.Name())
		}
		if _, raw := operation.(*op.Raw); raw {
			t.Fatalf("typed operation %s still uses raw adapter", name)
		}
	}
}
