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

// s3TransitionDefaultMinimumHeader is the S3 request header
// LifecycleConfiguration.TransitionDefaultMinimumObjectSize maps to.
const s3TransitionDefaultMinimumHeader = "x-amz-transition-default-minimum-object-size"

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
	BucketName                       string                               `json:"BucketName,omitempty"`
	LifecycleConfiguration           *cfnS3LifecycleConfiguration         `json:"LifecycleConfiguration,omitempty"`
	VersioningConfiguration          *cfnS3VersioningConfiguration        `json:"VersioningConfiguration,omitempty"`
	NotificationConfiguration        *cfnS3NotificationConfiguration      `json:"NotificationConfiguration,omitempty"`
	BucketEncryption                 *cfnS3BucketEncryption               `json:"BucketEncryption,omitempty"`
	Tags                             *[]cfnS3Tag                          `json:"Tags,omitempty"`
	CorsConfiguration                *cfnS3CorsConfiguration              `json:"CorsConfiguration,omitempty"`
	WebsiteConfiguration             *cfnS3WebsiteConfiguration           `json:"WebsiteConfiguration,omitempty"`
	AccelerateConfiguration          json.RawMessage                      `json:"AccelerateConfiguration,omitempty"`
	AccessControl                    *string                              `json:"AccessControl,omitempty"`
	AnalyticsConfigurations          json.RawMessage                      `json:"AnalyticsConfigurations,omitempty"`
	IntelligentTieringConfigurations json.RawMessage                      `json:"IntelligentTieringConfigurations,omitempty"`
	InventoryConfigurations          json.RawMessage                      `json:"InventoryConfigurations,omitempty"`
	LoggingConfiguration             json.RawMessage                      `json:"LoggingConfiguration,omitempty"`
	MetricsConfigurations            json.RawMessage                      `json:"MetricsConfigurations,omitempty"`
	ObjectLockConfiguration          json.RawMessage                      `json:"ObjectLockConfiguration,omitempty"`
	ObjectLockEnabled                json.RawMessage                      `json:"ObjectLockEnabled,omitempty"`
	OwnershipControls                json.RawMessage                      `json:"OwnershipControls,omitempty"`
	PublicAccessBlockConfiguration   *cfnS3PublicAccessBlockConfiguration `json:"PublicAccessBlockConfiguration,omitempty"`
	ReplicationConfiguration         json.RawMessage                      `json:"ReplicationConfiguration,omitempty"`
}

type cfnS3LifecycleConfiguration struct {
	Rules []cfnS3LifecycleRule `json:"Rules"`
	// TransitionDefaultMinimumObjectSize maps to S3's
	// x-amz-transition-default-minimum-object-size request header rather than
	// to anything in the body, so it travels as an operation header. S3 owns
	// the enum validation.
	TransitionDefaultMinimumObjectSize *string `json:"TransitionDefaultMinimumObjectSize,omitempty"`
}

type cfnS3LifecycleRule struct {
	AbortIncompleteMultipartUpload    *cfnS3AbortIncompleteMultipartUpload `json:"AbortIncompleteMultipartUpload,omitempty"`
	ExpirationDate                    *string                              `json:"ExpirationDate,omitempty"`
	ExpirationInDays                  *cfnS3Int                            `json:"ExpirationInDays,omitempty"`
	ExpiredObjectDeleteMarker         *bool                                `json:"ExpiredObjectDeleteMarker,omitempty"`
	ID                                string                               `json:"Id,omitempty"`
	NoncurrentVersionExpiration       *cfnS3NoncurrentVersionExpiration    `json:"NoncurrentVersionExpiration,omitempty"`
	NoncurrentVersionExpirationInDays *cfnS3Int                            `json:"NoncurrentVersionExpirationInDays,omitempty"`
	NoncurrentVersionTransition       *cfnS3NoncurrentVersionTransition    `json:"NoncurrentVersionTransition,omitempty"`
	NoncurrentVersionTransitions      *[]cfnS3NoncurrentVersionTransition  `json:"NoncurrentVersionTransitions,omitempty"`
	ObjectSizeGreaterThan             *string                              `json:"ObjectSizeGreaterThan,omitempty"`
	ObjectSizeLessThan                *string                              `json:"ObjectSizeLessThan,omitempty"`
	Prefix                            *string                              `json:"Prefix,omitempty"`
	Status                            string                               `json:"Status"`
	TagFilters                        []cfnS3TagFilter                     `json:"TagFilters,omitempty"`
	Transition                        *cfnS3Transition                     `json:"Transition,omitempty"`
	Transitions                       *[]cfnS3Transition                   `json:"Transitions,omitempty"`
}

