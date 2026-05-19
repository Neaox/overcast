package appsync

import (
	"encoding/json"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ─── GraphQL API ─────────────────────────────────────────────────────────────

// GraphqlAPI represents an AppSync GraphQL API.
// JSON field names match the AWS AppSync REST-JSON wire format exactly.
type GraphqlAPI struct {
	ApiId              string `json:"apiId"`
	Name               string `json:"name"`
	ARN                string `json:"arn"`
	AuthenticationType string `json:"authenticationType"`
	ApiType            string `json:"apiType,omitempty"`
	Visibility         string `json:"visibility,omitempty"`
	XrayEnabled        bool   `json:"xrayEnabled"`
	OwnerContact       string `json:"ownerContact,omitempty"`
	Owner              string `json:"owner,omitempty"`
	WafWebAclArn       string `json:"wafWebAclArn,omitempty"`

	MergedApiExecutionRoleArn string `json:"mergedApiExecutionRoleArn,omitempty"`
	IntrospectionConfig       string `json:"introspectionConfig,omitempty"`
	QueryDepthLimit           int    `json:"queryDepthLimit,omitempty"`
	ResolverCountLimit        int    `json:"resolverCountLimit,omitempty"`

	Uris map[string]string `json:"uris,omitempty"`
	Dns  map[string]string `json:"dns,omitempty"`
	Tags map[string]string `json:"tags,omitempty"`

	// Complex nested configs stored as raw JSON for zero-cost passthrough.
	// This avoids deep Go struct definitions and automatically handles
	// any new fields AWS adds to these objects.
	LogConfig                         json.RawMessage `json:"logConfig,omitempty"`
	UserPoolConfig                    json.RawMessage `json:"userPoolConfig,omitempty"`
	OpenIDConnectConfig               json.RawMessage `json:"openIDConnectConfig,omitempty"`
	LambdaAuthorizerConfig            json.RawMessage `json:"lambdaAuthorizerConfig,omitempty"`
	AdditionalAuthenticationProviders json.RawMessage `json:"additionalAuthenticationProviders,omitempty"`
	EnhancedMetricsConfig             json.RawMessage `json:"enhancedMetricsConfig,omitempty"`
}

// ─── API Key ─────────────────────────────────────────────────────────────────

// ApiKey represents an AppSync API key for API_KEY authentication.
type ApiKey struct {
	Id          string `json:"id"`
	Description string `json:"description,omitempty"`
	Expires     int64  `json:"expires"`
	Deletes     int64  `json:"deletes"`
}

// ─── Data Source ─────────────────────────────────────────────────────────────

// DataSource represents a backend data source attached to a GraphQL API.
type DataSource struct {
	DataSourceArn  string `json:"dataSourceArn"`
	Name           string `json:"name"`
	ApiId          string `json:"apiId,omitempty"`
	Type           string `json:"type"`
	Description    string `json:"description,omitempty"`
	ServiceRoleArn string `json:"serviceRoleArn,omitempty"`

	// Backend-specific configs stored as raw JSON.
	DynamodbConfig           json.RawMessage `json:"dynamodbConfig,omitempty"`
	LambdaConfig             json.RawMessage `json:"lambdaConfig,omitempty"`
	HttpConfig               json.RawMessage `json:"httpConfig,omitempty"`
	ElasticsearchConfig      json.RawMessage `json:"elasticsearchConfig,omitempty"`
	OpenSearchServiceConfig  json.RawMessage `json:"openSearchServiceConfig,omitempty"`
	RelationalDatabaseConfig json.RawMessage `json:"relationalDatabaseConfig,omitempty"`
	EventBridgeConfig        json.RawMessage `json:"eventBridgeConfig,omitempty"`
	MetricsConfig            json.RawMessage `json:"metricsConfig,omitempty"`
}

// ─── Function ────────────────────────────────────────────────────────────────

// FunctionConfiguration represents a resolver function (used in pipeline resolvers).
type FunctionConfiguration struct {
	FunctionId              string `json:"functionId"`
	FunctionArn             string `json:"functionArn"`
	Name                    string `json:"name"`
	ApiId                   string `json:"apiId,omitempty"`
	DataSourceName          string `json:"dataSourceName,omitempty"`
	Description             string `json:"description,omitempty"`
	RequestMappingTemplate  string `json:"requestMappingTemplate,omitempty"`
	ResponseMappingTemplate string `json:"responseMappingTemplate,omitempty"`
	FunctionVersion         string `json:"functionVersion,omitempty"`
	MaxBatchSize            int    `json:"maxBatchSize,omitempty"`
	Code                    string `json:"code,omitempty"`

	Runtime    json.RawMessage `json:"runtime,omitempty"`
	SyncConfig json.RawMessage `json:"syncConfig,omitempty"`
}

// ─── Type Definition ─────────────────────────────────────────────────────────

// TypeDefinition represents a GraphQL type definition within an API.
// Used by the Types API (CreateType, GetType, ListTypes, UpdateType, DeleteType)
// to allow programmatic type creation and introspection.
type TypeDefinition struct {
	Name        string `json:"name"`
	Arn         string `json:"arn"`
	Description string `json:"description,omitempty"`
	Definition  string `json:"definition"`
	Format      string `json:"format"` // SDL or JSON
}

// ─── Resolver ────────────────────────────────────────────────────────────────

// Resolver represents a GraphQL field resolver (UNIT or PIPELINE).
type Resolver struct {
	TypeName                string `json:"typeName"`
	FieldName               string `json:"fieldName"`
	ResolverArn             string `json:"resolverArn"`
	ApiId                   string `json:"apiId,omitempty"`
	DataSourceName          string `json:"dataSourceName,omitempty"`
	RequestMappingTemplate  string `json:"requestMappingTemplate,omitempty"`
	ResponseMappingTemplate string `json:"responseMappingTemplate,omitempty"`
	Kind                    string `json:"kind,omitempty"`
	MaxBatchSize            int    `json:"maxBatchSize,omitempty"`
	Code                    string `json:"code,omitempty"`

	PipelineConfig json.RawMessage `json:"pipelineConfig,omitempty"`
	Runtime        json.RawMessage `json:"runtime,omitempty"`
	SyncConfig     json.RawMessage `json:"syncConfig,omitempty"`
	CachingConfig  json.RawMessage `json:"cachingConfig,omitempty"`
	MetricsConfig  json.RawMessage `json:"metricsConfig,omitempty"`
}

// ─── Schema ──────────────────────────────────────────────────────────────────

// Schema holds a GraphQL schema definition for an API.
type Schema struct {
	ApiId      string `json:"apiId"`
	Definition []byte `json:"definition"` // Raw SDL bytes.
	Status     string `json:"status"`     // ACTIVE, PROCESSING, FAILED, etc.
}

// ─── Domain Name ─────────────────────────────────────────────────────────────

// DomainNameConfig represents a custom domain name registered with AppSync.
type DomainNameConfig struct {
	DomainName        string `json:"domainName"`
	Description       string `json:"description,omitempty"`
	CertificateArn    string `json:"certificateArn"`
	AppsyncDomainName string `json:"appsyncDomainName,omitempty"` // Generated: d-xxxxx.appsync-api.region.amazonaws.com
	HostedZoneId      string `json:"hostedZoneId,omitempty"`      // Synthetic hosted zone ID.
}

// ─── API Association ─────────────────────────────────────────────────────────

// ApiAssociation maps a custom domain name to a GraphQL API.
type ApiAssociation struct {
	DomainName        string `json:"domainName"`
	ApiId             string `json:"apiId"`
	AssociationStatus string `json:"associationStatus"` // PROCESSING, SUCCESS, FAILED.
	DeploymentDetail  string `json:"deploymentDetail,omitempty"`
}

// ─── API Cache ───────────────────────────────────────────────────────────────

// ApiCacheConfig represents the caching configuration for a GraphQL API.
type ApiCacheConfig struct {
	ApiId                    string `json:"apiId,omitempty"`
	Type                     string `json:"type"`               // T2_SMALL, T2_MEDIUM, etc.
	ApiCachingBehavior       string `json:"apiCachingBehavior"` // FULL_REQUEST_CACHING, PER_RESOLVER_CACHING.
	TransitEncryptionEnabled bool   `json:"transitEncryptionEnabled"`
	AtRestEncryptionEnabled  bool   `json:"atRestEncryptionEnabled"`
	Ttl                      int64  `json:"ttl"`
	Status                   string `json:"status,omitempty"` // AVAILABLE, CREATING, DELETING, FAILED, etc.
	HealthMetricsConfig      string `json:"healthMetricsConfig,omitempty"`
}

// ─── Source API Association (Merged APIs) ────────────────────────────────────

// SourceApiAssociation links a source API to a merged API.
type SourceApiAssociation struct {
	AssociationId  string `json:"associationId"`
	AssociationArn string `json:"associationArn,omitempty"`
	Description    string `json:"description,omitempty"`
	SourceApiId    string `json:"sourceApiId"`
	SourceApiArn   string `json:"sourceApiArn,omitempty"`
	MergedApiId    string `json:"mergedApiId"`
	MergedApiArn   string `json:"mergedApiArn,omitempty"`

	SourceApiAssociationConfig       json.RawMessage `json:"sourceApiAssociationConfig,omitempty"` // {"mergeType": "MANUAL_MERGE"|"AUTO_MERGE"}
	SourceApiAssociationStatus       string          `json:"sourceApiAssociationStatus,omitempty"` // MERGE_SCHEDULED, MERGE_IN_PROGRESS, MERGE_SUCCESS, MERGE_FAILED, etc.
	SourceApiAssociationStatusDetail string          `json:"sourceApiAssociationStatusDetail,omitempty"`
	LastSuccessfulMergeDate          int64           `json:"lastSuccessfulMergeDate,omitempty"` // Unix epoch seconds.
}

// ─── Event API ───────────────────────────────────────────────────────────────

// EventApi represents an AppSync Event API (separate from GraphQL APIs).
// Event APIs provide pub/sub messaging over WebSockets via channel namespaces.
type EventApi struct {
	ApiId        string            `json:"apiId"`
	Name         string            `json:"name"`
	ApiArn       string            `json:"apiArn,omitempty"`
	Dns          map[string]string `json:"dns,omitempty"`
	OwnerContact string            `json:"ownerContact,omitempty"`
	Tags         map[string]string `json:"tags,omitempty"`
	WafWebAclArn string            `json:"wafWebAclArn,omitempty"`
	XrayEnabled  bool              `json:"xrayEnabled"`
	Created      string            `json:"created,omitempty"`

	// EventConfig stored as raw JSON for zero-cost passthrough.
	EventConfig json.RawMessage `json:"eventConfig,omitempty"`
}

// ─── Channel Namespace ───────────────────────────────────────────────────────

// ChannelNamespace represents a channel namespace within an Event API.
type ChannelNamespace struct {
	ApiId               string            `json:"apiId,omitempty"`
	Name                string            `json:"name"`
	ChannelNamespaceArn string            `json:"channelNamespaceArn,omitempty"`
	CodeHandlers        string            `json:"codeHandlers,omitempty"`
	Tags                map[string]string `json:"tags,omitempty"`
	Created             string            `json:"created,omitempty"`
	LastModified        string            `json:"lastModified,omitempty"`

	// Complex nested configs stored as raw JSON.
	PublishAuthModes   json.RawMessage `json:"publishAuthModes,omitempty"`
	SubscribeAuthModes json.RawMessage `json:"subscribeAuthModes,omitempty"`
	HandlerConfigs     json.RawMessage `json:"handlerConfigs,omitempty"`
}

// ─── Environment Variables ───────────────────────────────────────────────────

// EnvironmentVariables holds the key-value pairs for an API's environment variables.
type EnvironmentVariables struct {
	ApiId                string            `json:"apiId"`
	EnvironmentVariables map[string]string `json:"environmentVariables"`
}

// ─── Error constructors ──────────────────────────────────────────────────────

// notFoundError returns a NotFoundException for the given resource.
func notFoundError(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "NotFoundException", Message: msg, HTTPStatus: http.StatusNotFound}
}

// conflictError returns a ConcurrentModificationException.
func conflictError(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ConcurrentModificationException", Message: msg, HTTPStatus: http.StatusConflict}
}

// badRequestError returns a BadRequestException.
func badRequestError(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "BadRequestException", Message: msg, HTTPStatus: http.StatusBadRequest}
}

// unauthorizedError returns an UnauthorizedException for failed authentication.
func unauthorizedError(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "UnauthorizedException", Message: msg, HTTPStatus: http.StatusUnauthorized}
}
