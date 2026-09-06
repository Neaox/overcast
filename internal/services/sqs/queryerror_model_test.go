package sqs

// queryerror_model_test.go cross-checks sqsQueryErrorCodes (queryerror.go)
// against the pinned AWS model, so the hard-coded table and
// models/aws/shapes/sqs.json cannot silently drift apart. It is a plain
// test, not build-tag gated: the table it checks isn't behind `dev` (unlike
// the capability declarations internal/services/*/  's own `_dev_test.go`
// files check — see AGENTS.md § Generated files), and
// internal/awsapi.TestShapeSnapshot_isGeneratorInputOnly already exempts
// _test.go files from its "nothing outside cmd/ reads the model snapshot at
// runtime" rule, so a plain test reading models/aws/shapes/sqs.json here
// follows the same pattern internal/awsapi/shapes_provenance_test.go and
// cmd/compatgen/model.go's ErrorCode already use.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/overcast-sh/overcast/internal/awsmodel"
)

// modeledQueryErrorCode reproduces cmd/compatgen/model.go's ErrorCode: the
// aws.protocols#awsQueryError trait's code when the shape declares one, else
// the shape's own name — AWS's documented fallback for awsQueryCompatible
// services. Restated here rather than imported because ErrorCode lives in
// package main (cmd/compatgen).
func modeledQueryErrorCode(t *testing.T, shapes map[string]awsmodel.SnapshotShape, shape string) string {
	t.Helper()
	s, ok := shapes[shape]
	if !ok {
		t.Fatalf("models/aws/shapes/sqs.json has no shape %q — has the snapshot been regenerated since this table was written?", shape)
	}
	raw, ok := s.Traits["aws.protocols#awsQueryError"]
	if !ok {
		return shape
	}
	var trait struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &trait); err != nil {
		t.Fatalf("shape %q: parse aws.protocols#awsQueryError trait: %v", shape, err)
	}
	if trait.Code == "" {
		t.Fatalf("shape %q: aws.protocols#awsQueryError trait has no code", shape)
	}
	return trait.Code
}

// TestSQSQueryErrorCodes_matchModel proves every entry in sqsQueryErrorCodes
// names a real shape in the pinned SQS model and states that shape's actual
// legacy Query-protocol code — the thing #1810's x-amzn-query-error header
// must carry. A model refresh that changes or removes an
// aws.protocols#awsQueryError trait fails this test rather than silently
// producing a wrong header.
func TestSQSQueryErrorCodes_matchModel(t *testing.T) {
	snapshotPath := filepath.Join("..", "..", "..", "models", "aws", "shapes", "sqs.json")
	contents, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatalf("read %s: %v", snapshotPath, err)
	}
	var snapshot awsmodel.Snapshot
	if err := json.Unmarshal(contents, &snapshot); err != nil {
		t.Fatalf("parse %s: %v", snapshotPath, err)
	}

	if len(sqsQueryErrorCodes) == 0 {
		t.Fatal("sqsQueryErrorCodes is empty")
	}
	for code, mapping := range sqsQueryErrorCodes {
		t.Run(code, func(t *testing.T) {
			want := modeledQueryErrorCode(t, snapshot.Shapes, mapping.Shape)
			if mapping.LegacyCode != want {
				t.Errorf("sqsQueryErrorCodes[%q] = %q, but the model's %s shape says the legacy code is %q",
					code, mapping.LegacyCode, mapping.Shape, want)
			}
		})
	}
}

// TestSQSLegacyQueryErrorCode_fallsBackToCodeItself documents the identity
// fallback sqsLegacyQueryErrorCode uses for a Code with no table entry —
// exercising it directly (rather than only via the table above) because the
// fallback is the majority case: every generic error SQS shares with other
// JSON-protocol services (InvalidParameterValue, MissingParameter,
// InvalidAction, ...) and every SQS shape with no awsQueryError trait of its
// own (InvalidAttributeName, InvalidAttributeValue) goes through it.
func TestSQSLegacyQueryErrorCode_fallsBackToCodeItself(t *testing.T) {
	for _, code := range []string{"InvalidParameterValue", "MissingParameter", "InvalidAction", "InvalidAttributeName", "InvalidAttributeValue"} {
		if got := sqsLegacyQueryErrorCode(code); got != code {
			t.Errorf("sqsLegacyQueryErrorCode(%q) = %q, want %q (identity fallback)", code, got, code)
		}
	}
}
