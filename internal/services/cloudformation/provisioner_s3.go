package cloudformation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"time"

	s3service "github.com/Neaox/overcast/internal/services/s3"
)

const s3ConfigurationContentType = "application/xml"

var s3BucketSubresources = []struct {
	property  string
	query     string
	putAPI    string
	deleteAPI string
}{
	{property: "LifecycleConfiguration", query: "lifecycle", putAPI: "PutBucketLifecycleConfiguration", deleteAPI: "DeleteBucketLifecycle"},
	{property: "VersioningConfiguration", query: "versioning", putAPI: "PutBucketVersioning"},
	{property: "NotificationConfiguration", query: "notification", putAPI: "PutBucketNotificationConfiguration"},
	{property: "BucketEncryption", query: "encryption", putAPI: "PutBucketEncryption", deleteAPI: "DeleteBucketEncryption"},
	{property: "Tags", query: "tagging", putAPI: "PutBucketTagging", deleteAPI: "DeleteBucketTagging"},
	{property: "CorsConfiguration", query: "cors", putAPI: "PutBucketCors", deleteAPI: "DeleteBucketCors"},
	{property: "WebsiteConfiguration", query: "website", putAPI: "PutBucketWebsite", deleteAPI: "DeleteBucketWebsite"},
}

type cfnS3BucketProperties struct {
	BucketName                string                          `json:"BucketName,omitempty"`
	LifecycleConfiguration    *cfnS3LifecycleConfiguration    `json:"LifecycleConfiguration,omitempty"`
	VersioningConfiguration   *cfnS3VersioningConfiguration   `json:"VersioningConfiguration,omitempty"`
	NotificationConfiguration *cfnS3NotificationConfiguration `json:"NotificationConfiguration,omitempty"`
	BucketEncryption          *cfnS3BucketEncryption          `json:"BucketEncryption,omitempty"`
	Tags                      *[]cfnS3Tag                     `json:"Tags,omitempty"`
	CorsConfiguration         *cfnS3CorsConfiguration         `json:"CorsConfiguration,omitempty"`
	WebsiteConfiguration      *cfnS3WebsiteConfiguration      `json:"WebsiteConfiguration,omitempty"`
}

type cfnS3LifecycleConfiguration struct {
	Rules                              []cfnS3LifecycleRule `json:"Rules"`
	TransitionDefaultMinimumObjectSize json.RawMessage      `json:"TransitionDefaultMinimumObjectSize,omitempty"`
}

type cfnS3LifecycleRule struct {
	AbortIncompleteMultipartUpload    *cfnS3AbortIncompleteMultipartUpload `json:"AbortIncompleteMultipartUpload,omitempty"`
	ExpirationDate                    *string                              `json:"ExpirationDate,omitempty"`
	ExpirationInDays                  *int                                 `json:"ExpirationInDays,omitempty"`
	ExpiredObjectDeleteMarker         *bool                                `json:"ExpiredObjectDeleteMarker,omitempty"`
	ID                                string                               `json:"Id,omitempty"`
	NoncurrentVersionExpiration       json.RawMessage                      `json:"NoncurrentVersionExpiration,omitempty"`
	NoncurrentVersionExpirationInDays *int                                 `json:"NoncurrentVersionExpirationInDays,omitempty"`
	NoncurrentVersionTransition       json.RawMessage                      `json:"NoncurrentVersionTransition,omitempty"`
	NoncurrentVersionTransitions      json.RawMessage                      `json:"NoncurrentVersionTransitions,omitempty"`
	ObjectSizeGreaterThan             *string                              `json:"ObjectSizeGreaterThan,omitempty"`
	ObjectSizeLessThan                *string                              `json:"ObjectSizeLessThan,omitempty"`
	Prefix                            *string                              `json:"Prefix,omitempty"`
	Status                            string                               `json:"Status"`
	TagFilters                        []cfnS3TagFilter                     `json:"TagFilters,omitempty"`
	Transition                        *cfnS3Transition                     `json:"Transition,omitempty"`
	Transitions                       *[]cfnS3Transition                   `json:"Transitions,omitempty"`
}

