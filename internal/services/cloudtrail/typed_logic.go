package cloudtrail

import (
	"context"
	"net/http"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
)

type createTrailInput struct {
	Name                       string `json:"Name"`
	S3BucketName               string `json:"S3BucketName"`
	S3KeyPrefix                string `json:"S3KeyPrefix"`
	IncludeGlobalServiceEvents bool   `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         bool   `json:"IsMultiRegionTrail"`
	EnableLogFileValidation    bool   `json:"EnableLogFileValidation"`
	CloudWatchLogsLogGroupArn  string `json:"CloudWatchLogsLogGroupArn"`
	CloudWatchLogsRoleArn      string `json:"CloudWatchLogsRoleArn"`
	KmsKeyId                   string `json:"KmsKeyId"`
	IsOrganizationTrail        bool   `json:"IsOrganizationTrail"`
}

type createTrailOutput struct {
	Name                       string `json:"Name"`
	S3BucketName               string `json:"S3BucketName"`
	S3KeyPrefix                string `json:"S3KeyPrefix,omitempty"`
	IncludeGlobalServiceEvents bool   `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         bool   `json:"IsMultiRegionTrail"`
	TrailARN                   string `json:"TrailARN"`
	LogFileValidationEnabled   bool   `json:"LogFileValidationEnabled"`
	CloudWatchLogsLogGroupArn  string `json:"CloudWatchLogsLogGroupArn,omitempty"`
	CloudWatchLogsRoleArn      string `json:"CloudWatchLogsRoleArn,omitempty"`
	KmsKeyId                   string `json:"KmsKeyId,omitempty"`
	IsOrganizationTrail        bool   `json:"IsOrganizationTrail"`
}

