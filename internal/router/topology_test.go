package router

import (
	"encoding/json"
	"testing"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/state"
)

func TestBuildTopologyESMEdgesFromLambdaESMNamespace(t *testing.T) {
	// Given: an SQS queue, a Lambda function, and an ESM linking them.
	queuePayload, _ := json.Marshal(map[string]any{
		"name": "my-queue",
		"arn":  "arn:aws:sqs:us-east-1:000000000000:my-queue",
	})
	functionPayload, _ := json.Marshal(map[string]any{
		"name": "my-function",
		"arn":  "arn:aws:lambda:us-east-1:000000000000:function:my-function",
	})
	esmPayload, _ := json.Marshal(map[string]any{
		"FunctionArn":    "arn:aws:lambda:us-east-1:000000000000:function:my-function",
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:my-queue",
	})

	resp := buildTopology(&config.Config{Region: "us-east-1"}, map[string][]state.KV{
		tNsQueues:    {{Key: "us-east-1/my-queue", Value: string(queuePayload)}},
		tNsFunctions: {{Key: "us-east-1/my-function", Value: string(functionPayload)}},
		tNsESM:       {{Key: "us-east-1/uuid-1", Value: string(esmPayload)}},
	}, "")

	edges := map[string]topologyEdge{}
	for _, edge := range resp.Edges {
		edges[edge.ID] = edge
	}

	wantID := "esm::us-east-1::sqs::my-queue→us-east-1::lambda::my-function"
	if _, ok := edges[wantID]; !ok {
		t.Fatalf("expected SQS → Lambda ESM edge (%s), got edges: %v", wantID, resp.Edges)
	}
	if got := edges[wantID].Type; got != "esm" {
		t.Errorf("edge type: got %q, want %q", got, "esm")
	}
}

func TestBuildTopologyAddsECREdgesForLambdaAndECSConsumers(t *testing.T) {
	repoPayload, _ := json.Marshal(map[string]any{
		"repositoryArn":  "arn:aws:ecr:us-east-1:000000000000:repository/sample-app",
		"repositoryName": "sample-app",
		"repositoryUri":  "localhost:5000/000000000000/sample-app",
	})
	lambdaPayload, _ := json.Marshal(map[string]any{
		"name":         "image-fn",
		"arn":          "arn:aws:lambda:us-east-1:000000000000:function:image-fn",
		"package_type": "Image",
		"image_uri":    "localhost:5000/000000000000/sample-app:latest",
	})
	ecsServicePayload, _ := json.Marshal(map[string]any{
		"serviceName":    "web",
		"serviceArn":     "arn:aws:ecs:us-east-1:000000000000:service/demo/web",
		"clusterArn":     "arn:aws:ecs:us-east-1:000000000000:cluster/demo",
		"taskDefinition": "arn:aws:ecs:us-east-1:000000000000:task-definition/demo:3",
		"status":         "ACTIVE",
	})
	taskDefPayload, _ := json.Marshal(map[string]any{
		"taskDefinitionArn": "arn:aws:ecs:us-east-1:000000000000:task-definition/demo:3",
		"containerDefinitions": []map[string]any{{
			"name":  "app",
			"image": "localhost:5000/000000000000/sample-app:prod",
		}},
	})

	resp := buildTopology(&config.Config{Region: "us-east-1"}, map[string][]state.KV{
		tNsECRRepos:    {{Key: "us-east-1/sample-app", Value: string(repoPayload)}},
		tNsFunctions:   {{Key: "us-east-1/image-fn", Value: string(lambdaPayload)}},
		tNsECSServices: {{Key: "us-east-1/web", Value: string(ecsServicePayload)}},
		tNsECSTaskDefs: {{Key: "us-east-1/demo:3", Value: string(taskDefPayload)}},
	}, "")

	nodes := map[string]topologyNode{}
	for _, node := range resp.Nodes {
		nodes[node.ID] = node
	}
	if _, ok := nodes["us-east-1::ecr::sample-app"]; !ok {
		t.Fatalf("expected ECR repository node, got %#v", resp.Nodes)
	}
	if got := nodes["us-east-1::ecr::sample-app"].RepositoryUri; got != "localhost:5000/000000000000/sample-app" {
		t.Fatalf("expected repositoryUri to propagate to topology node, got %q", got)
	}

	edges := map[string]topologyEdge{}
	for _, edge := range resp.Edges {
		edges[edge.ID] = edge
	}
	if _, ok := edges["ecr-lambda::us-east-1::ecr::sample-app→us-east-1::lambda::image-fn"]; !ok {
		t.Fatalf("expected ECR → Lambda edge, got %#v", resp.Edges)
	}
	if _, ok := edges["ecr-ecs::us-east-1::ecr::sample-app→us-east-1::ecs-service::cluster/demo/web"]; !ok {
		t.Fatalf("expected ECR → ECS service edge, got %#v", resp.Edges)
	}
}