type cfnS3AbortIncompleteMultipartUpload struct {
	DaysAfterInitiation int `json:"DaysAfterInitiation"`
}

type cfnS3TagFilter struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type cfnS3Transition struct {
	StorageClass     string  `json:"StorageClass"`
	TransitionDate   *string `json:"TransitionDate,omitempty"`
	TransitionInDays *int    `json:"TransitionInDays,omitempty"`
}

type cfnS3VersioningConfiguration struct {
	Status string `json:"Status"`
}

type cfnS3NotificationConfiguration struct {
	EventBridgeConfiguration json.RawMessage            `json:"EventBridgeConfiguration,omitempty"`
	LambdaConfigurations     []cfnS3LambdaConfiguration `json:"LambdaConfigurations,omitempty"`
	QueueConfigurations      []cfnS3QueueConfiguration  `json:"QueueConfigurations,omitempty"`
	TopicConfigurations      []cfnS3TopicConfiguration  `json:"TopicConfigurations,omitempty"`
}

type cfnS3LambdaConfiguration struct {
	Event    string                   `json:"Event"`
	Filter   *cfnS3NotificationFilter `json:"Filter,omitempty"`
	Function string                   `json:"Function"`
}

type cfnS3QueueConfiguration struct {
	Event  string                   `json:"Event"`
	Filter *cfnS3NotificationFilter `json:"Filter,omitempty"`
	Queue  string                   `json:"Queue"`
}

type cfnS3TopicConfiguration struct {
	Event  string                   `json:"Event"`
	Filter *cfnS3NotificationFilter `json:"Filter,omitempty"`
	Topic  string                   `json:"Topic"`
}

type cfnS3NotificationFilter struct {
	S3Key cfnS3NotificationKeyFilter `json:"S3Key"`
}

type cfnS3NotificationKeyFilter struct {
	Rules []cfnS3NotificationFilterRule `json:"Rules"`
}

type cfnS3NotificationFilterRule struct {
	Name  string `json:"Name"`
	Value string `json:"Value"`
}

type cfnS3BucketEncryption struct {
	ServerSideEncryptionConfiguration []cfnS3ServerSideEncryptionRule `json:"ServerSideEncryptionConfiguration"`
}

type cfnS3ServerSideEncryptionRule struct {
	BlockedEncryptionTypes        json.RawMessage                     `json:"BlockedEncryptionTypes,omitempty"`
	BucketKeyEnabled              *bool                               `json:"BucketKeyEnabled,omitempty"`
	ServerSideEncryptionByDefault *cfnS3ServerSideEncryptionByDefault `json:"ServerSideEncryptionByDefault,omitempty"`
}

type cfnS3ServerSideEncryptionByDefault struct {
	KMSMasterKeyID string `json:"KMSMasterKeyID,omitempty"`
	SSEAlgorithm   string `json:"SSEAlgorithm"`
}

type cfnS3Tag struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type cfnS3CorsConfiguration struct {
	CorsRules []cfnS3CorsRule `json:"CorsRules"`
}

type cfnS3CorsRule struct {
	AllowedHeaders []string `json:"AllowedHeaders,omitempty"`
	AllowedMethods []string `json:"AllowedMethods"`
	AllowedOrigins []string `json:"AllowedOrigins"`
	ExposedHeaders []string `json:"ExposedHeaders,omitempty"`
	ID             *string  `json:"Id,omitempty"`
	MaxAge         int      `json:"MaxAge,omitempty"`
}

