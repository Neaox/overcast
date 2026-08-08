package firehose

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

type createDeliveryStreamReq struct {
	DeliveryStreamName string `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
	DeliveryStreamType string `json:"DeliveryStreamType" cbor:"DeliveryStreamType"`
}

type describeDeliveryStreamReq struct {
	DeliveryStreamName string `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
}

type deleteDeliveryStreamReq struct {
	DeliveryStreamName string `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
}

type putRecordReq struct {
	DeliveryStreamName string `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
}

type putRecordBatchReq struct {
	DeliveryStreamName string          `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
	Records            json.RawMessage `json:"Records" cbor:"Records"`
}

type createDeliveryStreamResp struct {
	DeliveryStreamARN string `json:"DeliveryStreamARN" cbor:"DeliveryStreamARN"`
}

type describeDeliveryStreamDescription struct {
	DeliveryStreamName   string `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
	DeliveryStreamARN    string `json:"DeliveryStreamARN" cbor:"DeliveryStreamARN"`
	DeliveryStreamStatus string `json:"DeliveryStreamStatus" cbor:"DeliveryStreamStatus"`
	DeliveryStreamType   string `json:"DeliveryStreamType" cbor:"DeliveryStreamType"`
	HasMoreDestinations  bool   `json:"HasMoreDestinations" cbor:"HasMoreDestinations"`
	Destinations         []any  `json:"Destinations" cbor:"Destinations"`
}

type describeDeliveryStreamResp struct {
	DeliveryStreamDescription describeDeliveryStreamDescription `json:"DeliveryStreamDescription" cbor:"DeliveryStreamDescription"`
}

type listDeliveryStreamsResp struct {
	DeliveryStreamNames    []string `json:"DeliveryStreamNames" cbor:"DeliveryStreamNames"`
	HasMoreDeliveryStreams bool     `json:"HasMoreDeliveryStreams" cbor:"HasMoreDeliveryStreams"`
}

type putRecordResp struct {
	RecordId  string `json:"RecordId" cbor:"RecordId"`
	Encrypted bool   `json:"Encrypted" cbor:"Encrypted"`
}

type putRecordBatchResult struct {
	RecordId string `json:"RecordId" cbor:"RecordId"`
}

type putRecordBatchResp struct {
	FailedPutCount   int                    `json:"FailedPutCount" cbor:"FailedPutCount"`
	Encrypted        bool                   `json:"Encrypted" cbor:"Encrypted"`
	RequestResponses []putRecordBatchResult `json:"RequestResponses" cbor:"RequestResponses"`
}