type updateTrailInput struct {
	Name                       string  `json:"Name"`
	S3BucketName               *string `json:"S3BucketName"`
	S3KeyPrefix                *string `json:"S3KeyPrefix"`
	IncludeGlobalServiceEvents *bool   `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         *bool   `json:"IsMultiRegionTrail"`
	EnableLogFileValidation    *bool   `json:"EnableLogFileValidation"`
	CloudWatchLogsLogGroupArn  *string `json:"CloudWatchLogsLogGroupArn"`
	CloudWatchLogsRoleArn      *string `json:"CloudWatchLogsRoleArn"`
	KmsKeyId                   *string `json:"KmsKeyId"`
	IsOrganizationTrail        *bool   `json:"IsOrganizationTrail"`
}

type describeTrailsRequest struct {
	TrailNameList []string `json:"trailNameList"`
}

type describeTrailsResponse struct {
	TrailList []trailDescribeEntry `json:"trailList"`
}

type trailDescribeEntry struct {
	Name                       string `json:"Name"`
	S3BucketName               string `json:"S3BucketName"`
	S3KeyPrefix                string `json:"S3KeyPrefix,omitempty"`
	IncludeGlobalServiceEvents bool   `json:"IncludeGlobalServiceEvents"`
	IsMultiRegionTrail         bool   `json:"IsMultiRegionTrail"`
	HomeRegion                 string `json:"HomeRegion"`
	TrailARN                   string `json:"TrailARN"`
	LogFileValidationEnabled   bool   `json:"LogFileValidationEnabled"`
	CloudWatchLogsLogGroupArn  string `json:"CloudWatchLogsLogGroupArn,omitempty"`
	CloudWatchLogsRoleArn      string `json:"CloudWatchLogsRoleArn,omitempty"`
	KmsKeyId                   string `json:"KmsKeyId,omitempty"`
	HasCustomEventSelectors    bool   `json:"HasCustomEventSelectors"`
	HasInsightSelectors        bool   `json:"HasInsightSelectors"`
	IsOrganizationTrail        bool   `json:"IsOrganizationTrail"`
}

type deleteTrailRequest struct {
	Name string `json:"Name"`
}

type listTrailsResponse struct {
	Trails []trailListItem `json:"Trails"`
}

type trailListItem struct {
	Name       string `json:"Name"`
	TrailARN   string `json:"TrailARN"`
	HomeRegion string `json:"HomeRegion"`
}

type getTrailStatusRequest struct {
	Name string `json:"Name"`
}

type getTrailStatusResponse struct {
	IsLogging                 bool   `json:"IsLogging"`
	LatestDeliveryError       string `json:"LatestDeliveryError"`
	LatestNotificationError   string `json:"LatestNotificationError"`
	LatestCloudWatchLogsError string `json:"LatestCloudWatchLogsError"`
	LatestDigestDeliveryError string `json:"LatestDigestDeliveryError"`
}

type loggingRequest struct {
	Name string `json:"Name"`
}

type lookupEventsResponse struct {
	Events    []any `json:"Events"`
	NextToken any   `json:"NextToken"`
}

func (h *Handler) createTrailTyped(ctx context.Context, req *createTrailInput) (*createTrailOutput, *protocol.AWSError) {
	if strings.TrimSpace(req.Name) == "" || strings.TrimSpace(req.S3BucketName) == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name and S3BucketName are required",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if _, exists, aerr := h.getTrail(ctx, req.Name); aerr != nil {
		return nil, aerr
	} else if exists {
		return nil, &protocol.AWSError{
			Code:       "TrailAlreadyExistsException",
			Message:    "Trail already exists",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	t := trail{
		Name:                       req.Name,
		S3BucketName:               req.S3BucketName,
		S3KeyPrefix:                req.S3KeyPrefix,
		IncludeGlobalServiceEvents: req.IncludeGlobalServiceEvents,
		IsMultiRegionTrail:         req.IsMultiRegionTrail,
		HomeRegion:                 h.region(),
		TrailARN:                   h.trailARN(req.Name),
		LogFileValidationEnabled:   req.EnableLogFileValidation,
		CloudWatchLogsLogGroupArn:  req.CloudWatchLogsLogGroupArn,
		CloudWatchLogsRoleArn:      req.CloudWatchLogsRoleArn,
		KmsKeyId:                   req.KmsKeyId,
		IsOrganizationTrail:        req.IsOrganizationTrail,
		IsLogging:                  false,
	}

	if aerr := h.putTrail(ctx, &t); aerr != nil {
		return nil, aerr
	}

	return &createTrailOutput{
		Name:                       t.Name,
		S3BucketName:               t.S3BucketName,
		S3KeyPrefix:                t.S3KeyPrefix,
		IncludeGlobalServiceEvents: t.IncludeGlobalServiceEvents,
		IsMultiRegionTrail:         t.IsMultiRegionTrail,
		TrailARN:                   t.TrailARN,
		LogFileValidationEnabled:   t.LogFileValidationEnabled,
		CloudWatchLogsLogGroupArn:  t.CloudWatchLogsLogGroupArn,
		CloudWatchLogsRoleArn:      t.CloudWatchLogsRoleArn,
		KmsKeyId:                   t.KmsKeyId,
		IsOrganizationTrail:        t.IsOrganizationTrail,
	}, nil
}

func (h *Handler) describeTrailsTyped(ctx context.Context, req *describeTrailsRequest) (*describeTrailsResponse, *protocol.AWSError) {
	trails, aerr := h.listAllTrails(ctx)
	if aerr != nil {
		return nil, aerr
	}

	allow := make(map[string]struct{}, len(req.TrailNameList))
	if len(req.TrailNameList) > 0 {
		for _, name := range req.TrailNameList {
			allow[name] = struct{}{}
		}
	}

	out := make([]trailDescribeEntry, 0, len(trails))
	for i := range trails {
		if len(allow) > 0 {
			if _, ok := allow[trails[i].Name]; !ok {
				continue
			}
		}
		out = append(out, trailDescribeEntry{
			Name:                       trails[i].Name,
			S3BucketName:               trails[i].S3BucketName,
			S3KeyPrefix:                trails[i].S3KeyPrefix,
			IncludeGlobalServiceEvents: trails[i].IncludeGlobalServiceEvents,
			IsMultiRegionTrail:         trails[i].IsMultiRegionTrail,
			HomeRegion:                 trails[i].HomeRegion,
			TrailARN:                   trails[i].TrailARN,
			LogFileValidationEnabled:   trails[i].LogFileValidationEnabled,
			CloudWatchLogsLogGroupArn:  trails[i].CloudWatchLogsLogGroupArn,
			CloudWatchLogsRoleArn:      trails[i].CloudWatchLogsRoleArn,
			KmsKeyId:                   trails[i].KmsKeyId,
			HasCustomEventSelectors:    false,
			HasInsightSelectors:        false,
			IsOrganizationTrail:        trails[i].IsOrganizationTrail,
		})
	}

	return &describeTrailsResponse{TrailList: out}, nil
}

func (h *Handler) updateTrailTyped(ctx context.Context, req *updateTrailInput) (*createTrailOutput, *protocol.AWSError) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	t, exists, aerr := h.getTrail(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if req.S3BucketName != nil {
		t.S3BucketName = *req.S3BucketName
	}
	if req.S3KeyPrefix != nil {
		t.S3KeyPrefix = *req.S3KeyPrefix
	}
	if req.IncludeGlobalServiceEvents != nil {
		t.IncludeGlobalServiceEvents = *req.IncludeGlobalServiceEvents
	}
	if req.IsMultiRegionTrail != nil {
		t.IsMultiRegionTrail = *req.IsMultiRegionTrail
	}
	if req.EnableLogFileValidation != nil {
		t.LogFileValidationEnabled = *req.EnableLogFileValidation
	}
	if req.CloudWatchLogsLogGroupArn != nil {
		t.CloudWatchLogsLogGroupArn = *req.CloudWatchLogsLogGroupArn
	}
	if req.CloudWatchLogsRoleArn != nil {
		t.CloudWatchLogsRoleArn = *req.CloudWatchLogsRoleArn
	}
	if req.KmsKeyId != nil {
		t.KmsKeyId = *req.KmsKeyId
	}
	if req.IsOrganizationTrail != nil {
		t.IsOrganizationTrail = *req.IsOrganizationTrail
	}

	if aerr := h.putTrail(ctx, t); aerr != nil {
		return nil, aerr
	}

	return &createTrailOutput{
		Name:                       t.Name,
		S3BucketName:               t.S3BucketName,
		S3KeyPrefix:                t.S3KeyPrefix,
		IncludeGlobalServiceEvents: t.IncludeGlobalServiceEvents,
		IsMultiRegionTrail:         t.IsMultiRegionTrail,
		TrailARN:                   t.TrailARN,
		LogFileValidationEnabled:   t.LogFileValidationEnabled,
		CloudWatchLogsLogGroupArn:  t.CloudWatchLogsLogGroupArn,
		CloudWatchLogsRoleArn:      t.CloudWatchLogsRoleArn,
		KmsKeyId:                   t.KmsKeyId,
		IsOrganizationTrail:        t.IsOrganizationTrail,
	}, nil
}

func (h *Handler) deleteTrailTyped(ctx context.Context, req *deleteTrailRequest) (*struct{}, *protocol.AWSError) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if _, exists, aerr := h.getTrail(ctx, req.Name); aerr != nil {
		return nil, aerr
	} else if !exists {
		return nil, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	if err := h.store.Delete(ctx, nsTrails, req.Name); err != nil {
		return nil, protocol.ErrInternalError
	}

	return &struct{}{}, nil
}

func (h *Handler) listTrailsTyped(ctx context.Context, req *struct{}) (*listTrailsResponse, *protocol.AWSError) {
	trails, aerr := h.listAllTrails(ctx)
	if aerr != nil {
		return nil, aerr
	}
	out := make([]trailListItem, 0, len(trails))
	for i := range trails {
		out = append(out, trailListItem{
			Name:       trails[i].Name,
			TrailARN:   trails[i].TrailARN,
			HomeRegion: trails[i].HomeRegion,
		})
	}
	return &listTrailsResponse{Trails: out}, nil
}

func (h *Handler) getTrailStatusTyped(ctx context.Context, req *getTrailStatusRequest) (*getTrailStatusResponse, *protocol.AWSError) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	t, exists, aerr := h.getTrail(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		}
	}

	return &getTrailStatusResponse{
		IsLogging:                 t.IsLogging,
		LatestDeliveryError:       "",
		LatestNotificationError:   "",
		LatestCloudWatchLogsError: "",
		LatestDigestDeliveryError: "",
	}, nil
}

func (h *Handler) lookupEventsTyped(ctx context.Context, req *struct{}) (*lookupEventsResponse, *protocol.AWSError) {
	return &lookupEventsResponse{
		Events:    []any{},
		NextToken: nil,
	}, nil
}

func (h *Handler) startLoggingTyped(ctx context.Context, req *loggingRequest) (*struct{}, *protocol.AWSError) {
	return h.setLoggingTyped(ctx, req, true)
}

func (h *Handler) stopLoggingTyped(ctx context.Context, req *loggingRequest) (*struct{}, *protocol.AWSError) {
	return h.setLoggingTyped(ctx, req, false)
}

func (h *Handler) setLoggingTyped(ctx context.Context, req *loggingRequest, logging bool) (*struct{}, *protocol.AWSError) {
	if strings.TrimSpace(req.Name) == "" {
		return nil, &protocol.AWSError{
			Code:       "InvalidTrailNameException",
			Message:    "Name is required",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	t, exists, aerr := h.getTrail(ctx, req.Name)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{
			Code:       "TrailNotFoundException",
			Message:    "Trail not found",
			HTTPStatus: http.StatusBadRequest,
		}
	}
	if t.IsLogging == logging {
		return &struct{}{}, nil
	}
	t.IsLogging = logging
	if aerr := h.putTrail(ctx, t); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}
