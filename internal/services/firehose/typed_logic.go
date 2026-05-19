package firehose

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
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
