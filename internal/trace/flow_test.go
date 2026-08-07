package trace

import (
	"testing"
	"time"
)

func TestBuildFlowGraphBasic(t *testing.T) {
	entry := Entry{
		Service:    "cloudformation",
		Operation:  "CreateStack",
		StatusCode: 200,
		Duration:   time.Second,
		RequestID:  "req-1",
		Hops: []Hop{
			{ID: "hop-1", Parent: "", Service: "sqs", Operation: "CreateQueue", CallerService: "cloudformation", ResponseStatus: 200},
			{ID: "hop-2", Parent: "", Service: "lambda", Operation: "CreateFunction", CallerService: "cloudformation", ResponseStatus: 200},
			{ID: "hop-3", Parent: "hop-2", Service: "iam", Operation: "CreateRole", CallerService: "lambda", ResponseStatus: 200},
		},
	}

	nodes, edges := BuildFlowGraph(entry, 5)

	if len(nodes) != 4 { // entry + 3 hops
		t.Fatalf("expected 4 nodes, got %d", len(nodes))
	}
	if len(edges) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(edges))
	}

	if nodes[0].ID != "entry" {
		t.Errorf("expected entry root, got %s", nodes[0].ID)
	}
	if !nodes[0].IsEntry {
		t.Error("root should have IsEntry=true")
	}
}

func TestBuildFlowGraphAggregation(t *testing.T) {
	var hops []Hop
	for i := 0; i < 10; i++ {
		hops = append(hops, Hop{
			ID:             "hop-" + string(rune('a'+i)),
			Parent:         "",
			Service:        "lambda",
			Operation:      "Invoke",
			CallerService:  "stepfunctions",
			ResponseStatus: 200,
			Noisy:          true,
		})
	}
	hops = append(hops, Hop{
		ID:             "hop-single",
		Parent:         "",
		Service:        "s3",
		Operation:      "PutObject",
		CallerService:  "lambda",
		ResponseStatus: 200,
		Noisy:          false,
	})

	entry := Entry{
		Service:   "stepfunctions",
		Operation: "StartExecution",
		Hops:      hops,
	}

	nodes, edges := BuildFlowGraph(entry, 5)

	if len(nodes) != 3 { // entry + 1 aggregate + 1 non-noisy
		t.Fatalf("expected 3 nodes (entry + aggregate + s3), got %d", len(nodes))
	}

	var aggNode *FlowNode
	for i := range nodes {
		if nodes[i].AggregateCount > 0 {
			aggNode = &nodes[i]
			break
		}
	}
	if aggNode == nil {
		t.Fatal("expected an aggregate node")
	}
	if aggNode.AggregateCount != 10 {
		t.Errorf("expected aggregate count 10, got %d", aggNode.AggregateCount)
	}
	if len(aggNode.AggregatedHops) != 10 {
		t.Errorf("expected 10 aggregated hops, got %d", len(aggNode.AggregatedHops))
	}
	if len(edges) != 2 {
		t.Errorf("expected 2 edges, got %d", len(edges))
	}
}

func TestBuildFlowGraphEmptyHops(t *testing.T) {
	entry := Entry{
		Service:   "sqs",
		Operation: "SendMessage",
	}
	nodes, edges := BuildFlowGraph(entry, 5)

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}
	if len(edges) != 0 {
		t.Fatalf("expected 0 edges, got %d", len(edges))
	}
	if !nodes[0].IsEntry {
		t.Error("expected IsEntry root node")
	}
}

func TestBuildFlowGraphBelowThreshold(t *testing.T) {
	var hops []Hop
	for i := 0; i < 3; i++ {
		hops = append(hops, Hop{
			ID:             "hop-" + string(rune('a'+i)),
			Parent:         "",
			Service:        "lambda",
			Operation:      "Invoke",
			CallerService:  "stepfunctions",
			ResponseStatus: 200,
			Noisy:          true,
		})
	}

	entry := Entry{
		Service: "stepfunctions",
		Hops:    hops,
	}

	nodes, _ := BuildFlowGraph(entry, 5)
	if len(nodes) != 4 { // entry + 3 hops (below threshold, not aggregated)
		t.Fatalf("expected 4 nodes, got %d", len(nodes))
	}
}

func TestFlowNodeFields(t *testing.T) {
	entry := Entry{
		Service:    "cloudformation",
		Operation:  "CreateStack",
		StatusCode: 201,
		Duration:   time.Millisecond * 150,
		Hops: []Hop{
			{
				ID:             "hop-1",
				Service:        "sqs",
				Operation:      "CreateQueue",
				CallerService:  "cloudformation",
				ResponseStatus: 200,
				Duration:       time.Millisecond * 50,
				TargetURI:      "POST / (X-Amz-Target: AmazonSQS.CreateQueue)",
				Error:          "",
			},
		},
	}

	nodes, _ := BuildFlowGraph(entry, 5)
	hop := nodes[1]

	if hop.Duration != 50_000_000 {
		t.Errorf("expected 50ms duration, got %d", hop.Duration)
	}
	if hop.TargetURI != "POST / (X-Amz-Target: AmazonSQS.CreateQueue)" {
		t.Errorf("unexpected target URI: %s", hop.TargetURI)
	}
	if hop.ResponseStatus != 200 {
		t.Errorf("expected status 200, got %d", hop.ResponseStatus)
	}
}