type cfnS3WebsiteConfiguration struct {
	ErrorDocument         string          `json:"ErrorDocument,omitempty"`
	IndexDocument         string          `json:"IndexDocument,omitempty"`
	RedirectAllRequestsTo json.RawMessage `json:"RedirectAllRequestsTo,omitempty"`
	RoutingRules          json.RawMessage `json:"RoutingRules,omitempty"`
}

type s3BucketOperation struct {
	api         string
	method      string
	query       string
	contentType string
	body        []byte
}

func decodeS3BucketProperties(props map[string]any) (*cfnS3BucketProperties, error) {
	data, err := json.Marshal(props)
	if err != nil {
		return nil, fmt.Errorf("marshal AWS::S3::Bucket properties: %w", err)
	}
	var decoded cfnS3BucketProperties
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode AWS::S3::Bucket properties: %w", err)
	}
	return &decoded, nil
}

func planS3BucketOperations(props, oldProps *cfnS3BucketProperties) ([]s3BucketOperation, error) {
	bodies, err := marshalS3BucketPropertyBodies(props)
	if err != nil {
		return nil, err
	}

	operations := make([]s3BucketOperation, 0, len(s3BucketSubresources))
	for _, subresource := range s3BucketSubresources {
		present := s3BucketPropertyPresent(props, subresource.property)
		wasPresent := oldProps != nil && s3BucketPropertyPresent(oldProps, subresource.property)
		if present {
			if oldProps != nil && wasPresent && reflect.DeepEqual(
				s3BucketPropertyValue(props, subresource.property),
				s3BucketPropertyValue(oldProps, subresource.property),
			) {
				continue
			}
			operations = append(operations, s3BucketOperation{
				api: subresource.putAPI, method: http.MethodPut, query: subresource.query,
				contentType: s3ConfigurationContentType, body: bodies[subresource.property],
			})
			continue
		}
		if !wasPresent {
			continue
		}

		switch subresource.property {
		case "VersioningConfiguration":
			body, marshalErr := s3service.MarshalVersioningConfiguration("Suspended")
			if marshalErr != nil {
				return nil, fmt.Errorf("translate removed VersioningConfiguration: %w", marshalErr)
			}
			operations = append(operations, s3BucketOperation{
				api: subresource.putAPI, method: http.MethodPut, query: subresource.query,
				contentType: s3ConfigurationContentType, body: body,
			})
		case "NotificationConfiguration":
			body, marshalErr := s3service.MarshalNotificationConfiguration(&s3service.NotificationConfig{})
			if marshalErr != nil {
				return nil, fmt.Errorf("translate removed NotificationConfiguration: %w", marshalErr)
			}
			operations = append(operations, s3BucketOperation{
				api: subresource.putAPI, method: http.MethodPut, query: subresource.query,
				contentType: s3ConfigurationContentType, body: body,
			})
		default:
			operations = append(operations, s3BucketOperation{
				api: subresource.deleteAPI, method: http.MethodDelete, query: subresource.query,
			})
		}
	}
	return operations, nil
}

func applyS3BucketOperations(ctx context.Context, router http.Handler, region, bucket string, operations []s3BucketOperation) (int, error) {
	for i, operation := range operations {
		if _, err := internalRequest(ctx, router, region, operation.method, "/"+bucket+"?"+operation.query, operation.contentType, operation.body); err != nil {
			return i, fmt.Errorf("s3 %s: %w", operation.api, err)
		}
	}
	return len(operations), nil
}

func compensateS3BucketOperations(ctx context.Context, router http.Handler, region, bucket string, applied []s3BucketOperation, compensations []s3BucketOperation) error {
	byQuery := make(map[string]s3BucketOperation, len(compensations))
	for _, operation := range compensations {
		byQuery[operation.query] = operation
	}
	var compensationErr error
	for i := len(applied) - 1; i >= 0; i-- {
		operation, ok := byQuery[applied[i].query]
		if !ok {
			continue
		}
		if _, err := applyS3BucketOperations(ctx, router, region, bucket, []s3BucketOperation{operation}); err != nil {
			compensationErr = errors.Join(compensationErr, fmt.Errorf("restore %s after failed update: %w", operation.api, err))
		}
	}
	return compensationErr
}