type cfnS3AbortIncompleteMultipartUpload struct {
	DaysAfterInitiation cfnS3Int `json:"DaysAfterInitiation"`
}

type cfnS3NoncurrentVersionExpiration struct {
	NoncurrentDays          cfnS3Int  `json:"NoncurrentDays"`
	NewerNoncurrentVersions *cfnS3Int `json:"NewerNoncurrentVersions,omitempty"`
}

// cfnS3NoncurrentVersionTransition is CloudFormation's
// NoncurrentVersionTransition, which carries the same three members S3's own
// element does.
type cfnS3NoncurrentVersionTransition struct {
	StorageClass            string    `json:"StorageClass"`
	TransitionInDays        cfnS3Int  `json:"TransitionInDays"`
	NewerNoncurrentVersions *cfnS3Int `json:"NewerNoncurrentVersions,omitempty"`
}

type cfnS3TagFilter struct {
	Key   string `json:"Key"`
	Value string `json:"Value"`
}

type cfnS3Transition struct {
	StorageClass     string    `json:"StorageClass"`
	TransitionDate   *string   `json:"TransitionDate,omitempty"`
	TransitionInDays *cfnS3Int `json:"TransitionInDays,omitempty"`
}

type cfnS3VersioningConfiguration struct {
	Status string `json:"Status"`
}

type cfnS3NotificationConfiguration struct {
	EventBridgeConfiguration *cfnS3EventBridgeConfiguration `json:"EventBridgeConfiguration,omitempty"`
	LambdaConfigurations     []cfnS3LambdaConfiguration     `json:"LambdaConfigurations,omitempty"`
	QueueConfigurations      []cfnS3QueueConfiguration      `json:"QueueConfigurations,omitempty"`
	TopicConfigurations      []cfnS3TopicConfiguration      `json:"TopicConfigurations,omitempty"`
}

// cfnS3EventBridgeConfiguration is CloudFormation's EventBridgeConfiguration.
// EventBridgeEnabled is required and CloudFormation accepts only true, so a
// present property always means "on"; S3's own API models the same thing as
// the presence of an empty element.
type cfnS3EventBridgeConfiguration struct {
	EventBridgeEnabled bool `json:"EventBridgeEnabled"`
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
	MaxAge         cfnS3Int `json:"MaxAge,omitempty"`
}

// cfnS3Int accepts both JSON numbers and the numeric strings produced when a
// CloudFormation Number parameter is resolved. It is a shape adapter only;
// range and semantic validation still belongs to S3.
type cfnS3Int int

func (n *cfnS3Int) UnmarshalJSON(data []byte) error {
	var number json.Number
	if len(data) > 0 && data[0] == '"' {
		var value string
		if err := json.Unmarshal(data, &value); err != nil {
			return err
		}
		number = json.Number(value)
	} else {
		number = json.Number(string(data))
	}
	value, err := strconv.ParseInt(number.String(), 10, 64)
	if err != nil {
		return fmt.Errorf("%q is not an integer", number.String())
	}
	*n = cfnS3Int(value)
	return nil
}

type cfnS3WebsiteConfiguration struct {
	ErrorDocument         string                      `json:"ErrorDocument,omitempty"`
	IndexDocument         string                      `json:"IndexDocument,omitempty"`
	RedirectAllRequestsTo *cfnS3RedirectAllRequestsTo `json:"RedirectAllRequestsTo,omitempty"`
	RoutingRules          []cfnS3WebsiteRoutingRule   `json:"RoutingRules,omitempty"`
}

