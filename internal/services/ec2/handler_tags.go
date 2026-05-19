package ec2

// handler_tags.go — CreateTags, DeleteTags, DescribeTags handlers.

import (
	"encoding/xml"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
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
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "ResourceId is required",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	for _, rid := range resourceIDs {
		existing, _ := h.store.getTags(r.Context(), rid)
		if existing == nil {
			existing = make(map[string]string)
		}
		for k, v := range tags {
			existing[k] = v
		}
		if aerr := h.store.putTags(r.Context(), rid, existing); aerr != nil {
			protocol.WriteXMLError(w, r, aerr)
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
			protocol.WriteXMLError(w, r, aerr)
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
	// Collect resource IDs from Filter.N.Value.M where Filter.N.Name = "resource-id"
	var filterResources []string
	for k, vals := range r.Form {
		if strings.HasPrefix(k, "Filter.") && strings.HasSuffix(k, ".Name") {
			if len(vals) > 0 && vals[0] == "resource-id" {
				prefix := k[:len(k)-len("Name")]
				for vk, vvals := range r.Form {
					if strings.HasPrefix(vk, prefix+"Value.") && len(vvals) > 0 {
						filterResources = append(filterResources, vvals[0])
					}
				}
			}
		}
	}

	items := make([]xmlTagItem, 0)
	allTags, _ := h.store.listAllTags(r.Context())
	for rid, tags := range allTags {
		if len(filterResources) > 0 && !containsStr(filterResources, rid) {
			continue
		}
		resType := inferResourceType(rid)
		for k, v := range tags {
			items = append(items, xmlTagItem{
				ResourceID:   rid,
				ResourceType: resType,
				Key:          k,
				Value:        v,
			})
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeTagsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(r.Context()),
		TagSet:    items,
	})
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

// containsStr reports whether s appears in the slice.
func containsStr(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
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