func marshalS3BucketPropertyBodies(props *cfnS3BucketProperties) (map[string][]byte, error) {
	bodies := make(map[string][]byte, len(s3BucketSubresources))
	marshal := func(property string, present bool, fn func() ([]byte, error)) error {
		if !present {
			return nil
		}
		body, err := fn()
		if err != nil {
			return fmt.Errorf("translate %s: %w", property, err)
		}
		bodies[property] = body
		return nil
	}

	if err := marshal("LifecycleConfiguration", props.LifecycleConfiguration != nil, func() ([]byte, error) {
		cfg, err := translateS3LifecycleConfiguration(props.LifecycleConfiguration)
		if err != nil {
			return nil, err
		}
		return s3service.MarshalLifecycleConfiguration(cfg)
	}); err != nil {
		return nil, err
	}
	if err := marshal("VersioningConfiguration", props.VersioningConfiguration != nil, func() ([]byte, error) {
		return s3service.MarshalVersioningConfiguration(props.VersioningConfiguration.Status)
	}); err != nil {
		return nil, err
	}
	if err := marshal("NotificationConfiguration", props.NotificationConfiguration != nil, func() ([]byte, error) {
		cfg, err := translateS3NotificationConfiguration(props.NotificationConfiguration)
		if err != nil {
			return nil, err
		}
		return s3service.MarshalNotificationConfiguration(cfg)
	}); err != nil {
		return nil, err
	}
	if err := marshal("BucketEncryption", props.BucketEncryption != nil, func() ([]byte, error) {
		rules, err := translateS3BucketEncryption(props.BucketEncryption)
		if err != nil {
			return nil, err
		}
		return s3service.MarshalBucketEncryption(rules)
	}); err != nil {
		return nil, err
	}
	if err := marshal("Tags", props.Tags != nil, func() ([]byte, error) {
		tags, err := translateS3BucketTags(*props.Tags)
		if err != nil {
			return nil, err
		}
		return s3service.MarshalBucketTagging(tags)
	}); err != nil {
		return nil, err
	}
	if err := marshal("CorsConfiguration", props.CorsConfiguration != nil, func() ([]byte, error) {
		rules, err := translateS3CorsConfiguration(props.CorsConfiguration)
		if err != nil {
			return nil, err
		}
		return s3service.MarshalCORSConfiguration(rules)
	}); err != nil {
		return nil, err
	}
	if err := marshal("WebsiteConfiguration", props.WebsiteConfiguration != nil, func() ([]byte, error) {
		cfg, err := translateS3WebsiteConfiguration(props.WebsiteConfiguration)
		if err != nil {
			return nil, err
		}
		return s3service.MarshalWebsiteConfiguration(cfg)
	}); err != nil {
		return nil, err
	}
	return bodies, nil
}

func translateS3LifecycleConfiguration(in *cfnS3LifecycleConfiguration) (*s3service.LifecycleConfiguration, error) {
	if len(in.TransitionDefaultMinimumObjectSize) != 0 {
		return nil, fmt.Errorf("TransitionDefaultMinimumObjectSize cannot be represented by the current S3 API handler")
	}
	out := &s3service.LifecycleConfiguration{Rules: make([]s3service.LifecycleRule, 0, len(in.Rules))}
	for i := range in.Rules {
		rule, err := translateS3LifecycleRule(&in.Rules[i])
		if err != nil {
			return nil, fmt.Errorf("Rules[%d]: %w", i, err)
		}
		out.Rules = append(out.Rules, rule)
	}
	return out, nil
}

