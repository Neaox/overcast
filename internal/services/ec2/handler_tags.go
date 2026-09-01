package ec2

// handler_tags.go — CreateTags, DeleteTags, DescribeTags handlers.

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// ── CreateTags ──────────────────────────────────────────────────────────────

type xmlCreateTagsResponse struct {
	XMLName   xml.Name `xml:"CreateTagsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// CreateTags adds or overwrites tags on one or more resources.
func (h *Handler) CreateTags(w http.ResponseWriter, r *http.Request) {
	resourceIDs := collectFormValues(r, "ResourceId.")
	tags := collectFormTags(r, "Tag.")
	if len(resourceIDs) == 0 {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "ResourceId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	for _, rid := range resourceIDs {
		if aerr := h.putResourceTags(r.Context(), rid, sortedTags(tags)); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateTagsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── DeleteTags ──────────────────────────────────────────────────────────────

type xmlDeleteTagsResponse struct {
	XMLName   xml.Name `xml:"DeleteTagsResponse"`
	Xmlns     string   `xml:"xmlns,attr"`
	RequestID string   `xml:"requestId"`
	Return    bool     `xml:"return"`
}

// DeleteTags removes tags from one or more resources.
func (h *Handler) DeleteTags(w http.ResponseWriter, r *http.Request) {
	resourceIDs := collectFormValues(r, "ResourceId.")
	tags := collectFormTags(r, "Tag.")

	for _, rid := range resourceIDs {
		existing, _ := h.store.getTags(r.Context(), rid)
		if existing == nil {
			continue
		}
		for k := range tags {
			delete(existing, k)
		}
		if aerr := h.store.putTags(r.Context(), rid, existing); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteTagsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		Return:    true,
	})
}

// ── DescribeTags ────────────────────────────────────────────────────────────

type xmlDescribeTagsResponse struct {
	XMLName   xml.Name     `xml:"DescribeTagsResponse"`
	Xmlns     string       `xml:"xmlns,attr"`
	RequestID string       `xml:"requestId"`
	TagSet    []xmlTagItem `xml:"tagSet>item"`
}

type xmlTagItem struct {
	ResourceID   string `xml:"resourceId"`
	ResourceType string `xml:"resourceType"`
	Key          string `xml:"key"`
	Value        string `xml:"value"`
}

// DescribeTags lists tags for resources.
func (h *Handler) DescribeTags(w http.ResponseWriter, r *http.Request) {
	resp, aerr := h.describeTags(r.Context(), unselectedQuery(r))
	writeDescribe(w, r, resp, aerr)
}

func (h *Handler) describeTags(ctx context.Context, q describeQuery) (*xmlDescribeTagsResponse, *protocol.AWSError) {
	filters, aerr := tagItemFilters.parse(q.filters)
	if aerr != nil {
		return nil, aerr
	}

	allTags, _ := h.store.listAllTags(ctx)
	items := tagItems(tagIndex(allTags),
		func(rid string, tag Tag) xmlTagItem {
			return xmlTagItem{
				ResourceID:   rid,
				ResourceType: inferResourceType(rid),
				Key:          tag.Key,
				Value:        tag.Value,
			}
		},
		filters.matches)

	return &xmlDescribeTagsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		TagSet:    items,
	}, nil
}

// ── Helpers ─────────────────────────────────────────────────────────────────

// collectFormValues collects numbered form values like "ResourceId.1", "ResourceId.2".
func collectFormValues(r *http.Request, prefix string) []string {
	var vals []string
	for k, v := range r.Form {
		if strings.HasPrefix(k, prefix) && len(v) > 0 {
			vals = append(vals, v[0])
		}
	}
	return vals
}

// collectFormTags parses Tag.N.Key / Tag.N.Value pairs from form data.
func collectFormTags(r *http.Request, prefix string) map[string]string {
	tags := make(map[string]string)
	// Build a map of N -> (key, value) from Tag.N.Key and Tag.N.Value
	keys := make(map[string]string) // N -> key
	vals := make(map[string]string) // N -> value
	for k, v := range r.Form {
		if !strings.HasPrefix(k, prefix) || len(v) == 0 {
			continue
		}
		rest := k[len(prefix):]
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 {
			continue
		}
		n, field := parts[0], parts[1]
		switch field {
		case "Key":
			keys[n] = v[0]
		case "Value":
			vals[n] = v[0]
		}
	}
	for n, key := range keys {
		tags[key] = vals[n]
	}
	return tags
}

// inferResourceType guesses the EC2 resource type from an ID prefix.
func inferResourceType(id string) string {
	switch {
	case strings.HasPrefix(id, "vpc-"):
		return "vpc"
	case strings.HasPrefix(id, "subnet-"):
		return "subnet"
	case strings.HasPrefix(id, "i-"):
		return "instance"
	case strings.HasPrefix(id, "sg-"):
		return "security-group"
	case strings.HasPrefix(id, "igw-"):
		return "internet-gateway"
	case strings.HasPrefix(id, "rtb-"):
		return "route-table"
	case strings.HasPrefix(id, "nat-"):
		return "natgateway"
	case strings.HasPrefix(id, "eipalloc-"):
		return "elastic-ip"
	case strings.HasPrefix(id, "eni-"):
		return "network-interface"
	default:
		return "unknown"
	}
}
