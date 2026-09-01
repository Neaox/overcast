package apigateway

// trigger_source_test.go — pins the Lambda proactive-init trigger evidence:
// integrations referencing a function are detected, and an ARN that is a
// prefix of another function's ARN never false-positives.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

func TestReferencesFunction_detectsIntegrationsWithoutPrefixCollisions(t *testing.T) {
	// Given: a REST integration targeting app-2 and an HTTP API integration
	// targeting app.
	svc := New(&config.Config{Region: "us-east-1"}, state.NewMemoryStore(), zap.NewNop(), clock.NewMock())
	ctx := context.Background()
	app := "arn:aws:lambda:us-east-1:000000000000:function:app"
	app2 := "arn:aws:lambda:us-east-1:000000000000:function:app-2"
	res := &Resource{
		ID:   "r1",
		Path: "/x",
		ResourceMethods: map[string]*Method{
			"GET": {HTTPMethod: "GET", MethodIntegration: &Integration{
				Type: "AWS_PROXY",
				URI:  "arn:aws:apigateway:us-east-1:lambda:path/2015-03-31/functions/" + app2 + "/invocations",
			}},
		},
	}
	if aerr := svc.handler.store.putResource(ctx, "api1", res); aerr != nil {
		t.Fatalf("putResource: %v", aerr)
	}
	if aerr := svc.handler.store.putV2Integration(ctx, "api2", &IntegrationV2{
		IntegrationID:   "i1",
		IntegrationType: "AWS_PROXY",
		IntegrationURI:  app,
	}); aerr != nil {
		t.Fatalf("putV2Integration: %v", aerr)
	}

	// Then: each function is attested by its own integration…
	if !svc.ReferencesFunction(ctx, app2) {
		t.Fatal("REST integration for app-2 not detected")
	}
	if !svc.ReferencesFunction(ctx, app) {
		t.Fatal("HTTP API integration for app not detected")
	}
	// …an unreferenced function is not…
	if svc.ReferencesFunction(ctx, "arn:aws:lambda:us-east-1:000000000000:function:unwired") {
		t.Fatal("unreferenced function attested")
	}
	// …and app must not be attested by app-2's integration alone. Remove
	// app's own integration to prove the prefix guard.
	if aerr := svc.handler.store.deleteV2Integration(ctx, "api2", "i1"); aerr != nil {
		t.Fatalf("deleteV2Integration: %v", aerr)
	}
	if svc.ReferencesFunction(ctx, app) {
		t.Fatal("ARN prefix of app-2 attested app — delimiter guard broken")
	}
}