func translateS3LifecycleRule(in *cfnS3LifecycleRule) (s3service.LifecycleRule, error) {
	if len(in.NoncurrentVersionExpiration) != 0 {
		return s3service.LifecycleRule{}, fmt.Errorf("NoncurrentVersionExpiration cannot be represented by the current S3 lifecycle handler")
	}
	if len(in.NoncurrentVersionTransition) != 0 {
		return s3service.LifecycleRule{}, fmt.Errorf("NoncurrentVersionTransition cannot be represented by the current S3 lifecycle handler")
	}
	if len(in.NoncurrentVersionTransitions) != 0 {
		return s3service.LifecycleRule{}, fmt.Errorf("NoncurrentVersionTransitions cannot be represented by the current S3 lifecycle handler")
	}
	if in.NoncurrentVersionExpirationInDays != nil {
		return s3service.LifecycleRule{}, fmt.Errorf("NoncurrentVersionExpirationInDays cannot be represented by the current S3 lifecycle handler")
	}
	if in.ExpiredObjectDeleteMarker != nil {
		return s3service.LifecycleRule{}, fmt.Errorf("ExpiredObjectDeleteMarker cannot be represented by the current S3 lifecycle handler")
	}
	if in.Transition != nil && in.Transitions != nil {
		return s3service.LifecycleRule{}, fmt.Errorf("Transition and Transitions are mutually exclusive")
	}

	rule := s3service.LifecycleRule{ID: in.ID, Status: in.Status}
	filter, prefix, err := translateS3LifecycleFilter(in)
	if err != nil {
		return s3service.LifecycleRule{}, err
	}
	rule.Filter = filter
	rule.Prefix = prefix

	if in.ExpirationDate != nil && in.ExpirationInDays != nil {
		return s3service.LifecycleRule{}, fmt.Errorf("ExpirationDate and ExpirationInDays are mutually exclusive")
	}
	if in.ExpirationDate != nil {
		date, parseErr := parseS3LifecycleDate(*in.ExpirationDate)
		if parseErr != nil {
			return s3service.LifecycleRule{}, fmt.Errorf("ExpirationDate: %w", parseErr)
		}
		rule.Expiration = &s3service.LifecycleExpiration{Date: &date}
	} else if in.ExpirationInDays != nil {
		rule.Expiration = &s3service.LifecycleExpiration{Days: *in.ExpirationInDays}
	}
	if in.AbortIncompleteMultipartUpload != nil {
		rule.AbortIncompleteMultipartUpload = &s3service.LifecycleAbortMPU{
			DaysAfterInitiation: in.AbortIncompleteMultipartUpload.DaysAfterInitiation,
		}
	}

	transitions := make([]cfnS3Transition, 0, 1)
	if in.Transition != nil {
		transitions = append(transitions, *in.Transition)
	}
	if in.Transitions != nil {
		transitions = append(transitions, (*in.Transitions)...)
	}
	for i := range transitions {
		transition, translateErr := translateS3LifecycleTransition(&transitions[i])
		if translateErr != nil {
			return s3service.LifecycleRule{}, fmt.Errorf("Transitions[%d]: %w", i, translateErr)
		}
		rule.Transitions = append(rule.Transitions, transition)
	}
	return rule, nil
}

