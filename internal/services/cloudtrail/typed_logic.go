package cloudtrail

import (
	"context"
	"net/http"
	"sort"
	"strings"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
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
	// CreateTrail is the only trail operation that carries tags inline. The
	// Trail shape CreateTrail and DescribeTrails answer with has no Tags
	// member, so they must not report them back.
	TagsList []cloudTrailTag `json:"TagsList"`
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

	tags, aerr := validatedTagsList(req.TagsList)
	if aerr != nil {
		return nil, aerr
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
		Tags:                       tags,
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

// ---- Resource tagging --------------------------------------------------------
//
// CloudTrail's spelling of the tag operations is its own: AddTags / RemoveTags
// / ListTags, addressing a `ResourceId` and carrying a `TagsList`. RemoveTags
// takes tags rather than tag keys and matches on each entry's Key, and
// ListTags takes a *list* of resource IDs.

type cloudTrailTag struct {
	Key   string `json:"Key"`
	Value string `json:"Value,omitempty"`
}

type addTagsRequest struct {
	ResourceId string          `json:"ResourceId"`
	TagsList   []cloudTrailTag `json:"TagsList"`
}

type removeTagsRequest struct {
	ResourceId string          `json:"ResourceId"`
	TagsList   []cloudTrailTag `json:"TagsList"`
}

type listTagsRequest struct {
	ResourceIdList []string `json:"ResourceIdList"`
	NextToken      string   `json:"NextToken"`
}

type resourceTag struct {
	ResourceId string          `json:"ResourceId"`
	TagsList   []cloudTrailTag `json:"TagsList"`
}

type listTagsResponse struct {
	ResourceTagList []resourceTag `json:"ResourceTagList"`
}

// cloudTrailTagCfg tunes shared tag validation to CloudTrail's error shape.
var cloudTrailTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "TagsLimitExceededException",
	InvalidCode:     "InvalidTagParameterException",
	ExceededMessage: "A trail can have a maximum of 50 tags.",
}

// trailNameFromResourceID extracts the trail name from a CloudTrail resource
// ARN. Event data stores and channels are CloudTrail's other taggable
// resources and neither is emulated, so their ARNs are refused.
func trailNameFromResourceID(resourceID string) (string, *protocol.AWSError) {
	if resourceID == "" {
		return "", &protocol.AWSError{
			Code: "InvalidTrailNameException", Message: "ResourceId is required", HTTPStatus: http.StatusBadRequest,
		}
	}
	parts := strings.SplitN(resourceID, ":", 6)
	if len(parts) < 6 || !strings.HasPrefix(parts[5], "trail/") {
		return "", &protocol.AWSError{
			Code:       "ResourceTypeNotSupportedException",
			Message:    "Resource type is not supported: " + resourceID,
			HTTPStatus: http.StatusBadRequest,
		}
	}
	name := strings.TrimPrefix(parts[5], "trail/")
	if name == "" {
		return "", &protocol.AWSError{
			Code: "InvalidTrailNameException", Message: "Invalid trail ARN: " + resourceID, HTTPStatus: http.StatusBadRequest,
		}
	}
	return name, nil
}

// trailForResourceID resolves a tag request's ResourceId to its stored trail.
func (h *Handler) trailForResourceID(ctx context.Context, resourceID string) (*trail, *protocol.AWSError) {
	name, aerr := trailNameFromResourceID(resourceID)
	if aerr != nil {
		return nil, aerr
	}
	t, exists, aerr := h.getTrail(ctx, name)
	if aerr != nil {
		return nil, aerr
	}
	if !exists {
		return nil, &protocol.AWSError{
			Code:       "ResourceNotFoundException",
			Message:    "Trail not found: " + resourceID,
			HTTPStatus: http.StatusNotFound,
		}
	}
	return t, nil
}

// tagsToMap and mapToTags convert between the stored wire shape and the map
// the shared validation helper works in. mapToTags sorts, because Go
// randomizes map iteration per process and the list would otherwise reorder
// between otherwise identical responses.
func tagsToMap(list []cloudTrailTag) map[string]string {
	out := make(map[string]string, len(list))
	for _, t := range list {
		out[t.Key] = t.Value
	}
	return out
}

func mapToTags(m map[string]string) []cloudTrailTag {
	out := make([]cloudTrailTag, 0, len(m))
	for k, v := range m {
		out = append(out, cloudTrailTag{Key: k, Value: v})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// validatedTagsList validates an inline TagsList from CreateTrail.
func validatedTagsList(list []cloudTrailTag) ([]cloudTrailTag, *protocol.AWSError) {
	if len(list) == 0 {
		return nil, nil
	}
	m := tagsToMap(list)
	if aerr := serviceutil.ValidateTags(cloudTrailTagCfg, m); aerr != nil {
		return nil, aerr
	}
	return mapToTags(m), nil
}

func (h *Handler) addTagsTyped(ctx context.Context, req *addTagsRequest) (*struct{}, *protocol.AWSError) {
	t, aerr := h.trailForResourceID(ctx, req.ResourceId)
	if aerr != nil {
		return nil, aerr
	}
	merged := tagsToMap(t.Tags)
	for _, tag := range req.TagsList {
		merged[tag.Key] = tag.Value
	}
	if aerr := serviceutil.ValidateTags(cloudTrailTagCfg, merged); aerr != nil {
		return nil, aerr
	}
	t.Tags = mapToTags(merged)
	if aerr := h.putTrail(ctx, t); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

// removeTagsTyped matches on each entry's Key and ignores its Value, which is
// how AWS reads RemoveTags' TagsList.
func (h *Handler) removeTagsTyped(ctx context.Context, req *removeTagsRequest) (*struct{}, *protocol.AWSError) {
	t, aerr := h.trailForResourceID(ctx, req.ResourceId)
	if aerr != nil {
		return nil, aerr
	}
	remaining := tagsToMap(t.Tags)
	for _, tag := range req.TagsList {
		delete(remaining, tag.Key)
	}
	t.Tags = mapToTags(remaining)
	if aerr := h.putTrail(ctx, t); aerr != nil {
		return nil, aerr
	}
	return &struct{}{}, nil
}

func (h *Handler) listTagsTyped(ctx context.Context, req *listTagsRequest) (*listTagsResponse, *protocol.AWSError) {
	out := make([]resourceTag, 0, len(req.ResourceIdList))
	for _, id := range req.ResourceIdList {
		t, aerr := h.trailForResourceID(ctx, id)
		if aerr != nil {
			return nil, aerr
		}
		tags := t.Tags
		if tags == nil {
			tags = []cloudTrailTag{}
		}
		out = append(out, resourceTag{ResourceId: id, TagsList: tags})
	}
	return &listTagsResponse{ResourceTagList: out}, nil
}
