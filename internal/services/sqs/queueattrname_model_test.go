package sqs

// queueattrname_model_test.go cross-checks queueAttributeNames
// (queueattrname.go) against the pinned AWS model's QueueAttributeName enum,
// so the hard-coded set and models/aws/shapes/sqs.json cannot silently drift
// apart. Same shape, and same justification for reading the snapshot from a
// plain test, as queryerror_model_test.go — see its header.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/overcast-sh/overcast/internal/awsmodel"
)

// TestQueueAttributeNames_matchModel proves the accepted set is exactly the
// modeled enum: a name AWS added that Overcast would reject, and a name
// Overcast accepts that AWS does not model, both fail here rather than
// reaching a client as a wrong InvalidAttributeName decision.
func TestQueueAttributeNames_matchModel(t *testing.T) {
	snapshotPath := filepath.Join("..", "..", "..", "models", "aws", "shapes", "sqs.json")
	contents, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read %s: %v", snapshotPath, err)
	}
	var snapshot awsmodel.Snapshot
	if err := json.Unmarshal(contents, &snapshot); err != nil {
		t.Fatalf("parse %s: %v", snapshotPath, err)
	}

	shape, ok := snapshot.Shapes["QueueAttributeName"]
	if !ok {
		t.Fatal("models/aws/shapes/sqs.json has no QueueAttributeName shape — has the snapshot been regenerated since this set was written?")
	}
	if len(shape.Members) == 0 {
		t.Fatal("QueueAttributeName has no members")
	}

	for name := range shape.Members {
		if _, ok := queueAttributeNames[name]; !ok {
			t.Errorf("model has QueueAttributeName %q, which queueAttributeNames rejects", name)
		}
	}
	for name := range queueAttributeNames {
		if _, ok := shape.Members[name]; !ok {
			t.Errorf("queueAttributeNames accepts %q, which the model does not list as a QueueAttributeName", name)
		}
	}
}
