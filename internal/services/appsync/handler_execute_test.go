package appsync

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/state"
)

type captureLambdaInvoker struct {
	functionName string
	payload      []byte
	payloads     [][]byte
	outcome      *events.InvokeOutcome
}

func TestAuthenticateCognito_identityShape(t *testing.T) {
	// Given: a Cognito-authenticated request with JWT claims and forwarded IPs.
	h := &Handler{}
	claims := map[string]any{
		"sub":              "user-sub",
		"iss":              "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool",
		"cognito:username": "alice",
		"email":            "alice@example.com",
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.RemoteAddr = "10.0.0.1:12345"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 203.0.113.11")
	req.Header.Set("Authorization", "Bearer "+unsignedJWT(t, claims))

	// When: AppSync authenticates the request.
	identity, authErr := h.authenticateCognito(req, nil)

	// Then: the resolver identity includes the AWS-documented Cognito fields.
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	assertIdentityField(t, identity, "sub", "user-sub")
	assertIdentityField(t, identity, "issuer", "https://cognito-idp.us-east-1.amazonaws.com/us-east-1_pool")
	assertIdentityField(t, identity, "username", "alice")
	assertIdentityField(t, identity, "defaultAuthStrategy", "ALLOW")
	sourceIP, ok := identity["sourceIp"].([]string)
	if !ok {
		t.Fatalf("expected sourceIp []string, got %#v", identity["sourceIp"])
	}
	if len(sourceIP) != 3 || sourceIP[0] != "203.0.113.10" || sourceIP[1] != "203.0.113.11" || sourceIP[2] != "10.0.0.1" {
		t.Fatalf("unexpected sourceIp: %#v", sourceIP)
	}
}

func TestTryAuthIAM_identityShape(t *testing.T) {
	// Given: an IAM-authenticated request signed with an access key credential scope.
	h := &Handler{cfg: &config.Config{AccountID: "123456789012"}}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.RemoteAddr = "10.0.0.2:12345"
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=AKIAEXAMPLE/20260723/us-east-1/appsync/aws4_request, SignedHeaders=host;x-amz-date, Signature=abc")

	// When: AppSync accepts IAM auth.
	identity, authErr := h.tryAuth(req, &GraphqlAPI{}, "AWS_IAM", nil, nil, nil)

	// Then: the resolver identity has the AWS-documented IAM keys that can be derived locally.
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	assertIdentityField(t, identity, "accountId", "123456789012")
	assertIdentityField(t, identity, "username", "AKIAEXAMPLE")
	assertIdentityField(t, identity, "user", "AKIAEXAMPLE")
	assertIdentityField(t, identity, "userArn", "arn:aws:iam::123456789012:user/AKIAEXAMPLE")
}

func TestAuthenticateOIDC_identityShape(t *testing.T) {
	// Given: an OIDC-authenticated request and configured issuer override.
	h := &Handler{}
	claims := map[string]any{
		"sub":      "oidc-sub",
		"iss":      "https://token.example.com",
		"username": "oidc-user",
	}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.RemoteAddr = "10.0.0.3:12345"
	req.Header.Set("Authorization", "Bearer "+unsignedJWT(t, claims))
	oidcConfig := json.RawMessage(`{"issuer":"https://configured.example.com"}`)

	// When: AppSync authenticates with OIDC.
	identity, authErr := h.authenticateOIDC(req, oidcConfig)

	// Then: the identity includes issuer, username, claims, and source IP.
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	assertIdentityField(t, identity, "sub", "oidc-sub")
	assertIdentityField(t, identity, "issuer", "https://configured.example.com")
	assertIdentityField(t, identity, "username", "oidc-user")
	if _, ok := identity["claims"].(map[string]any); !ok {
		t.Fatalf("expected claims map, got %#v", identity["claims"])
	}
	if got := identity["sourceIp"].([]string); len(got) != 1 || got[0] != "10.0.0.3" {
		t.Fatalf("unexpected sourceIp: %#v", got)
	}
}

func TestTryAuthLambdaAuthorizer_identityShape(t *testing.T) {
	// Given: an API using AWS_LAMBDA auth in the current accept-all emulator mode.
	h := &Handler{}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)

	// When: AppSync accepts Lambda authorizer auth.
	identity, authErr := h.tryAuth(req, &GraphqlAPI{}, "AWS_LAMBDA", nil, nil, nil)

	// Then: identity follows the documented resolverContext container shape.
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	resolverContext, ok := identity["resolverContext"].(map[string]any)
	if !ok {
		t.Fatalf("expected resolverContext map, got %#v", identity["resolverContext"])
	}
	if len(resolverContext) != 0 {
		t.Fatalf("expected stub resolverContext to be empty, got %#v", resolverContext)
	}
}