func (s *Service) createDeliveryStreamTyped(ctx context.Context, req *createDeliveryStreamReq) (*createDeliveryStreamResp, *protocol.AWSError) {
	if req.DeliveryStreamName == "" {
		return nil, &protocol.AWSError{
			Code: "InvalidArgumentException", Message: "DeliveryStreamName is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	region := middleware.RegionFromContext(ctx, s.cfg.Region)
	arn := fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/%s", region, s.cfg.AccountID, req.DeliveryStreamName)
	dsType := req.DeliveryStreamType
	if dsType == "" {
		dsType = "DirectPut"
	}
	ds := &DeliveryStream{
		DeliveryStreamName:   req.DeliveryStreamName,
		DeliveryStreamARN:    arn,
		DeliveryStreamStatus: "ACTIVE",
		DeliveryStreamType:   dsType,
		Tags:                 make(map[string]string),
	}
	if err := s.store.putStream(ctx, ds); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &createDeliveryStreamResp{DeliveryStreamARN: arn}, nil
}

func (s *Service) describeDeliveryStreamTyped(ctx context.Context, req *describeDeliveryStreamReq) (*describeDeliveryStreamResp, *protocol.AWSError) {
	ds, found := s.store.getStream(ctx, req.DeliveryStreamName)
	if !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	return &describeDeliveryStreamResp{
		DeliveryStreamDescription: describeDeliveryStreamDescription{
			DeliveryStreamName:   ds.DeliveryStreamName,
			DeliveryStreamARN:    ds.DeliveryStreamARN,
			DeliveryStreamStatus: ds.DeliveryStreamStatus,
			DeliveryStreamType:   ds.DeliveryStreamType,
			HasMoreDestinations:  false,
			Destinations:         []any{},
		},
	}, nil
}

func (s *Service) listDeliveryStreamsTyped(ctx context.Context, _ *struct{}) (*listDeliveryStreamsResp, *protocol.AWSError) {
	streams, err := s.store.listStreams(ctx)
	if err != nil {
		return nil, protocol.ErrInternalError
	}
	names := make([]string, 0, len(streams))
	for _, ds := range streams {
		names = append(names, ds.DeliveryStreamName)
	}
	return &listDeliveryStreamsResp{
		DeliveryStreamNames:    names,
		HasMoreDeliveryStreams: false,
	}, nil
}

func (s *Service) deleteDeliveryStreamTyped(ctx context.Context, req *deleteDeliveryStreamReq) (*struct{}, *protocol.AWSError) {
	if _, found := s.store.getStream(ctx, req.DeliveryStreamName); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	if err := s.store.deleteStream(ctx, req.DeliveryStreamName); err != nil {
		return nil, protocol.ErrInternalError
	}
	return &struct{}{}, nil
}

func (s *Service) putRecordTyped(ctx context.Context, req *putRecordReq) (*putRecordResp, *protocol.AWSError) {
	if _, found := s.store.getStream(ctx, req.DeliveryStreamName); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	return &putRecordResp{RecordId: uuid.NewString(), Encrypted: false}, nil
}

func (s *Service) putRecordBatchTyped(ctx context.Context, req *putRecordBatchReq) (*putRecordBatchResp, *protocol.AWSError) {
	if _, found := s.store.getStream(ctx, req.DeliveryStreamName); !found {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    fmt.Sprintf("Delivery stream %s not found", req.DeliveryStreamName),
			HTTPStatus: http.StatusNotFound,
		}
	}
	var records []json.RawMessage
	_ = json.Unmarshal(req.Records, &records)
	results := make([]putRecordBatchResult, 0, len(records))
	for range records {
		results = append(results, putRecordBatchResult{RecordId: uuid.NewString()})
	}
	return &putRecordBatchResp{
		FailedPutCount:   0,
		Encrypted:        false,
		RequestResponses: results,
	}, nil
}

// ─── Tag operations ──────────────────────────────────────────────────────────

// Real Firehose sends Tags as a LIST of {Key,Value} structs, never a map.
type tagDeliveryStreamReq struct {
	DeliveryStreamName string        `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
	Tags               []firehoseTag `json:"Tags" cbor:"Tags"`
}

type untagDeliveryStreamReq struct {
	DeliveryStreamName string   `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
	TagKeys            []string `json:"TagKeys" cbor:"TagKeys"`
}

type listTagsForDeliveryStreamReq struct {
	DeliveryStreamName string `json:"DeliveryStreamName" cbor:"DeliveryStreamName"`
}

type listTagsForDeliveryStreamResp struct {
	Tags        []firehoseTag `json:"Tags" cbor:"Tags"`
	HasMoreTags bool          `json:"HasMoreTags" cbor:"HasMoreTags"`
}

type firehoseTag struct {
	Key   string `json:"Key" cbor:"Key"`
	Value string `json:"Value" cbor:"Value"`
}

func (s *Service) tagDeliveryStreamTyped(ctx context.Context, req *tagDeliveryStreamReq) (*struct{}, *protocol.AWSError) {
	incoming := make(map[string]string, len(req.Tags))
	for _, t := range req.Tags {
		incoming[t.Key] = t.Value
	}
	if aerr := serviceutil.ApplyInlineTags(ctx, req.DeliveryStreamName, incoming, firehoseTagCfg,
		func(ctx context.Context, name string) (*DeliveryStream, *protocol.AWSError) {
			ds, found := s.store.getStream(ctx, name)
			if !found {
				return nil, &protocol.AWSError{
					Code: "ResourceNotFoundException", Message: fmt.Sprintf("Delivery stream %s not found", name),
					HTTPStatus: http.StatusNotFound,
				}
			}
			return ds, nil
		},
		func(ctx context.Context, ds *DeliveryStream) *protocol.AWSError {
			if err := s.store.putStream(ctx, ds); err != nil {
				return protocol.ErrInternalError
			}
			return nil
		},
	); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (s *Service) untagDeliveryStreamTyped(ctx context.Context, req *untagDeliveryStreamReq) (*struct{}, *protocol.AWSError) {
	if aerr := serviceutil.RemoveInlineTags(ctx, req.DeliveryStreamName, req.TagKeys,
		func(ctx context.Context, name string) (*DeliveryStream, *protocol.AWSError) {
			ds, found := s.store.getStream(ctx, name)
			if !found {
				return nil, &protocol.AWSError{
					Code: "ResourceNotFoundException", Message: fmt.Sprintf("Delivery stream %s not found", name),
					HTTPStatus: http.StatusNotFound,
				}
			}
			return ds, nil
		},
		func(ctx context.Context, ds *DeliveryStream) *protocol.AWSError {
			if err := s.store.putStream(ctx, ds); err != nil {
				return protocol.ErrInternalError
			}
			return nil
		},
	); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (s *Service) listTagsForDeliveryStreamTyped(ctx context.Context, req *listTagsForDeliveryStreamReq) (*listTagsForDeliveryStreamResp, *protocol.AWSError) {
	tags, aerr := serviceutil.ListInlineTags(ctx, req.DeliveryStreamName,
		func(ctx context.Context, name string) (*DeliveryStream, *protocol.AWSError) {
			ds, found := s.store.getStream(ctx, name)
			if !found {
				return nil, &protocol.AWSError{
					Code: "ResourceNotFoundException", Message: fmt.Sprintf("Delivery stream %s not found", name),
					HTTPStatus: http.StatusNotFound,
				}
			}
			return ds, nil
		},
	)
	if aerr != nil {
		return nil, aerr
	}
	tagList := make([]firehoseTag, 0, len(tags))
	for k, v := range tags {
		tagList = append(tagList, firehoseTag{Key: k, Value: v})
	}
	return &listTagsForDeliveryStreamResp{Tags: tagList, HasMoreTags: false}, nil
}