func TestBuildTopologyMatchesECRConsumersWithNormalizedImageRefs(t *testing.T) {
	repoPayload, _ := json.Marshal(map[string]any{
		"repositoryArn":  "arn:aws:ecr:us-east-1:000000000000:repository/sample-app",
		"repositoryName": "sample-app",
		"repositoryUri":  "https://localhost:5000/000000000000/sample-app",
	})
	lambdaPayload, _ := json.Marshal(map[string]any{
		"name":         "image-fn",
		"arn":          "arn:aws:lambda:us-east-1:000000000000:function:image-fn",
		"package_type": "Image",
		"image_uri":    "localhost:5000/000000000000/sample-app@sha256:deadbeef",
	})
	ecsServicePayload, _ := json.Marshal(map[string]any{
		"serviceName":    "web",
		"serviceArn":     "arn:aws:ecs:us-east-1:000000000000:service/demo/web",
		"clusterArn":     "arn:aws:ecs:us-east-1:000000000000:cluster/demo",
		"taskDefinition": "arn:aws:ecs:us-east-1:000000000000:task-definition/demo:3",
		"status":         "ACTIVE",
	})
	taskDefPayload, _ := json.Marshal(map[string]any{
		"taskDefinitionArn": "arn:aws:ecs:us-east-1:000000000000:task-definition/demo:3",
		"containerDefinitions": []map[string]any{{
			"name":  "app",
			"image": "https://localhost:5000/000000000000/sample-app:prod",
		}},
	})

	resp := buildTopology(&config.Config{Region: "us-east-1"}, map[string][]state.KV{
		tNsECRRepos:    {{Key: "us-east-1/sample-app", Value: string(repoPayload)}},
		tNsFunctions:   {{Key: "us-east-1/image-fn", Value: string(lambdaPayload)}},
		tNsECSServices: {{Key: "us-east-1/web", Value: string(ecsServicePayload)}},
		tNsECSTaskDefs: {{Key: "us-east-1/demo:3", Value: string(taskDefPayload)}},
	}, "")

	edges := map[string]topologyEdge{}
	for _, edge := range resp.Edges {
		edges[edge.ID] = edge
	}
	if _, ok := edges["ecr-lambda::us-east-1::ecr::sample-app→us-east-1::lambda::image-fn"]; !ok {
		t.Fatalf("expected normalized ECR → Lambda edge, got %#v", resp.Edges)
	}
	if _, ok := edges["ecr-ecs::us-east-1::ecr::sample-app→us-east-1::ecs-service::cluster/demo/web"]; !ok {
		t.Fatalf("expected normalized ECR → ECS service edge, got %#v", resp.Edges)
	}
}

func TestCfnResourceNodeIDMapsNonDefaultTypes(t *testing.T) {
	tests := []struct {
		resType    string
		physicalID string
		want       string
	}{
		{"AWS::ApiGateway::RestApi", "abc123", "us-east-1::apigateway::abc123"},
		{"AWS::ApiGatewayV2::Api", "def456", "us-east-1::apigateway::def456"},
		{"AWS::ApiGateway::Resource", "abc123/res1", ""},
		{"AWS::ApiGateway::Method", "abc123/res1/GET", ""},
		{"AWS::Cognito::UserPool", "us-east-1_A1B2C3D4", "us-east-1::cognito::us-east-1_A1B2C3D4"},
		{"AWS::AppSync::GraphQLApi", "abc123def456", "us-east-1::appsync::abc123def456"},
		{"AWS::CloudFront::Distribution", "E1234567890ABC", "us-east-1::cloudfront::E1234567890ABC"},
	}
	for _, tt := range tests {
		got := cfnResourceNodeID(tCFNResource{Type: tt.resType, PhysicalID: tt.physicalID}, "us-east-1")
		if got != tt.want {
			t.Errorf("cfnResourceNodeID(%s, %s) = %q, want %q", tt.resType, tt.physicalID, got, tt.want)
		}
	}
}