func TestTryAuthLambdaAuthorizer_invokesFunction(t *testing.T) {
	// Given: an API using a configured Lambda authorizer.
	invoker := &captureLambdaInvoker{outcome: &events.InvokeOutcome{Payload: []byte(`{"isAuthorized":true,"resolverContext":{"tenant":"acme"}}`)}}
	h := &Handler{cfg: &config.Config{AccountID: "123456789012"}, invoker: invoker}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Authorization", "custom-token")
	req.Header.Set("X-Custom", "visible")
	lambdaCfg := json.RawMessage(`{"authorizerUri":"arn:aws:lambda:us-east-1:123456789012:function:auth-fn"}`)

	// When: AppSync authenticates with AWS_LAMBDA.
	identity, authErr := h.tryAuth(req, &GraphqlAPI{ApiId: "api123"}, "AWS_LAMBDA", nil, nil, lambdaCfg)

	// Then: the authorizer is invoked and resolverContext becomes resolver identity.
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	if invoker.functionName != "auth-fn" {
		t.Fatalf("expected auth-fn invocation, got %q", invoker.functionName)
	}
	var event map[string]any
	if err := json.Unmarshal(invoker.payload, &event); err != nil {
		t.Fatalf("authorizer payload is not JSON: %v", err)
	}
	if event["authorizationToken"] != "custom-token" {
		t.Fatalf("expected authorization token, got %#v", event["authorizationToken"])
	}
	requestContext := event["requestContext"].(map[string]any)
	if requestContext["apiId"] != "api123" || requestContext["accountId"] != "123456789012" {
		t.Fatalf("unexpected requestContext: %#v", requestContext)
	}
	resolverContext := identity["resolverContext"].(map[string]any)
	if resolverContext["tenant"] != "acme" {
		t.Fatalf("expected resolverContext tenant, got %#v", resolverContext)
	}
}

func TestTryAuthLambdaAuthorizer_unauthorized(t *testing.T) {
	// Given: a Lambda authorizer that denies the token.
	invoker := &captureLambdaInvoker{outcome: &events.InvokeOutcome{Payload: []byte(`{"isAuthorized":false}`)}}
	h := &Handler{invoker: invoker}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Authorization", "custom-token")
	lambdaCfg := json.RawMessage(`{"authorizerUri":"arn:aws:lambda:us-east-1:000000000000:function:auth-fn"}`)

	// When: AppSync authenticates with AWS_LAMBDA.
	_, authErr := h.tryAuth(req, &GraphqlAPI{ApiId: "api123"}, "AWS_LAMBDA", nil, nil, lambdaCfg)

	// Then: the request is rejected with UnauthorizedException.
	if authErr == nil {
		t.Fatal("expected unauthorized error")
	}
	if authErr.Code != "UnauthorizedException" {
		t.Fatalf("expected UnauthorizedException, got %#v", authErr)
	}
}

func TestTryAuthLambdaAuthorizer_deniedFields(t *testing.T) {
	// Given: a Lambda authorizer that allows the request but denies Query.secret.
	invoker := &captureLambdaInvoker{outcome: &events.InvokeOutcome{Payload: []byte(`{"isAuthorized":true,"resolverContext":{"tenant":"acme"},"deniedFields":["Query.secret"]}`)}}
	h := &Handler{invoker: invoker}
	req := httptest.NewRequest(http.MethodPost, "/graphql", nil)
	req.Header.Set("Authorization", "custom-token")
	lambdaCfg := json.RawMessage(`{"authorizerUri":"arn:aws:lambda:us-east-1:000000000000:function:auth-fn"}`)

	// When: AppSync authenticates with AWS_LAMBDA.
	identity, authErr := h.tryAuth(req, &GraphqlAPI{ApiId: "api123"}, "AWS_LAMBDA", nil, nil, lambdaCfg)

	// Then: deniedFields are retained internally for response nulling.
	if authErr != nil {
		t.Fatalf("unexpected auth error: %v", authErr)
	}
	if !h.deniesField(&GraphqlAPI{ApiId: "api123"}, identity, "Query", "secret") {
		t.Fatal("expected Query.secret to be denied")
	}
	if h.deniesField(&GraphqlAPI{ApiId: "api123"}, identity, "Query", "public") {
		t.Fatal("did not expect Query.public to be denied")
	}
}

