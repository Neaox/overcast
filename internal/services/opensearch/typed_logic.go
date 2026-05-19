package opensearch

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
)

// --- CreateDomain ---

type createDomainRequest struct {
	DomainName    string `json:"DomainName" cbor:"DomainName"`
	EngineVersion string `json:"EngineVersion" cbor:"EngineVersion"`
}

type createDomainResponse struct {
	DomainStatus *DomainStatus `json:"DomainStatus" cbor:"DomainStatus"`
}

func (s *Service) createDomainTyped(ctx context.Context, req *createDomainRequest) (*createDomainResponse, *protocol.AWSError) {
	if req.DomainName == "" {
		return nil, &protocol.AWSError{
			Code: "ValidationException", Message: "DomainName is required",
			HTTPStatus: 400,
		}
	}
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	arn := fmt.Sprintf("arn:aws:es:%s:%s:domain/%s", region, s.cfg.AccountID, req.DomainName)
	domainID := fmt.Sprintf("%s/%s", s.cfg.AccountID, req.DomainName)

	ev := req.EngineVersion
	if ev == "" {
		ev = "OpenSearch_2.11"
	}

	domain := &DomainStatus{
		DomainId:      domainID,
		DomainName:    req.DomainName,
		ARN:           arn,
		EngineVersion: ev,
		Created:       true,
		Deleted:       false,
		Processing:    false,
		Endpoint:      fmt.Sprintf("search-%s.%s.es.%s", req.DomainName, region, s.cfg.ExternalHostname()),
	}
	if err := s.store.putDomain(ctx, domain); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createDomainResponse{DomainStatus: domain}, nil
}

// --- DescribeDomain ---

type describeDomainRequest struct {
	DomainName string `json:"DomainName" cbor:"DomainName"`
}

type describeDomainResponse struct {
	DomainStatus *DomainStatus `json:"DomainStatus" cbor:"DomainStatus"`
}

func (s *Service) describeDomainTyped(ctx context.Context, req *describeDomainRequest) (*describeDomainResponse, *protocol.AWSError) {
	domain, found := s.store.getDomain(ctx, req.DomainName)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Domain %s not found", req.DomainName),
			HTTPStatus: 404,
		}
	}
	return &describeDomainResponse{DomainStatus: domain}, nil
}

// --- DeleteDomain ---

type deleteDomainRequest struct {
	DomainName string `json:"DomainName" cbor:"DomainName"`
}

type deleteDomainResponse struct {
	DomainStatus *DomainStatus `json:"DomainStatus" cbor:"DomainStatus"`
}

func (s *Service) deleteDomainTyped(ctx context.Context, req *deleteDomainRequest) (*deleteDomainResponse, *protocol.AWSError) {
	domain, found := s.store.getDomain(ctx, req.DomainName)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Domain %s not found", req.DomainName),
			HTTPStatus: 404,
		}
	}
	domain.Deleted = true
	if err := s.store.deleteDomain(ctx, req.DomainName); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &deleteDomainResponse{DomainStatus: domain}, nil
}

// --- ListDomainNames ---

type listDomainNamesRequest struct{}

type listDomainNamesResponse struct {
	DomainNames []domainNameItem `json:"DomainNames" cbor:"DomainNames"`
}

type domainNameItem struct {
	DomainName string `json:"DomainName" cbor:"DomainName"`
	EngineType string `json:"EngineType" cbor:"EngineType"`
}

func (s *Service) listDomainNamesTyped(ctx context.Context, _ *listDomainNamesRequest) (*listDomainNamesResponse, *protocol.AWSError) {
	domains, err := s.store.listDomains(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	names := make([]domainNameItem, 0, len(domains))
	for _, d := range domains {
		names = append(names, domainNameItem{
			DomainName: d.DomainName,
			EngineType: "OpenSearch",
		})
	}
	return &listDomainNamesResponse{DomainNames: names}, nil
}

// --- DescribeDomains ---

type describeDomainsRequest struct {
	DomainNames []string `json:"DomainNames" cbor:"DomainNames"`
}

type describeDomainsResponse struct {
	DomainStatusList []*DomainStatus `json:"DomainStatusList" cbor:"DomainStatusList"`
}

func (s *Service) describeDomainsTyped(ctx context.Context, req *describeDomainsRequest) (*describeDomainsResponse, *protocol.AWSError) {
	var results []*DomainStatus
	for _, name := range req.DomainNames {
		if d, found := s.store.getDomain(ctx, name); found {
			results = append(results, d)
		}
	}
	return &describeDomainsResponse{DomainStatusList: results}, nil
}

// --- AddTags ---

type addTagsRequest struct {
	ARN     string    `json:"ARN" cbor:"ARN"`
	TagList []tagItem `json:"TagList" cbor:"TagList"`
}

type tagItem struct {
	Key   string `json:"Key" cbor:"Key"`
	Value string `json:"Value" cbor:"Value"`
}

func (s *Service) addTagsTyped(ctx context.Context, req *addTagsRequest) (*struct{}, *protocol.AWSError) {
	tags, err := s.store.getTags(ctx, req.ARN)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	for _, tag := range req.TagList {
		tags[tag.Key] = tag.Value
	}
	if err := s.store.setTags(ctx, req.ARN, tags); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

// --- ListTags ---

type listTagsRequest struct {
	ARN string `json:"ARN" cbor:"ARN"`
}

type listTagsResponse struct {
	TagList []tagItem `json:"TagList" cbor:"TagList"`
}

func (s *Service) listTagsTyped(ctx context.Context, req *listTagsRequest) (*listTagsResponse, *protocol.AWSError) {
	tags, err := s.store.getTags(ctx, req.ARN)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	tagList := make([]tagItem, 0, len(tags))
	for k, v := range tags {
		tagList = append(tagList, tagItem{Key: k, Value: v})
	}
	return &listTagsResponse{TagList: tagList}, nil
}

// --- RemoveTags ---

type removeTagsRequest struct {
	ARN     string   `json:"ARN" cbor:"ARN"`
	TagKeys []string `json:"TagKeys" cbor:"TagKeys"`
}

func (s *Service) removeTagsTyped(ctx context.Context, req *removeTagsRequest) (*struct{}, *protocol.AWSError) {
	tags, err := s.store.getTags(ctx, req.ARN)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	for _, k := range req.TagKeys {
		delete(tags, k)
	}
	if err := s.store.setTags(ctx, req.ARN, tags); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}
