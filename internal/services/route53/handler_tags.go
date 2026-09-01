package route53

import (
	"encoding/xml"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// REST-XML handlers for the shared tag operations (hosted zones and health
// checks use the same /tags/{ResourceType}/{ResourceId} surface).

type xmlResourceTagSet struct {
	ResourceType string `xml:"ResourceType"`
	ResourceId   string `xml:"ResourceId"`
	Tags         []Tag  `xml:"Tags>Tag"`
}

// ─── ChangeTagsForResource ────────────────────────────────────────────────────

type xmlChangeTagsForResourceRequest struct {
	XMLName       xml.Name `xml:"ChangeTagsForResourceRequest"`
	AddTags       []Tag    `xml:"AddTags>Tag"`
	RemoveTagKeys []string `xml:"RemoveTagKeys>Key"`
}

type xmlChangeTagsForResourceResponse struct {
	XMLName xml.Name `xml:"ChangeTagsForResourceResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
}

func (s *Service) changeTagsForResource(w http.ResponseWriter, r *http.Request) {
	var req xmlChangeTagsForResourceRequest
	if !decodeXMLBody(w, r, &req) {
		return
	}
	resourceType, resourceID := chi.URLParam(r, "resourceType"), chi.URLParam(r, "resourceId")
	if aerr := s.changeTagsCore(r.Context(), resourceType, resourceID, req.AddTags, req.RemoveTagKeys); aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlChangeTagsForResourceResponse{Xmlns: r53XMLNS})
}

// ─── ListTagsForResource ──────────────────────────────────────────────────────

type xmlListTagsForResourceResponse struct {
	XMLName        xml.Name          `xml:"ListTagsForResourceResponse"`
	Xmlns          string            `xml:"xmlns,attr"`
	ResourceTagSet xmlResourceTagSet `xml:"ResourceTagSet"`
}

func (s *Service) listTagsForResource(w http.ResponseWriter, r *http.Request) {
	resourceType, resourceID := chi.URLParam(r, "resourceType"), chi.URLParam(r, "resourceId")
	tags, aerr := s.listTagsCore(r.Context(), resourceType, resourceID)
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlListTagsForResourceResponse{
		Xmlns: r53XMLNS,
		ResourceTagSet: xmlResourceTagSet{
			ResourceType: resourceType,
			ResourceId:   resourceID,
			Tags:         tags,
		},
	})
}

// ─── ListTagsForResources ─────────────────────────────────────────────────────

type xmlListTagsForResourcesRequest struct {
	XMLName     xml.Name `xml:"ListTagsForResourcesRequest"`
	ResourceIds []string `xml:"ResourceIds>ResourceId"`
}

type xmlListTagsForResourcesResponse struct {
	XMLName         xml.Name            `xml:"ListTagsForResourcesResponse"`
	Xmlns           string              `xml:"xmlns,attr"`
	ResourceTagSets []xmlResourceTagSet `xml:"ResourceTagSets>ResourceTagSet"`
}

func (s *Service) listTagsForResources(w http.ResponseWriter, r *http.Request) {
	var req xmlListTagsForResourcesRequest
	if !decodeXMLBody(w, r, &req) {
		return
	}
	if len(req.ResourceIds) == 0 {
		writeError(w, r, errInvalidInput("ResourceIds is required"))
		return
	}
	resourceType := chi.URLParam(r, "resourceType")
	sets := make([]xmlResourceTagSet, 0, len(req.ResourceIds))
	for _, id := range req.ResourceIds {
		tags, aerr := s.listTagsCore(r.Context(), resourceType, id)
		if aerr != nil {
			writeError(w, r, aerr)
			return
		}
		sets = append(sets, xmlResourceTagSet{ResourceType: resourceType, ResourceId: id, Tags: tags})
	}
	protocol.WriteXML(w, r, http.StatusOK, &xmlListTagsForResourcesResponse{
		Xmlns:           r53XMLNS,
		ResourceTagSets: sets,
	})
}