func TestDeniedFields_fullARNDoesNotDenyOtherAPI(t *testing.T) {
	// Given: a Lambda authorizer deniedFields entry scoped to another API ARN.
	h := &Handler{cfg: &config.Config{Region: "us-east-1", AccountID: "000000000000"}}
	identity := map[string]any{
		lambdaAuthorizerDeniedFieldsKey: []string{"arn:aws:appsync:us-east-1:000000000000:apis/otherapi/types/Query/fields/secret"},
	}

	// When/Then: the current API's same short field is not denied by the other API's full ARN.
	if h.deniesField(&GraphqlAPI{ApiId: "api123"}, identity, "Query", "secret") {
		t.Fatal("did not expect a full ARN for another API to deny this API's field")
	}
}

func TestFilterSubFields_authorizerDeniedStructuralField(t *testing.T) {
	// Given: a structurally resolved object with a field denied by Lambda authorizer.
	h := &Handler{}
	identity := map[string]any{lambdaAuthorizerDeniedFieldsKey: []string{"Post.secret"}}
	field := &ast.Field{SelectionSet: ast.SelectionSet{
		&ast.Field{Name: "id"},
		&ast.Field{Name: "secret"},
	}}
	result := map[string]any{"id": "post-1", "secret": "hidden"}

	// When: AppSync structurally filters the selected subfields.
	filtered := h.filterSubFields(result, field, &GraphqlAPI{ApiId: "api123"}, identity, "Post").(map[string]any)

	// Then: the denied structural field is nulled, not leaked.
	if filtered["id"] != "post-1" {
		t.Fatalf("expected id to pass through, got %#v", filtered["id"])
	}
	if filtered["secret"] != nil {
		t.Fatalf("expected denied field to be nil, got %#v", filtered["secret"])
	}
}

func TestBuildDirectLambdaResolverEvent_hidesAuthorizerDeniedFields(t *testing.T) {
	// Given: resolver identity with authorizer resolverContext and internal denied fields.
	h := &Handler{}
	rctx := &resolveContext{Identity: map[string]any{
		"resolverContext":               map[string]any{"tenant": "acme"},
		lambdaAuthorizerDeniedFieldsKey: []string{"Query.secret"},
	}}

	// When: AppSync builds a direct Lambda resolver event.
	event := h.buildDirectLambdaResolverEvent(rctx)

	// Then: Lambda sees resolverContext but not internal deniedFields metadata.
	identity := event["identity"].(map[string]any)
	if _, ok := identity[lambdaAuthorizerDeniedFieldsKey]; ok {
		t.Fatalf("internal deniedFields leaked into identity: %#v", identity)
	}
	resolverContext := identity["resolverContext"].(map[string]any)
	if resolverContext["tenant"] != "acme" {
		t.Fatalf("expected resolverContext tenant, got %#v", resolverContext)
	}
}

func TestBuildDirectLambdaResolverEvent_defaultDomainAndHeaders(t *testing.T) {
	// Given: a resolver context with Cookie and normal headers.
	h := &Handler{}
	rctx := &resolveContext{
		Headers: http.Header{
			"Cookie":        []string{"session=secret"},
			"X-Custom":      []string{"visible"},
			"Authorization": []string{"Bearer token"},
		},
	}

	// When: AppSync builds the direct Lambda resolver event.
	event := h.buildDirectLambdaResolverEvent(rctx)

	// Then: default endpoint domainName is null and Cookie is not exposed.
	request := event["request"].(map[string]any)
	if request["domainName"] != nil {
		t.Fatalf("expected default domainName to be nil, got %#v", request["domainName"])
	}
	headers := request["headers"].(map[string]string)
	if _, ok := headers["Cookie"]; ok {
		t.Fatalf("expected Cookie header to be omitted, got %#v", headers)
	}
	if headers["X-Custom"] != "visible" {
		t.Fatalf("expected X-Custom header to be preserved, got %#v", headers)
	}
}

