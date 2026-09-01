package rds

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// rdsTagCfg tunes shared tag validation to return RDS-specific error codes.
var rdsTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterValue",
	InvalidCode:     "InvalidParameterValue",
	ExceededMessage: "Too many tags. Maximum allowed: 50.",
}

// ── Tagging XML types ─────────────────────────────────────────────────────────

type rdsXMLTag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type rdsXMLTagList struct {
	Items []rdsXMLTag `xml:"Tag"`
}

type rdsXMLAddTagsResponse struct {
	XMLName          xml.Name                  `xml:"AddTagsToResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           rdsXMLAddTagsResult       `xml:"AddTagsToResourceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type rdsXMLAddTagsResult struct{}

type rdsXMLListTagsResponse struct {
	XMLName          xml.Name                  `xml:"ListTagsForResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           rdsXMLListTagsResult      `xml:"ListTagsForResourceResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type rdsXMLListTagsResult struct {
	TagList rdsXMLTagList `xml:"TagList"`
}

type rdsXMLRemoveTagsResponse struct {
	XMLName          xml.Name                  `xml:"RemoveTagsFromResourceResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

// ── Tagging handlers ──────────────────────────────────────────────────────────

// parseIndexedTags collects 1-indexed Key/Value pairs under the given wire
// prefix, e.g. "Tags.Tag" → Tags.Tag.1.Key / Tags.Tag.1.Value.
func parseIndexedTags(r *http.Request, prefix string) map[string]string {
	tags := map[string]string{}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("%s.%d.Key", prefix, i))
		val := r.FormValue(fmt.Sprintf("%s.%d.Value", prefix, i))
		if key == "" && val == "" {
			break
		}
		tags[key] = val
	}
	return tags
}

// requireTaggableResource resolves an RDS resource ARN to a stored resource
// and returns the AWS-modeled error when it does not exist. Real RDS refuses
// tag operations on unknown resources instead of minting a tag store for any
// string.
func (h *Handler) requireTaggableResource(ctx context.Context, arn string) *protocol.AWSError {
	// arn:aws:rds:<region>:<account>:<type>:<name>
	parts := strings.SplitN(arn, ":", 7)
	if len(parts) != 7 || parts[0] != "arn" || parts[2] != "rds" {
		return errInvalidParameterValue(fmt.Sprintf("Invalid resource name: %s", arn))
	}
	resourceType, name := parts[5], parts[6]
	switch resourceType {
	case "db":
		_, aerr := h.store.getDBInstance(ctx, name)
		return aerr
	case "cluster":
		_, aerr := h.store.getDBCluster(ctx, name)
		return aerr
	case "subgrp":
		_, aerr := h.store.getDBSubnetGroup(ctx, name)
		return aerr
	case "pg":
		_, aerr := h.store.getDBParameterGroup(ctx, name)
		return aerr
	default:
		// Resource types the emulator does not model (option groups,
		// snapshots, …) cannot exist here, so their ARNs never match a
		// resource — the same InvalidParameterValue real RDS answers for an
		// ARN that names nothing in this region.
		return errInvalidParameterValue(fmt.Sprintf("Invalid resource name: %s", arn))
	}
}

func (h *Handler) AddTagsToResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	if aerr := h.requireTaggableResource(r.Context(), arn); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	// The RDS model gives TagList's member the locationName "Tag", so every
	// SDK and the CLI send Tags.Tag.N.Key / Tags.Tag.N.Value. Keep the
	// member-indexed form as a fallback for hand-rolled clients.
	incoming := parseIndexedTags(r, "Tags.Tag")
	if len(incoming) == 0 {
		incoming = parseIndexedTags(r, "Tags.member")
	}
	if _, aerr := serviceutil.ApplyStoreTags(r.Context(), h.store.tags(), arn, incoming, rdsTagCfg); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &rdsXMLAddTagsResponse{
		Xmlns:            rdsXMLNS,
		Result:           rdsXMLAddTagsResult{},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	if aerr := h.requireTaggableResource(r.Context(), arn); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	tags, aerr := h.store.tags().Load(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	items := serviceutil.TagElements(tags, func(k, v string) rdsXMLTag {
		return rdsXMLTag{Key: k, Value: v}
	})
	protocol.WriteQueryXML(w, r, http.StatusOK, &rdsXMLListTagsResponse{
		Xmlns:            rdsXMLNS,
		Result:           rdsXMLListTagsResult{TagList: rdsXMLTagList{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) RemoveTagsFromResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	if aerr := h.requireTaggableResource(r.Context(), arn); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	// TagKeys is a plain KeyList with no locationName override, so clients
	// send the standard Query form TagKeys.member.N.
	var keys []string
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if key == "" {
			break
		}
		keys = append(keys, key)
	}
	if _, aerr := serviceutil.RemoveStoreTags(r.Context(), h.store.tags(), arn, keys); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &rdsXMLRemoveTagsResponse{
		Xmlns:            rdsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}
