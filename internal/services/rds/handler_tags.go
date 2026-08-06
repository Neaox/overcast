package rds

import (
	"encoding/xml"
	"fmt"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
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

type rdsXMLAddTagsResult struct {
	TagList rdsXMLTagList `xml:"TagList"`
}

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

func (h *Handler) AddTagsToResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	tags, aerr := h.store.getTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("Tags.Tag.%d.Key", i))
		if key == "" {
			break
		}
		tags[key] = r.FormValue(fmt.Sprintf("Tags.Tag.%d.Value", i))
	}
	if aerr := serviceutil.ValidateTags(rdsTagCfg, tags); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.setTags(r.Context(), arn, tags); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	items := make([]rdsXMLTag, 0, len(tags))
	for k, v := range tags {
		items = append(items, rdsXMLTag{Key: k, Value: v})
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &rdsXMLAddTagsResponse{
		Xmlns:            rdsXMLNS,
		Result:           rdsXMLAddTagsResult{TagList: rdsXMLTagList{Items: items}},
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}

func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	arn := r.FormValue("ResourceName")
	if arn == "" {
		protocol.WriteQueryXMLError(w, r, errInvalidParameterValue("ResourceName is required"))
		return
	}
	tags, aerr := h.store.getTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	items := make([]rdsXMLTag, 0, len(tags))
	for k, v := range tags {
		items = append(items, rdsXMLTag{Key: k, Value: v})
	}
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
	tags, aerr := h.store.getTags(r.Context(), arn)
	if aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	for i := 1; ; i++ {
		key := r.FormValue(fmt.Sprintf("TagKeys.member.%d", i))
		if key == "" {
			break
		}
		delete(tags, key)
	}
	if aerr := h.store.setTags(r.Context(), arn, tags); aerr != nil {
		protocol.WriteQueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &rdsXMLRemoveTagsResponse{
		Xmlns:            rdsXMLNS,
		ResponseMetadata: protocol.QueryResponseMetadata(r),
	})
}