func (i *captureLambdaInvoker) Invoke(_ context.Context, functionName string, payload []byte) (*events.InvokeOutcome, error) {
	i.functionName = functionName
	i.payload = append([]byte(nil), payload...)
	i.payloads = append(i.payloads, append([]byte(nil), payload...))
	if i.outcome != nil {
		return i.outcome, nil
	}
	return &events.InvokeOutcome{Payload: []byte(`{"ok":true}`)}, nil
}

func TestLambdaFunctionNameFromARN_qualifiedAlias(t *testing.T) {
	// Given: a Lambda alias ARN stored in an AppSync data source.
	arn := "arn:aws:lambda:us-east-1:000000000000:function:lambda-function-l-ue1-digital-guides-namespace:live"

	// When: AppSync extracts the name to invoke Lambda.
	got := lambdaFunctionNameFromARN(arn)

	// Then: the qualifier is stripped and the underlying function is invoked.
	want := "lambda-function-l-ue1-digital-guides-namespace"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestResolveLambdaDataSource_directResolverEventShape(t *testing.T) {
	// Given: a direct Lambda resolver context with variables, identity, headers, and selections.
	invoker := &captureLambdaInvoker{}
	h := &Handler{invoker: invoker}
	ds := &DataSource{LambdaConfig: json.RawMessage(`{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:namespace-fn"}`)}
	rctx := &resolveContext{
		FieldName:  "namespaces",
		ParentType: "Query",
		Arguments:  map[string]any{"companyId": float64(4)},
		Variables:  map[string]any{"limit": float64(10)},
		Headers:    http.Header{"X-Api-Key": []string{"test-key"}},
		Identity:   map[string]any{"sub": "user-1"},
	}

	// When: AppSync invokes the Lambda data source without a request mapping template.
	_, gqlErr := h.resolveLambdaDataSource(context.Background(), ds, "", rctx)

	// Then: the payload is compatible with AWS AppSync direct Lambda resolver events.
	if gqlErr != nil {
		t.Fatalf("unexpected GraphQL error: %v", gqlErr)
	}
	if invoker.functionName != "namespace-fn" {
		t.Fatalf("expected function namespace-fn, got %q", invoker.functionName)
	}

	var event map[string]any
	if err := json.Unmarshal(invoker.payload, &event); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if _, ok := event["identity"]; !ok {
		t.Fatal("expected direct Lambda event to include identity")
	}
	request, ok := event["request"].(map[string]any)
	if !ok {
		t.Fatalf("expected request object, got %#v", event["request"])
	}
	if _, ok := request["domainName"]; !ok {
		t.Fatal("expected request.domainName to be present")
	}
	info, ok := event["info"].(map[string]any)
	if !ok {
		t.Fatalf("expected info object, got %#v", event["info"])
	}
	if _, ok := info["variables"]; !ok {
		t.Fatal("expected info.variables to be present")
	}
	if _, ok := info["selectionSetGraphQL"]; !ok {
		t.Fatal("expected info.selectionSetGraphQL to be present")
	}
}

func unsignedJWT(t *testing.T, claims map[string]any) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return header + "." + payload + "."
}

func assertIdentityField(t *testing.T, identity map[string]any, key string, want any) {
	t.Helper()
	if got := identity[key]; got != want {
		t.Fatalf("expected identity[%q]=%#v, got %#v", key, want, got)
	}
}

