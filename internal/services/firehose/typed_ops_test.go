package firehose

import (
	"testing"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol/op"
	"github.com/Neaox/overcast/internal/state"
	"go.uber.org/zap"
)

var allFirehoseOps = []string{
	"CreateDeliveryStream", "DescribeDeliveryStream", "ListDeliveryStreams",
	"DeleteDeliveryStream", "PutRecord", "PutRecordBatch",
}

func TestTypedOps_matchAllOperations(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())

	if len(s.typedOp) != len(allFirehoseOps) {
		t.Fatalf("typed op count = %d, want %d", len(s.typedOp), len(allFirehoseOps))
	}
	for _, name := range allFirehoseOps {
		operation, ok := s.typedOp[name]
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