type cfnS3RedirectAllRequestsTo struct {
	HostName string `json:"HostName"`
	Protocol string `json:"Protocol,omitempty"`
}

// cfnS3WebsiteRoutingRule is CloudFormation's spelling of S3's RoutingRule:
// the condition and redirect containers carry different names from the API's
// Condition and Redirect.
type cfnS3WebsiteRoutingRule struct {
	RedirectRule         cfnS3WebsiteRedirectRule   `json:"RedirectRule"`
	RoutingRuleCondition *cfnS3RoutingRuleCondition `json:"RoutingRuleCondition,omitempty"`
}

type cfnS3WebsiteRedirectRule struct {
	HostName             string `json:"HostName,omitempty"`
	HttpRedirectCode     string `json:"HttpRedirectCode,omitempty"`
	Protocol             string `json:"Protocol,omitempty"`
	ReplaceKeyPrefixWith string `json:"ReplaceKeyPrefixWith,omitempty"`
	ReplaceKeyWith       string `json:"ReplaceKeyWith,omitempty"`
}

type cfnS3RoutingRuleCondition struct {
	HttpErrorCodeReturnedEquals string `json:"HttpErrorCodeReturnedEquals,omitempty"`
	KeyPrefixEquals             string `json:"KeyPrefixEquals,omitempty"`
}

type cfnS3PublicAccessBlockConfiguration struct {
	BlockPublicAcls       *bool `json:"BlockPublicAcls,omitempty"`
	BlockPublicPolicy     *bool `json:"BlockPublicPolicy,omitempty"`
	IgnorePublicAcls      *bool `json:"IgnorePublicAcls,omitempty"`
	RestrictPublicBuckets *bool `json:"RestrictPublicBuckets,omitempty"`
}

type s3BucketOperation struct {
	api         string
	method      string
	query       string
	contentType string
	body        []byte
	// headers carries operation parameters S3 models as request headers rather
	// than in the body. Nil for every sub-resource that has none.
	headers http.Header
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
	unsupported := []struct {
		name string
		raw  json.RawMessage
	}{
		{"AccelerateConfiguration", decoded.AccelerateConfiguration},
		{"AnalyticsConfigurations", decoded.AnalyticsConfigurations},
		{"IntelligentTieringConfigurations", decoded.IntelligentTieringConfigurations},
		{"InventoryConfigurations", decoded.InventoryConfigurations},
		{"LoggingConfiguration", decoded.LoggingConfiguration},
		{"MetricsConfigurations", decoded.MetricsConfigurations},
		{"ObjectLockConfiguration", decoded.ObjectLockConfiguration},
		{"ObjectLockEnabled", decoded.ObjectLockEnabled},
		{"OwnershipControls", decoded.OwnershipControls},
		{"ReplicationConfiguration", decoded.ReplicationConfiguration},
	}
	for _, property := range unsupported {
		if len(property.raw) != 0 {
			return nil, fmt.Errorf("AWS::S3::Bucket property %s is not supported", property.name)
		}
	}
	// CreateBucket already gives a new bucket the private ACL that AWS applies
	// for an omitted AccessControl property. Accepting the explicit equivalent
	// therefore needs no CloudFormation-owned state or lifecycle logic. Other
	// canned ACLs require S3's PutBucketAcl implementation so they fail loudly
	// instead of claiming a state the underlying service cannot observe.
	if decoded.AccessControl != nil && *decoded.AccessControl != "Private" {
		return nil, fmt.Errorf("AWS::S3::Bucket AccessControl %q is not supported", *decoded.AccessControl)
	}
	// AWS enables all four bucket-level public-access protections on new
	// general-purpose buckets. CDK bootstrap states that default explicitly.
	// Accept only the exact default until S3 owns the public-access-block APIs;
	// any other shape would claim an observable configuration we cannot expose.
	if block := decoded.PublicAccessBlockConfiguration; block != nil &&
		(!cfnS3True(block.BlockPublicAcls) ||
			!cfnS3True(block.BlockPublicPolicy) ||
			!cfnS3True(block.IgnorePublicAcls) ||
			!cfnS3True(block.RestrictPublicBuckets)) {
		return nil, fmt.Errorf("AWS::S3::Bucket PublicAccessBlockConfiguration is not supported unless all settings are true")
	}
	return &decoded, nil
}