func translateS3LifecycleFilter(in *cfnS3LifecycleRule) (*s3service.LifecycleFilter, *string, error) {
	greater, err := parseOptionalInt64(in.ObjectSizeGreaterThan)
	if err != nil {
		return nil, nil, fmt.Errorf("ObjectSizeGreaterThan: %w", err)
	}
	less, err := parseOptionalInt64(in.ObjectSizeLessThan)
	if err != nil {
		return nil, nil, fmt.Errorf("ObjectSizeLessThan: %w", err)
	}
	if len(in.TagFilters) == 0 && greater == nil && less == nil {
		if in.Prefix != nil {
			return nil, in.Prefix, nil
		}
		empty := ""
		return nil, &empty, nil
	}

	predicateCount := len(in.TagFilters)
	if in.Prefix != nil {
		predicateCount++
	}
	if greater != nil {
		predicateCount++
	}
	if less != nil {
		predicateCount++
	}
	if predicateCount == 1 {
		filter := &s3service.LifecycleFilter{ObjectSizeGreaterThan: greater, ObjectSizeLessThan: less}
		switch {
		case in.Prefix != nil:
			filter.Prefix = *in.Prefix
		case len(in.TagFilters) == 1:
			filter.Tag = &s3service.LifecycleTag{Key: in.TagFilters[0].Key, Value: in.TagFilters[0].Value}
		}
		return filter, nil, nil
	}

	and := &s3service.LifecycleAnd{ObjectSizeGreaterThan: greater, ObjectSizeLessThan: less}
	if in.Prefix != nil {
		and.Prefix = *in.Prefix
	}
	for _, tag := range in.TagFilters {
		and.Tags = append(and.Tags, s3service.LifecycleTag{Key: tag.Key, Value: tag.Value})
	}
	return &s3service.LifecycleFilter{And: and}, nil, nil
}

func translateS3LifecycleTransition(in *cfnS3Transition) (s3service.LifecycleTransition, error) {
	if in.TransitionDate != nil && in.TransitionInDays != nil {
		return s3service.LifecycleTransition{}, fmt.Errorf("TransitionDate and TransitionInDays are mutually exclusive")
	}
	out := s3service.LifecycleTransition{StorageClass: in.StorageClass}
	if in.TransitionDate != nil {
		date, err := parseS3LifecycleDate(*in.TransitionDate)
		if err != nil {
			return s3service.LifecycleTransition{}, fmt.Errorf("TransitionDate: %w", err)
		}
		out.Date = &date
	} else if in.TransitionInDays != nil {
		out.Days = *in.TransitionInDays
	}
	return out, nil
}

func parseS3LifecycleDate(value string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not an ISO-8601 date", value)
}

func parseOptionalInt64(value *string) (*int64, error) {
	if value == nil {
		return nil, nil
	}
	parsed, err := strconv.ParseInt(*value, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%q is not an integer", *value)
	}
	return &parsed, nil
}

func translateS3NotificationConfiguration(in *cfnS3NotificationConfiguration) (*s3service.NotificationConfig, error) {
	if len(in.EventBridgeConfiguration) != 0 {
		return nil, fmt.Errorf("EventBridgeConfiguration cannot be represented by the current S3 notification handler")
	}
	out := &s3service.NotificationConfig{}
	for _, config := range in.QueueConfigurations {
		out.QueueConfigurations = append(out.QueueConfigurations, s3service.QueueNotificationConfig{
			ARN: config.Queue, Events: []string{config.Event}, Filter: translateS3NotificationFilter(config.Filter),
		})
	}
	for _, config := range in.TopicConfigurations {
		out.TopicConfigurations = append(out.TopicConfigurations, s3service.TopicNotificationConfig{
			ARN: config.Topic, Events: []string{config.Event}, Filter: translateS3NotificationFilter(config.Filter),
		})
	}
	for _, config := range in.LambdaConfigurations {
		out.LambdaConfigurations = append(out.LambdaConfigurations, s3service.LambdaNotificationConfig{
			ARN: config.Function, Events: []string{config.Event}, Filter: translateS3NotificationFilter(config.Filter),
		})
	}
	return out, nil
}

func translateS3NotificationFilter(in *cfnS3NotificationFilter) *s3service.NotificationFilter {
	if in == nil {
		return nil
	}
	out := &s3service.NotificationFilter{}
	for _, rule := range in.S3Key.Rules {
		out.Key.Rules = append(out.Key.Rules, s3service.NotificationFilterRule{Name: rule.Name, Value: rule.Value})
	}
	return out
}