func TestResolveLambdaDataSource_requestMappingTemplatePayload(t *testing.T) {
	// Given: a Lambda resolver with an evaluated request mapping template.
	invoker := &captureLambdaInvoker{}
	h := &Handler{invoker: invoker}
	ds := &DataSource{LambdaConfig: json.RawMessage(`{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:mapped-fn"}`)}
	templatePayload := `{"version":"2018-05-29","operation":"Invoke","payload":{"companyId":4}}`

	// When: AppSync invokes the Lambda data source with the mapped request.
	_, gqlErr := h.resolveLambdaDataSource(context.Background(), ds, templatePayload, &resolveContext{})

	// Then: Lambda receives the evaluated mapping document instead of the direct context.
	if gqlErr != nil {
		t.Fatalf("unexpected GraphQL error: %v", gqlErr)
	}
	if got := string(invoker.payload); got != templatePayload {
		t.Fatalf("expected mapped payload %s, got %s", templatePayload, got)
	}
}

func TestResolveLambdaDataSource_requestMappingTemplateInvalidOperation(t *testing.T) {
	// Given: a Lambda resolver request object with an unsupported operation.
	invoker := &captureLambdaInvoker{}
	h := &Handler{invoker: invoker}
	ds := &DataSource{LambdaConfig: json.RawMessage(`{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:mapped-fn"}`)}
	templatePayload := `{"version":"2018-05-29","operation":"GetItem","payload":{"companyId":4}}`

	// When: AppSync invokes the Lambda data source.
	_, gqlErr := h.resolveLambdaDataSource(context.Background(), ds, templatePayload, &resolveContext{})

	// Then: the invalid Lambda operation is rejected before invoking Lambda.
	if gqlErr == nil {
		t.Fatal("expected GraphQL error")
	}
	if invoker.functionName != "" {
		t.Fatalf("expected Lambda not to be invoked, got %q", invoker.functionName)
	}
}

func TestResolveLambdaDataSource_requestMappingTemplateBatchInvokeUnsupported(t *testing.T) {
	// Given: a mapped Lambda request object using BatchInvoke outside the direct batching path.
	invoker := &captureLambdaInvoker{}
	h := &Handler{invoker: invoker}
	ds := &DataSource{LambdaConfig: json.RawMessage(`{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:mapped-fn"}`)}
	templatePayload := `{"version":"2018-05-29","operation":"BatchInvoke","payload":{"companyId":4}}`

	// When: AppSync invokes the Lambda data source.
	_, gqlErr := h.resolveLambdaDataSource(context.Background(), ds, templatePayload, &resolveContext{})

	// Then: BatchInvoke is rejected rather than silently treated as a single Invoke.
	if gqlErr == nil {
		t.Fatal("expected GraphQL error")
	}
	if invoker.functionName != "" {
		t.Fatalf("expected Lambda not to be invoked, got %q", invoker.functionName)
	}
}

func TestResolveLambdaDataSource_requestMappingTemplateEventInvocation(t *testing.T) {
	// Given: a Lambda resolver request object using async Event invocation.
	invoker := &captureLambdaInvoker{}
	h := &Handler{invoker: invoker}
	ds := &DataSource{LambdaConfig: json.RawMessage(`{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:mapped-fn"}`)}
	templatePayload := `{"version":"2018-05-29","operation":"Invoke","invocationType":"Event","payload":{"companyId":4}}`

	// When: AppSync invokes the Lambda data source.
	result, gqlErr := h.resolveLambdaDataSource(context.Background(), ds, templatePayload, &resolveContext{})

	// Then: Lambda receives the event and the field resolves to null without a response handler.
	if gqlErr != nil {
		t.Fatalf("unexpected GraphQL error: %v", gqlErr)
	}
	if invoker.functionName != "mapped-fn" {
		t.Fatalf("expected mapped-fn invocation, got %q", invoker.functionName)
	}
	if result != nil {
		t.Fatalf("expected nil result for Event invocation, got %#v", result)
	}
}