func cfnS3True(value *bool) bool {
	return value != nil && *value
}

func planS3BucketOperations(props, oldProps *cfnS3BucketProperties) ([]s3BucketOperation, error) {
	bodies, err := marshalS3BucketPropertyBodies(props)
	if err != nil {
		return nil, err
	}
	headers := s3BucketPropertyHeaders(props)

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
				headers: headers[subresource.property],
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
		var extra []http.Header
		if operation.headers != nil {
			extra = append(extra, operation.headers)
		}
		if _, err := internalS3Request(ctx, router, region, operation.method, "/"+bucket+"?"+operation.query, operation.contentType, operation.body, extra...); err != nil {
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

// s3BucketPropertyHeaders returns, per property, the S3 request headers its
// Put operation needs. Only LifecycleConfiguration has one today.
func s3BucketPropertyHeaders(props *cfnS3BucketProperties) map[string]http.Header {
	lifecycle := props.LifecycleConfiguration
	if lifecycle == nil || lifecycle.TransitionDefaultMinimumObjectSize == nil {
		return nil
	}
	header := http.Header{}
	header.Set(s3TransitionDefaultMinimumHeader, *lifecycle.TransitionDefaultMinimumObjectSize)
	return map[string]http.Header{"LifecycleConfiguration": header}
}

func translateS3LifecycleConfiguration(in *cfnS3LifecycleConfiguration) (*s3service.LifecycleConfiguration, error) {
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
	if in.NoncurrentVersionTransition != nil && in.NoncurrentVersionTransitions != nil {
		return s3service.LifecycleRule{}, fmt.Errorf("NoncurrentVersionTransition and NoncurrentVersionTransitions are mutually exclusive")
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
		rule.Expiration = &s3service.LifecycleExpiration{Days: int(*in.ExpirationInDays)}
	}
	// CloudFormation hangs ExpiredObjectDeleteMarker off the rule, while S3
	// nests it inside Expiration and refuses it beside an age. Translate the
	// shape and let S3 decide whether the combination is legal.
	if in.ExpiredObjectDeleteMarker != nil && *in.ExpiredObjectDeleteMarker {
		if rule.Expiration == nil {
			rule.Expiration = &s3service.LifecycleExpiration{}
		}
		rule.Expiration.ExpiredObjectDeleteMarker = true
	}
	if in.NoncurrentVersionExpiration != nil && in.NoncurrentVersionExpirationInDays != nil {
		return s3service.LifecycleRule{}, fmt.Errorf("NoncurrentVersionExpiration and NoncurrentVersionExpirationInDays are mutually exclusive")
	}
	if in.NoncurrentVersionExpiration != nil {
		noncurrent := &s3service.LifecycleNoncurrentVersionExpiration{
			NoncurrentDays: int(in.NoncurrentVersionExpiration.NoncurrentDays),
		}
		if in.NoncurrentVersionExpiration.NewerNoncurrentVersions != nil {
			newer := int(*in.NoncurrentVersionExpiration.NewerNoncurrentVersions)
			noncurrent.NewerNoncurrentVersions = &newer
			// The S3 API requires a Filter when a retained-version count is
			// supplied. CloudFormation exposes Prefix as a rule property, so
			// express the same predicate in S3's current Filter form.
			if rule.Filter == nil && rule.Prefix != nil {
				rule.Filter = &s3service.LifecycleFilter{Prefix: *rule.Prefix}
				rule.Prefix = nil
			}
		}
		rule.NoncurrentVersionExpiration = noncurrent
	} else if in.NoncurrentVersionExpirationInDays != nil {
		rule.NoncurrentVersionExpiration = &s3service.LifecycleNoncurrentVersionExpiration{
			NoncurrentDays: int(*in.NoncurrentVersionExpirationInDays),
		}
	}
	if in.AbortIncompleteMultipartUpload != nil {
		rule.AbortIncompleteMultipartUpload = &s3service.LifecycleAbortMPU{
			DaysAfterInitiation: int(in.AbortIncompleteMultipartUpload.DaysAfterInitiation),
		}
	}

	noncurrentTransitions := make([]cfnS3NoncurrentVersionTransition, 0, 1)
	if in.NoncurrentVersionTransition != nil {
		noncurrentTransitions = append(noncurrentTransitions, *in.NoncurrentVersionTransition)
	}
	if in.NoncurrentVersionTransitions != nil {
		noncurrentTransitions = append(noncurrentTransitions, (*in.NoncurrentVersionTransitions)...)
	}
	for i := range noncurrentTransitions {
		nct := &noncurrentTransitions[i]
		out := s3service.LifecycleNoncurrentVersionTransition{
			NoncurrentDays: int(nct.TransitionInDays),
			StorageClass:   nct.StorageClass,
		}
		if nct.NewerNoncurrentVersions != nil {
			newer := int(*nct.NewerNoncurrentVersions)
			out.NewerNoncurrentVersions = &newer
			// Same Filter requirement S3 puts on the expiration action above.
			if rule.Filter == nil && rule.Prefix != nil {
				rule.Filter = &s3service.LifecycleFilter{Prefix: *rule.Prefix}
				rule.Prefix = nil
			}
		}
		rule.NoncurrentVersionTransitions = append(rule.NoncurrentVersionTransitions, out)
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
		out.Days = int(*in.TransitionInDays)
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
	out := &s3service.NotificationConfig{}
	if eb := in.EventBridgeConfiguration; eb != nil {
		// CloudFormation's schema allows only true. An explicit false has no
		// S3 API spelling — the element's absence is what turns delivery off —
		// so refuse it rather than silently enabling delivery.
		if !eb.EventBridgeEnabled {
			return nil, fmt.Errorf("EventBridgeConfiguration.EventBridgeEnabled must be true")
		}
		out.EventBridgeConfiguration = &s3service.EventBridgeNotificationConfig{}
	}
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
			MaxAgeSeconds:  int(rule.MaxAge),
		})
	}
	return out, nil
}