func translateS3BucketEncryption(in *cfnS3BucketEncryption) ([]s3service.BucketEncryptionRule, error) {
	out := make([]s3service.BucketEncryptionRule, 0, len(in.ServerSideEncryptionConfiguration))
	for i, rule := range in.ServerSideEncryptionConfiguration {
		if len(rule.BlockedEncryptionTypes) != 0 {
			return nil, fmt.Errorf("ServerSideEncryptionConfiguration[%d].BlockedEncryptionTypes cannot be represented by the current S3 encryption handler", i)
		}
		translated := s3service.BucketEncryptionRule{BucketKeyEnabled: rule.BucketKeyEnabled}
		if rule.ServerSideEncryptionByDefault != nil {
			translated.SSEAlgorithm = rule.ServerSideEncryptionByDefault.SSEAlgorithm
			translated.KMSMasterKeyID = rule.ServerSideEncryptionByDefault.KMSMasterKeyID
		}
		out = append(out, translated)
	}
	return out, nil
}

func translateS3BucketTags(in []cfnS3Tag) (map[string]string, error) {
	out := make(map[string]string, len(in))
	for _, tag := range in {
		if _, duplicate := out[tag.Key]; duplicate {
			return nil, fmt.Errorf("duplicate tag key %q cannot be represented by PutBucketTagging", tag.Key)
		}
		out[tag.Key] = tag.Value
	}
	return out, nil
}

func translateS3CorsConfiguration(in *cfnS3CorsConfiguration) ([]s3service.CORSRule, error) {
	out := make([]s3service.CORSRule, 0, len(in.CorsRules))
	for i, rule := range in.CorsRules {
		if rule.ID != nil {
			return nil, fmt.Errorf("CorsRules[%d].Id cannot be represented by the current S3 CORS handler", i)
		}
		out = append(out, s3service.CORSRule{
			AllowedHeaders: rule.AllowedHeaders,
			AllowedMethods: rule.AllowedMethods,
			AllowedOrigins: rule.AllowedOrigins,
			ExposeHeaders:  rule.ExposedHeaders,
			MaxAgeSeconds:  rule.MaxAge,
		})
	}
	return out, nil
}

func translateS3WebsiteConfiguration(in *cfnS3WebsiteConfiguration) (*s3service.WebsiteConfiguration, error) {
	if len(in.RedirectAllRequestsTo) != 0 {
		return nil, fmt.Errorf("RedirectAllRequestsTo cannot be represented by the current S3 website handler")
	}
	if len(in.RoutingRules) != 0 {
		return nil, fmt.Errorf("RoutingRules cannot be represented by the current S3 website handler")
	}
	return &s3service.WebsiteConfiguration{
		IndexDocument: in.IndexDocument,
		ErrorDocument: in.ErrorDocument,
	}, nil
}

func s3BucketPropertyPresent(props *cfnS3BucketProperties, property string) bool {
	switch property {
	case "LifecycleConfiguration":
		return props.LifecycleConfiguration != nil
	case "VersioningConfiguration":
		return props.VersioningConfiguration != nil
	case "NotificationConfiguration":
		return props.NotificationConfiguration != nil
	case "BucketEncryption":
		return props.BucketEncryption != nil
	case "Tags":
		return props.Tags != nil
	case "CorsConfiguration":
		return props.CorsConfiguration != nil
	case "WebsiteConfiguration":
		return props.WebsiteConfiguration != nil
	default:
		return false
	}
}

func s3BucketPropertyValue(props *cfnS3BucketProperties, property string) any {
	switch property {
	case "LifecycleConfiguration":
		return props.LifecycleConfiguration
	case "VersioningConfiguration":
		return props.VersioningConfiguration
	case "NotificationConfiguration":
		return props.NotificationConfiguration
	case "BucketEncryption":
		return props.BucketEncryption
	case "Tags":
		return props.Tags
	case "CorsConfiguration":
		return props.CorsConfiguration
	case "WebsiteConfiguration":
		return props.WebsiteConfiguration
	default:
		return nil
	}
}
