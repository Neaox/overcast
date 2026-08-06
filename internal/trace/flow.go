package trace

import (
	"strconv"
	"strings"
)

// FlowNode is a single node in a request flow graph, derived from a Hop or
// the top-level Entry.
type FlowNode struct {
	ID             string `json:"id"`
	Parent         string `json:"parent,omitempty"`
	Service        string `json:"service"`
	Operation      string `json:"operation"`
	ResponseStatus int    `json:"responseStatus"`
	Duration       int64  `json:"duration"`
	RequestBody    []byte `json:"requestBody,omitempty"`
	ResponseBody   []byte `json:"responseBody,omitempty"`
	Error          string `json:"error,omitempty"`
	Noisy          bool   `json:"noisy,omitempty"`
	TargetURI      string `json:"targetUri,omitempty"`
	AggregateCount int    `json:"aggregateCount,omitempty"`
	AggregatedHops []Hop  `json:"aggregatedHops,omitempty"`
	IsEntry        bool   `json:"isEntry"`
}

// FlowEdge is a directed edge between two FlowNodes.
type FlowEdge struct {
	ID     string `json:"id"`
	Source string `json:"source"`
	Target string `json:"target"`
	Label  string `json:"label,omitempty"`
}

// BuildFlowGraph converts an Entry's hops into a tree of FlowNodes and
// FlowEdges. The entry itself becomes the root node. Hops are arranged by
// ID/Parent references. When aggregateThreshold is > 0, hops marked Noisy that
// share the same (Service, Operation, CallerService) and exceed the threshold
// are collapsed into a single aggregate node.
func BuildFlowGraph(entry Entry, aggregateThreshold int) (nodes []FlowNode, edges []FlowEdge) {
	if aggregateThreshold <= 0 {
		aggregateThreshold = 5
	}

	root := FlowNode{
		ID:             "entry",
		Service:        entry.Service,
		Operation:      entry.Operation,
		ResponseStatus: entry.StatusCode,
		Duration:       int64(entry.Duration),
		IsEntry:        true,
	}
	nodes = append(nodes, root)

	agg := aggregateHops(entry.Hops, aggregateThreshold)

	for _, h := range agg.kept {
		nid := h.ID
		parent := h.Parent
		if parent == "" {
			parent = "entry"
		}
		node := FlowNode{
			ID:             nid,
			Parent:         parent,
			Service:        h.Service,
			Operation:      h.Operation,
			ResponseStatus: h.ResponseStatus,
			Duration:       int64(h.Duration),
			RequestBody:    h.RequestBody,
			ResponseBody:   h.ResponseBody,
			Error:          h.Error,
			Noisy:          h.Noisy,
			TargetURI:      h.TargetURI,
		}
		nodes = append(nodes, node)
		edges = append(edges, FlowEdge{
			ID:     parent + "\u2192" + nid,
			Source: parent,
			Target: nid,
			Label:  h.Operation,
		})
	}

	for _, a := range agg.aggregates {
		parent := "entry"
		node := FlowNode{
			ID:             a.id,
			Parent:         parent,
			Service:        a.service,
			Operation:      a.operation,
			AggregateCount: a.count,
			AggregatedHops: a.hops,
			Noisy:          true,
		}
		nodes = append(nodes, node)
		edges = append(edges, FlowEdge{
			ID:     parent + "\u2192" + a.id,
			Source: parent,
			Target: a.id,
			Label:  a.operation + " \u00d7" + strconv.Itoa(a.count),
		})
	}

	return
}

type hopAggregation struct {
	kept       []Hop
	aggregates []aggregateGroup
}

type aggregateGroup struct {
	id        string
	service   string
	operation string
	hops      []Hop
	count     int
}

func aggregateHops(hops []Hop, threshold int) hopAggregation {
	groups := make(map[string][]Hop)
	var kept []Hop

	for _, h := range hops {
		if h.Noisy {
			key := h.CallerService + "/" + h.Service + "/" + h.Operation
			groups[key] = append(groups[key], h)
		} else {
			kept = append(kept, h)
		}
	}

	var aggs []aggregateGroup
	for _, group := range groups {
		if len(group) <= threshold {
			kept = append(kept, group...)
			continue
		}
		parts := strings.SplitN(group[0].Service+"/"+group[0].Operation, "/", 2)
		svc := group[0].Service
		op := group[0].Operation
		_ = parts
		aggs = append(aggs, aggregateGroup{
			id:        "agg-" + svc + "-" + op,
			service:   svc,
			operation: op,
			hops:      group,
			count:     len(group),
		})
	}

	return hopAggregation{kept: kept, aggregates: aggs}
}
