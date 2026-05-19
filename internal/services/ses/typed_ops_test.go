package ses

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var sesTypedOps = []string{
	"SendEmail", "SendRawEmail", "VerifyEmailIdentity", "VerifyDomainIdentity",
	"ListIdentities", "ListVerifiedEmailAddresses", "GetIdentityVerificationAttributes",
	"DeleteIdentity", "CreateTemplate", "GetTemplate", "UpdateTemplate", "ListTemplates",
	"DeleteTemplate", "SendTemplatedEmail", "GetSendQuota", "GetSendStatistics",
	"SetIdentityFeedbackForwardingEnabled",
}

func TestTypedOps_matchLegacyRegistry(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.handler.typedOp) == 0 {
		t.Fatal("typedOp is empty")
	}
	for _, name := range sesTypedOps {
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