func TestBuildTopologyCountsAPIGatewayRoutesWithRegionPrefixedKeys(t *testing.T) {
	restAPIPayload, _ := json.Marshal(map[string]any{
		"id":             "abc123",
		"name":           "my-api",
		"rootResourceId": "root1",
	})
	resourcePayload, _ := json.Marshal(map[string]any{
		"id":              "res1",
		"pathPart":        "items",
		"resourceMethods": map[string]any{},
	})
	resource2Payload, _ := json.Marshal(map[string]any{
		"id":              "res2",
		"pathPart":        "users",
		"resourceMethods": map[string]any{},
	})
	stagePayload, _ := json.Marshal(map[string]any{
		"stageName":    "prod",
		"deploymentId": "deploy1",
	})

	resp := buildTopology(&config.Config{Region: "ap-southeast-2"}, map[string][]state.KV{
		tNsRestAPIs: {{Key: "ap-southeast-2/abc123", Value: string(restAPIPayload)}},
		tNsAPIResources: {
			{Key: "ap-southeast-2/abc123/res1", Value: string(resourcePayload)},
			{Key: "ap-southeast-2/abc123/res2", Value: string(resource2Payload)},
		},
		tNsAPIStages: {{Key: "ap-southeast-2/abc123/prod", Value: string(stagePayload)}},
	}, "")

	nodes := map[string]topologyNode{}
	for _, node := range resp.Nodes {
		nodes[node.ID] = node
	}
	nodeID := "ap-southeast-2::apigateway::abc123"
	node, ok := nodes[nodeID]
	if !ok {
		t.Fatalf("expected API Gateway node %q, got nodes: %v", nodeID, keys(nodes))
	}
	if node.RouteCount == nil || *node.RouteCount != 2 {
		t.Errorf("expected 2 routes, got %v", node.RouteCount)
	}
	if node.StageCount == nil || *node.StageCount != 1 {
		t.Errorf("expected 1 stage, got %v", node.StageCount)
	}
	if node.Region != "ap-southeast-2" {
		t.Errorf("expected region ap-southeast-2, got %q", node.Region)
	}
}

func TestBuildTopologyAPIGatewayRegionDiffersFromDefault(t *testing.T) {
	// Config region is us-east-1, but API is stored in ap-southeast-2.
	// The node should appear in ap-southeast-2, not the default region.
	restAPIPayload, _ := json.Marshal(map[string]any{
		"id":             "68fea4a56c",
		"name":           "api-l-ase2-web-push-service",
		"rootResourceId": "root1",
	})
	resourcePayload, _ := json.Marshal(map[string]any{
		"id":              "eb7380",
		"pathPart":        "messages",
		"resourceMethods": map[string]any{},
	})
	stagePayload, _ := json.Marshal(map[string]any{
		"stageName":    "prod",
		"deploymentId": "deploy1",
	})

	resp := buildTopology(&config.Config{Region: "us-east-1"}, map[string][]state.KV{
		tNsRestAPIs:     {{Key: "ap-southeast-2/68fea4a56c", Value: string(restAPIPayload)}},
		tNsAPIResources: {{Key: "ap-southeast-2/68fea4a56c/eb7380", Value: string(resourcePayload)}},
		tNsAPIStages:    {{Key: "ap-southeast-2/68fea4a56c/prod", Value: string(stagePayload)}},
	}, "")

	nodes := map[string]topologyNode{}
	for _, node := range resp.Nodes {
		nodes[node.ID] = node
	}
	nodeID := "ap-southeast-2::apigateway::68fea4a56c"
	node, ok := nodes[nodeID]
	if !ok {
		t.Fatalf("expected API Gateway node %q, got nodes: %v", nodeID, keys(nodes))
	}
	if node.Region != "ap-southeast-2" {
		t.Errorf("expected region ap-southeast-2, got %q", node.Region)
	}
	if node.RouteCount == nil || *node.RouteCount != 1 {
		t.Errorf("expected 1 route, got %v", node.RouteCount)
	}
	if node.StageCount == nil || *node.StageCount != 1 {
		t.Errorf("expected 1 stage, got %v", node.StageCount)
	}
}