// translateS3WebsiteConfiguration maps CloudFormation's spelling onto S3's.
// The exclusion between RedirectAllRequestsTo and the index/error/routing form
// is S3's to enforce, so it is not repeated here.
func translateS3WebsiteConfiguration(in *cfnS3WebsiteConfiguration) (*s3service.WebsiteConfiguration, error) {
	out := &s3service.WebsiteConfiguration{
		IndexDocument: in.IndexDocument,
		ErrorDocument: in.ErrorDocument,
	}
	if in.RedirectAllRequestsTo != nil {
		out.RedirectAllRequestsTo = &s3service.WebsiteRedirectAll{
			HostName: in.RedirectAllRequestsTo.HostName,
			Protocol: in.RedirectAllRequestsTo.Protocol,
		}
	}
	for i := range in.RoutingRules {
		rule := &in.RoutingRules[i]
		translated := s3service.WebsiteRoutingRule{
			Redirect: s3service.WebsiteRedirect{
				HostName:             rule.RedirectRule.HostName,
				HTTPRedirectCode:     rule.RedirectRule.HttpRedirectCode,
				Protocol:             rule.RedirectRule.Protocol,
				ReplaceKeyPrefixWith: rule.RedirectRule.ReplaceKeyPrefixWith,
				ReplaceKeyWith:       rule.RedirectRule.ReplaceKeyWith,
			},
		}
		if condition := rule.RoutingRuleCondition; condition != nil {
			translated.Condition = &s3service.WebsiteRoutingCondition{
				HTTPErrorCodeReturnedEquals: condition.HttpErrorCodeReturnedEquals,
				KeyPrefixEquals:             condition.KeyPrefixEquals,
			}
		}
		out.RoutingRules = append(out.RoutingRules, translated)
	}
	return out, nil
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
