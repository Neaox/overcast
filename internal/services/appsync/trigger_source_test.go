package appsync

// trigger_source_test.go — pins the Lambda proactive-init trigger evidence
// from AWS_LAMBDA data sources.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

func TestReferencesFunction_detectsLambdaDataSources(t *testing.T) {
	// Given: a stored AWS_LAMBDA data source targeting one function.
	svc := New(&config.Config{Region: "us-east-1"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
	ctx := context.Background()
	arn := "arn:aws:lambda:us-east-1:000000000000:function:resolver-fn"
	record := `{"name":"ds1","type":"AWS_LAMBDA","lambdaConfig":{"lambdaFunctionArn":"` + arn + `"}}`
	if err := svc.handler.store.s.Set(ctx, storeNS, serviceutil.RegionKey("us-east-1", prefixDS+"api1:ds1"), record); err != nil {
		t.Fatalf("seed data source: %v", err)
	}

	// Then: the targeted function is attested; others are not.
	if !svc.ReferencesFunction(ctx, arn) {
		t.Fatal("lambda data source not detected")
	}
	if svc.ReferencesFunction(ctx, "arn:aws:lambda:us-east-1:000000000000:function:resolver") {
		t.Fatal("ARN prefix attested a different function")
	}
	if svc.ReferencesFunction(ctx, "arn:aws:lambda:us-east-1:000000000000:function:unwired") {
		t.Fatal("unreferenced function attested")
	}
}