func TestBuildTopologyESMEdgeRegionMismatch(t *testing.T) {
	// Given: nodes in ap-southeast-2, but ESM has ARNs with us-east-1 (stale
	// data or cross-stack import from a different region).  The topology
	// should still create the edges by falling back to name-based matching.

	queuePayload, _ := json.Marshal(map[string]any{
		"name": "my-queue",
		"arn":  "arn:aws:sqs:ap-southeast-2:000000000000:my-queue",
	})
	functionPayload, _ := json.Marshal(map[string]any{
		"name": "my-function",
		"arn":  "arn:aws:lambda:ap-southeast-2:000000000000:function:my-function",
	})
	tablePayload, _ := json.Marshal(map[string]any{
		"TableName": "my-table",
		"TableArn":  "arn:aws:dynamodb:ap-southeast-2:000000000000:table/my-table",
		"StreamSpecification": map[string]any{
			"StreamEnabled":  true,
			"StreamViewType": "NEW_AND_OLD_IMAGES",
		},
		"LatestStreamArn": "arn:aws:dynamodb:ap-southeast-2:000000000000:table/my-table/stream/2026-01-01T00:00:00.000",
	})
	// ESM ARNs reference us-east-1 (wrong region)
	sqsESM, _ := json.Marshal(map[string]any{
		"FunctionArn":    "arn:aws:lambda:us-east-1:000000000000:function:my-function",
		"EventSourceArn": "arn:aws:sqs:us-east-1:000000000000:my-queue",
	})
	ddbESM, _ := json.Marshal(map[string]any{
		"FunctionArn":    "arn:aws:lambda:us-east-1:000000000000:function:my-function",
		"EventSourceArn": "arn:aws:dynamodb:us-east-1:000000000000:table/my-table/stream/2026-01-01T00:00:00.000",
	})

	resp := buildTopology(&config.Config{Region: "us-east-1"}, map[string][]state.KV{
		tNsQueues:    {{Key: "ap-southeast-2/my-queue", Value: string(queuePayload)}},
		tNsFunctions: {{Key: "ap-southeast-2/my-function", Value: string(functionPayload)}},
		tNsTables:    {{Key: "ap-southeast-2/my-table", Value: string(tablePayload)}},
		tNsESM: {
			{Key: "ap-southeast-2/esm-sqs", Value: string(sqsESM)},
			{Key: "ap-southeast-2/esm-ddb", Value: string(ddbESM)},
		},
	}, "")

	esmEdges := map[string]topologyEdge{}
	for _, e := range resp.Edges {
		if e.Type == "esm" {
			esmEdges[e.ID] = e
		}
	}

	if len(esmEdges) != 2 {
		t.Fatalf("expected 2 ESM edges, got %d: %v", len(esmEdges), esmEdges)
	}

	// Verify SQS → Lambda ESM edge (resolved to ap-southeast-2 nodes)
	sqsEdgeID := "esm::ap-southeast-2::sqs::my-queue→ap-southeast-2::lambda::my-function"
	if _, ok := esmEdges[sqsEdgeID]; !ok {
		t.Errorf("missing SQS ESM edge; want %s, got: %v", sqsEdgeID, keys(esmEdges))
	}

	// Verify DynamoDB → Lambda ESM edge (resolved to ap-southeast-2 nodes)
	ddbEdgeID := "esm::ap-southeast-2::dynamodb::my-table→ap-southeast-2::lambda::my-function"
	if _, ok := esmEdges[ddbEdgeID]; !ok {
		t.Errorf("missing DynamoDB ESM edge; want %s, got: %v", ddbEdgeID, keys(esmEdges))
	}
}

func keys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