func TestResolveDirectLambdaBatchDataSource(t *testing.T) {
	// Given: two batched direct Lambda resolver contexts.
	invoker := &captureLambdaInvoker{outcome: &events.InvokeOutcome{Payload: []byte(`[{"data":{"name":"one"}},{"data":{"name":"two"}}]`)}}
	h := &Handler{invoker: invoker}
	ds := &DataSource{LambdaConfig: json.RawMessage(`{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:batch-fn"}`)}
	rctxs := []*resolveContext{
		{FieldName: "child", ParentType: "Item", Source: map[string]any{"id": "1"}},
		{FieldName: "child", ParentType: "Item", Source: map[string]any{"id": "2"}},
	}

	// When: AppSync batch-invokes the direct Lambda resolver.
	results, gqlErrs := h.resolveDirectLambdaBatchDataSource(context.Background(), ds, rctxs)

	// Then: Lambda receives an array of resolver context events and returns per-item data.
	if len(gqlErrs) != 0 {
		t.Fatalf("unexpected GraphQL errors: %#v", gqlErrs)
	}
	if invoker.functionName != "batch-fn" {
		t.Fatalf("expected batch-fn invocation, got %q", invoker.functionName)
	}
	var payload []map[string]any
	if err := json.Unmarshal(invoker.payload, &payload); err != nil {
		t.Fatalf("batch payload is not an event list: %v", err)
	}
	if len(payload) != 2 {
		t.Fatalf("expected 2 batch events, got %#v", payload)
	}
	first := results[0].(map[string]any)
	second := results[1].(map[string]any)
	if first["name"] != "one" || second["name"] != "two" {
		t.Fatalf("unexpected batch results: %#v", results)
	}
}

func TestResolveNestedField_directLambdaBatchResolver(t *testing.T) {
	// Given: a list field whose child field is a direct Lambda resolver with maxBatchSize.
	ctx := context.Background()
	store := newStore(state.NewMemoryStore(), "us-east-1")
	ds := &DataSource{
		Name:         "LambdaDS",
		Type:         "AWS_LAMBDA",
		LambdaConfig: json.RawMessage(`{"lambdaFunctionArn":"arn:aws:lambda:us-east-1:000000000000:function:batch-fn"}`),
	}
	if err := store.PutDataSource(ctx, "api123", ds); err != nil {
		t.Fatalf("put data source: %v", err)
	}
	if err := store.PutResolver(ctx, "api123", &Resolver{TypeName: "Item", FieldName: "child", DataSourceName: "LambdaDS", MaxBatchSize: 2}); err != nil {
		t.Fatalf("put resolver: %v", err)
	}
	invoker := &captureLambdaInvoker{outcome: &events.InvokeOutcome{Payload: []byte(`[{"data":"batched"}]`)}}
	h := &Handler{store: store, invoker: invoker}
	field := &ast.Field{
		Definition:   &ast.FieldDefinition{Type: ast.ListType(ast.NamedType("Item", nil), nil)},
		SelectionSet: ast.SelectionSet{&ast.Field{Name: "id"}, &ast.Field{Name: "child"}},
	}
	items := []any{map[string]any{"id": "one"}}

	// When: nested list selection resolution sees the batchable child resolver.
	resolved, errs := h.resolveNestedField(httptest.NewRequest(http.MethodPost, "/graphql", nil), &GraphqlAPI{ApiId: "api123"}, field, items, nil, nil)

	// Then: the child resolver is batch-invoked and mapped into the list item.
	if len(errs) != 0 {
		t.Fatalf("unexpected GraphQL errors: %#v", errs)
	}
	if len(invoker.payloads) != 1 {
		t.Fatalf("expected one batched Lambda invocation, got %d", len(invoker.payloads))
	}
	list := resolved.([]any)
	item := list[0].(map[string]any)
	if item["id"] != "one" || item["child"] != "batched" {
		t.Fatalf("unexpected resolved item: %#v", item)
	}
}

func TestBuildDirectLambdaResolverEvent_pipelineContext(t *testing.T) {
	// Given: a pipeline function context with shared stash and previous result.
	h := &Handler{}
	rctx := &resolveContext{
		Stash:      map[string]any{"message": "from-stash"},
		PrevResult: map[string]any{"id": "prev"},
	}

	// When: AppSync builds the direct Lambda resolver event.
	event := h.buildDirectLambdaResolverEvent(rctx)

	// Then: the pipeline context is visible to Lambda.
	stash := event["stash"].(map[string]any)
	if stash["message"] != "from-stash" {
		t.Fatalf("expected stash propagation, got %#v", stash)
	}
	prev := event["prev"].(map[string]any)
	prevResult := prev["result"].(map[string]any)
	if prevResult["id"] != "prev" {
		t.Fatalf("expected prev.result propagation, got %#v", prev)
	}
}
