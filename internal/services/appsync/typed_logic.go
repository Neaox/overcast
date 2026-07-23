package appsync

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/Neaox/overcast/internal/protocol"
)

// ─── GraphQL APIs ─────────────────────────────────────────────────────────────

type createGraphqlApiRequest struct {
	Name                      string            `json:"name" cbor:"name"`
	AuthenticationType        string            `json:"authenticationType" cbor:"authenticationType"`
	ApiType                   string            `json:"apiType" cbor:"apiType"`
	Visibility                string            `json:"visibility" cbor:"visibility"`
	IntrospectionConfig       string            `json:"introspectionConfig" cbor:"introspectionConfig"`
	MergedApiExecutionRoleArn string            `json:"mergedApiExecutionRoleArn" cbor:"mergedApiExecutionRoleArn"`
	QueryDepthLimit           int               `json:"queryDepthLimit" cbor:"queryDepthLimit"`
	ResolverCountLimit        int               `json:"resolverCountLimit" cbor:"resolverCountLimit"`
	OwnerContact              string            `json:"ownerContact" cbor:"ownerContact"`
	WafWebAclArn              string            `json:"wafWebAclArn" cbor:"wafWebAclArn"`
	XrayEnabled               bool              `json:"xrayEnabled" cbor:"xrayEnabled"`
	Tags                      map[string]string `json:"tags" cbor:"tags"`
}

type createGraphqlApiResponse struct {
	GraphqlApi *GraphqlAPI `json:"graphqlApi" cbor:"graphqlApi"`
}

func (h *Handler) createGraphqlApiTyped(ctx context.Context, req *createGraphqlApiRequest) (*createGraphqlApiResponse, *protocol.AWSError) {
	apiID := uuid.NewString()
	api := &GraphqlAPI{
		ApiId: apiID, Name: req.Name, AuthenticationType: req.AuthenticationType,
		ApiType: req.ApiType, Visibility: req.Visibility, Tags: req.Tags,
		OwnerContact:              req.OwnerContact,
		WafWebAclArn:              req.WafWebAclArn,
		XrayEnabled:               req.XrayEnabled,
		IntrospectionConfig:       req.IntrospectionConfig,
		MergedApiExecutionRoleArn: req.MergedApiExecutionRoleArn,
		QueryDepthLimit:           req.QueryDepthLimit, ResolverCountLimit: req.ResolverCountLimit,
		ARN:   protocol.ARN(h.regionCtx(ctx), h.cfg.AccountID, "appsync", "apis/"+apiID),
		Owner: h.cfg.AccountID,
	}
	if err := validateGraphqlAPIInput(api, true); err != nil {
		return nil, err
	}
	if api.ApiType == "" {
		api.ApiType = "GRAPHQL"
	}
	if api.Visibility == "" {
		api.Visibility = "GLOBAL"
	}
	if api.IntrospectionConfig == "" {
		api.IntrospectionConfig = "ENABLED"
	}
	api.Uris = localGraphQLURIs(h.baseURLFromContext(ctx), apiID)
	api.Dns = map[string]string{
		"GRAPHQL":  fmt.Sprintf("%s.appsync-api.%s.amazonaws.com", apiID, h.cfg.Region),
		"REALTIME": fmt.Sprintf("%s.appsync-realtime-api.%s.amazonaws.com", apiID, h.cfg.Region),
	}
	if err := h.store.PutAPI(ctx, api); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api.AuthenticationType == "API_KEY" {
		expires, _ := normalizeAPIKeyExpires(h.clk.Now(), 0)
		key := &ApiKey{Id: generateAPIKeyID(), Expires: expires, Deletes: apiKeyDeletes(expires)}
		_ = h.store.PutApiKey(ctx, apiID, key)
	}
	return &createGraphqlApiResponse{GraphqlApi: api}, nil
}

type getGraphqlApiRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type getGraphqlApiResponse struct {
	GraphqlApi *GraphqlAPI `json:"graphqlApi" cbor:"graphqlApi"`
}

func (h *Handler) getGraphqlApiTyped(ctx context.Context, req *getGraphqlApiRequest) (*getGraphqlApiResponse, *protocol.AWSError) {
	api, err := h.store.GetAPI(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundErr("GraphQL API " + req.ApiId + " not found.")
	}
	return &getGraphqlApiResponse{GraphqlApi: api.withLocalURIs(h.baseURLFromContext(ctx))}, nil
}

type listGraphqlApisRequest struct{}

type listGraphqlApisResponse struct {
	GraphqlApis []*GraphqlAPI `json:"graphqlApis" cbor:"graphqlApis"`
}

func (h *Handler) listGraphqlApisTyped(ctx context.Context, _ *listGraphqlApisRequest) (*listGraphqlApisResponse, *protocol.AWSError) {
	apis, err := h.store.ListAPIs(ctx)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	baseURL := h.baseURLFromContext(ctx)
	for i, api := range apis {
		apis[i] = api.withLocalURIs(baseURL)
	}
	return &listGraphqlApisResponse{GraphqlApis: apis}, nil
}

type updateGraphqlApiRequest struct {
	ApiId string     `json:"apiId" cbor:"apiId"`
	Api   GraphqlAPI `json:"graphqlApi" cbor:"graphqlApi"`
}

type updateGraphqlApiResponse struct {
	GraphqlApi *GraphqlAPI `json:"graphqlApi" cbor:"graphqlApi"`
}

func (h *Handler) updateGraphqlApiTyped(ctx context.Context, req *updateGraphqlApiRequest) (*updateGraphqlApiResponse, *protocol.AWSError) {
	existing, err := h.store.GetAPI(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if existing == nil {
		return nil, notFoundErr("GraphQL API " + req.ApiId + " not found.")
	}
	update := req.Api
	if err := validateGraphqlAPIInput(&update, false); err != nil {
		return nil, err
	}
	update.ApiId = existing.ApiId
	update.ARN = existing.ARN
	update.Owner = existing.Owner
	update.Uris = existing.Uris
	update.Dns = existing.Dns
	if update.ApiType == "" {
		update.ApiType = existing.ApiType
	}
	if update.Visibility == "" {
		update.Visibility = existing.Visibility
	}
	if update.IntrospectionConfig == "" {
		update.IntrospectionConfig = existing.IntrospectionConfig
	}
	if update.Tags == nil {
		update.Tags = existing.Tags
	}
	if err := h.store.PutAPI(ctx, &update); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateGraphqlApiResponse{GraphqlApi: update.withLocalURIs(h.baseURLFromContext(ctx))}, nil
}

type deleteGraphqlApiRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

func (h *Handler) deleteGraphqlApiTyped(ctx context.Context, req *deleteGraphqlApiRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteAPIAndChildren(ctx, req.ApiId); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

// ─── Tags ─────────────────────────────────────────────────────────────────────

type tagResourceRequest struct {
	ResourceArn string            `json:"resourceArn" cbor:"resourceArn"`
	Tags        map[string]string `json:"tags" cbor:"tags"`
}

func (h *Handler) tagResourceTyped(ctx context.Context, req *tagResourceRequest) (any, *protocol.AWSError) {
	apiID := extractApiID(req.ResourceArn)
	api, err := h.store.GetAPI(ctx, apiID)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundErr("Resource not found: " + req.ResourceArn)
	}
	if len(req.Tags) == 0 {
		return nil, badRequestError("tags is required.")
	}
	if err := validateTagMap(req.Tags); err != nil {
		return nil, err
	}
	if api.Tags == nil {
		api.Tags = make(map[string]string, len(req.Tags))
	}
	for k, v := range req.Tags {
		api.Tags[k] = v
	}
	if err := h.store.PutAPI(ctx, api); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type untagResourceRequest struct {
	ResourceArn string   `json:"resourceArn" cbor:"resourceArn"`
	TagKeys     []string `json:"tagKeys" cbor:"tagKeys"`
}

func (h *Handler) untagResourceTyped(ctx context.Context, req *untagResourceRequest) (any, *protocol.AWSError) {
	apiID := extractApiID(req.ResourceArn)
	api, err := h.store.GetAPI(ctx, apiID)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundErr("Resource not found: " + req.ResourceArn)
	}
	for _, k := range req.TagKeys {
		delete(api.Tags, k)
	}
	if err := h.store.PutAPI(ctx, api); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type listTagsForResourceRequest struct {
	ResourceArn string `json:"resourceArn" cbor:"resourceArn"`
}

type listTagsForResourceResponse struct {
	Tags map[string]string `json:"tags" cbor:"tags"`
}

func (h *Handler) listTagsForResourceTyped(ctx context.Context, req *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	apiID := extractApiID(req.ResourceArn)
	api, err := h.store.GetAPI(ctx, apiID)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundErr("Resource not found: " + req.ResourceArn)
	}
	tags := api.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	return &listTagsForResourceResponse{Tags: tags}, nil
}

func extractApiID(arn string) string {
	if idx := strings.LastIndex(arn, "apis/"); idx >= 0 {
		return arn[idx+len("apis/"):]
	}
	return arn
}

// ─── Domain Names ─────────────────────────────────────────────────────────────

type createDomainNameRequest struct {
	DomainName     string `json:"domainName" cbor:"domainName"`
	CertificateArn string `json:"certificateArn" cbor:"certificateArn"`
	Description    string `json:"description" cbor:"description"`
}

type createDomainNameResponse struct {
	DomainNameConfig *DomainNameConfig `json:"domainNameConfig" cbor:"domainNameConfig"`
}

func (h *Handler) createDomainNameTyped(ctx context.Context, req *createDomainNameRequest) (*createDomainNameResponse, *protocol.AWSError) {
	dn := &DomainNameConfig{
		DomainName: req.DomainName, Description: req.Description,
		CertificateArn: req.CertificateArn,
	}
	if err := h.store.PutDomainName(ctx, dn); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createDomainNameResponse{DomainNameConfig: dn}, nil
}

type getDomainNameRequest struct {
	DomainName string `json:"domainName" cbor:"domainName"`
}

type getDomainNameResponse struct {
	DomainNameConfig *DomainNameConfig `json:"domainNameConfig" cbor:"domainNameConfig"`
}

func (h *Handler) getDomainNameTyped(ctx context.Context, req *getDomainNameRequest) (*getDomainNameResponse, *protocol.AWSError) {
	dn, err := h.store.GetDomainName(ctx, req.DomainName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if dn == nil {
		return nil, notFoundErr("Domain name " + req.DomainName + " not found.")
	}
	return &getDomainNameResponse{DomainNameConfig: dn}, nil
}

type listDomainNamesRequest struct{}

type listDomainNamesResponse struct {
	DomainNameConfigs []*DomainNameConfig `json:"domainNameConfigs" cbor:"domainNameConfigs"`
}

func (h *Handler) listDomainNamesTyped(ctx context.Context, _ *listDomainNamesRequest) (*listDomainNamesResponse, *protocol.AWSError) {
	dns, err := h.store.ListDomainNames(ctx)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listDomainNamesResponse{DomainNameConfigs: dns}, nil
}

type updateDomainNameRequest struct {
	DomainName       string           `json:"domainName" cbor:"domainName"`
	DomainNameConfig DomainNameConfig `json:"domainNameConfig" cbor:"domainNameConfig"`
}

type updateDomainNameResponse struct {
	DomainNameConfig *DomainNameConfig `json:"domainNameConfig" cbor:"domainNameConfig"`
}

func (h *Handler) updateDomainNameTyped(ctx context.Context, req *updateDomainNameRequest) (*updateDomainNameResponse, *protocol.AWSError) {
	dn := req.DomainNameConfig
	dn.DomainName = req.DomainName
	if err := h.store.PutDomainName(ctx, &dn); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateDomainNameResponse{DomainNameConfig: &dn}, nil
}

type deleteDomainNameRequest struct {
	DomainName string `json:"domainName" cbor:"domainName"`
}

func (h *Handler) deleteDomainNameTyped(ctx context.Context, req *deleteDomainNameRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteDomainName(ctx, req.DomainName); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type associateApiRequest struct {
	DomainName string `json:"domainName" cbor:"domainName"`
	ApiId      string `json:"apiId" cbor:"apiId"`
}

type associateApiResponse struct {
	ApiAssociation *ApiAssociation `json:"apiAssociation" cbor:"apiAssociation"`
}

func (h *Handler) associateApiTyped(ctx context.Context, req *associateApiRequest) (*associateApiResponse, *protocol.AWSError) {
	assoc := &ApiAssociation{DomainName: req.DomainName, ApiId: req.ApiId, AssociationStatus: "SUCCESS"}
	if err := h.store.PutApiAssociation(ctx, assoc); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &associateApiResponse{ApiAssociation: assoc}, nil
}

type getApiAssociationRequest struct {
	DomainName string `json:"domainName" cbor:"domainName"`
}

type getApiAssociationResponse struct {
	ApiAssociation *ApiAssociation `json:"apiAssociation" cbor:"apiAssociation"`
}

func (h *Handler) getApiAssociationTyped(ctx context.Context, req *getApiAssociationRequest) (*getApiAssociationResponse, *protocol.AWSError) {
	assoc, err := h.store.GetApiAssociation(ctx, req.DomainName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &getApiAssociationResponse{ApiAssociation: assoc}, nil
}

type disassociateApiRequest struct {
	DomainName string `json:"domainName" cbor:"domainName"`
}

func (h *Handler) disassociateApiTyped(ctx context.Context, req *disassociateApiRequest) (any, *protocol.AWSError) {
	_ = h.store.DeleteApiAssociation(ctx, req.DomainName)
	return struct{}{}, nil
}

// ─── Schema ───────────────────────────────────────────────────────────────────

type startSchemaCreationRequest struct {
	ApiId      string `json:"apiId" cbor:"apiId"`
	Definition string `json:"definition" cbor:"definition"`
}

type startSchemaCreationResponse struct {
	Status string `json:"status" cbor:"status"`
}

func (h *Handler) startSchemaCreationTyped(ctx context.Context, req *startSchemaCreationRequest) (*startSchemaCreationResponse, *protocol.AWSError) {
	api, err := h.store.GetAPI(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundErr("GraphQL API " + req.ApiId + " not found.")
	}
	if req.Definition == "" {
		return nil, badReqErr("definition is required")
	}
	sdl := []byte(req.Definition)
	parsed, parseErr := h.sp.Parse(sdl)
	if parseErr != nil {
		return nil, badReqErr(parseErr.Error())
	}
	h.sp.Put(req.ApiId, parsed)
	schema := &Schema{ApiId: req.ApiId, Definition: sdl, Status: "ACTIVE"}
	if err := h.store.PutSchema(ctx, schema); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &startSchemaCreationResponse{Status: "ACTIVE"}, nil
}

type getSchemaCreationStatusRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type getSchemaCreationStatusResponse struct {
	Status  string `json:"status" cbor:"status"`
	Details string `json:"details" cbor:"details"`
}

func (h *Handler) getSchemaCreationStatusTyped(ctx context.Context, req *getSchemaCreationStatusRequest) (*getSchemaCreationStatusResponse, *protocol.AWSError) {
	schema, err := h.store.GetSchema(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	status := "NOT_APPLICABLE"
	details := ""
	if schema != nil {
		status = schema.Status
		details = "Schema created successfully."
	}
	return &getSchemaCreationStatusResponse{Status: status, Details: details}, nil
}

type getIntrospectionSchemaRequest struct {
	ApiId  string `json:"apiId" cbor:"apiId"`
	Format string `json:"format" cbor:"format"`
}

type getIntrospectionSchemaResponse struct {
	Schema string `json:"schema" cbor:"schema"`
}

func (h *Handler) getIntrospectionSchemaTyped(ctx context.Context, req *getIntrospectionSchemaRequest) (*getIntrospectionSchemaResponse, *protocol.AWSError) {
	schema, err := h.store.GetSchema(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	sdl := "type Query { version: String } schema { query: Query }"
	if schema != nil && len(schema.Definition) > 0 {
		sdl = string(schema.Definition)
	}
	return &getIntrospectionSchemaResponse{Schema: sdl}, nil
}

// ─── API Keys ─────────────────────────────────────────────────────────────────

type createApiKeyRequest struct {
	ApiId       string `json:"apiId" cbor:"apiId"`
	Description string `json:"description" cbor:"description"`
	Expires     int64  `json:"expires" cbor:"expires"`
}

type createApiKeyResponse struct {
	ApiKey *ApiKey `json:"apiKey" cbor:"apiKey"`
}

func (h *Handler) createApiKeyTyped(ctx context.Context, req *createApiKeyRequest) (*createApiKeyResponse, *protocol.AWSError) {
	if _, ok := h.validateAPICtx(ctx, req.ApiId); !ok {
		return nil, notFoundErr("GraphQL API " + req.ApiId + " not found.")
	}
	expires, validationErr := normalizeAPIKeyExpires(h.clk.Now(), req.Expires)
	if validationErr != nil {
		return nil, validationErr
	}
	key := &ApiKey{Id: generateAPIKeyID(), Description: req.Description, Expires: expires, Deletes: apiKeyDeletes(expires)}
	if err := h.store.PutApiKey(ctx, req.ApiId, key); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createApiKeyResponse{ApiKey: key}, nil
}

type listApiKeysRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type listApiKeysResponse struct {
	ApiKeys []*ApiKey `json:"apiKeys" cbor:"apiKeys"`
}

func (h *Handler) listApiKeysTyped(ctx context.Context, req *listApiKeysRequest) (*listApiKeysResponse, *protocol.AWSError) {
	keys, err := h.store.ListApiKeys(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listApiKeysResponse{ApiKeys: keys}, nil
}

type updateApiKeyRequest struct {
	ApiId       string `json:"apiId" cbor:"apiId"`
	KeyId       string `json:"keyId" cbor:"keyId"`
	Description string `json:"description" cbor:"description"`
	Expires     int64  `json:"expires" cbor:"expires"`
}

type updateApiKeyResponse struct {
	ApiKey *ApiKey `json:"apiKey" cbor:"apiKey"`
}

func (h *Handler) updateApiKeyTyped(ctx context.Context, req *updateApiKeyRequest) (*updateApiKeyResponse, *protocol.AWSError) {
	key, err := h.store.GetApiKey(ctx, req.ApiId, req.KeyId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if key == nil {
		return nil, notFoundErr("API key " + req.KeyId + " not found.")
	}
	key.Description = req.Description
	if req.Expires != 0 {
		expires, validationErr := normalizeAPIKeyExpires(h.clk.Now(), req.Expires)
		if validationErr != nil {
			return nil, validationErr
		}
		key.Expires = expires
		key.Deletes = apiKeyDeletes(expires)
	}
	if err := h.store.PutApiKey(ctx, req.ApiId, key); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateApiKeyResponse{ApiKey: key}, nil
}

type deleteApiKeyRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	KeyId string `json:"keyId" cbor:"keyId"`
}

func (h *Handler) deleteApiKeyTyped(ctx context.Context, req *deleteApiKeyRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteApiKey(ctx, req.ApiId, req.KeyId); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

// ─── Data Sources ─────────────────────────────────────────────────────────────

type createDataSourceRequest struct {
	ApiId string     `json:"apiId" cbor:"apiId"`
	DS    DataSource `json:"dataSource" cbor:"dataSource"`
}

type createDataSourceResponse struct {
	DataSource *DataSource `json:"dataSource" cbor:"dataSource"`
}

func (h *Handler) createDataSourceTyped(ctx context.Context, req *createDataSourceRequest) (*createDataSourceResponse, *protocol.AWSError) {
	if _, ok := h.validateAPICtx(ctx, req.ApiId); !ok {
		return nil, notFoundErr("GraphQL API " + req.ApiId + " not found.")
	}
	ds := req.DS
	if ds.Name == "" {
		return nil, badReqErr("name is required")
	}
	existing, err := h.store.GetDataSource(ctx, req.ApiId, ds.Name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if existing != nil {
		return nil, conflictErr("Data source " + ds.Name + " already exists.")
	}
	ds.ApiId = req.ApiId
	ds.DataSourceArn = protocol.ARN(h.regionCtx(ctx), h.cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/datasources/%s", req.ApiId, ds.Name))
	if err := h.store.PutDataSource(ctx, req.ApiId, &ds); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createDataSourceResponse{DataSource: &ds}, nil
}

type getDataSourceRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Name  string `json:"name" cbor:"name"`
}

type getDataSourceResponse struct {
	DataSource *DataSource `json:"dataSource" cbor:"dataSource"`
}

func (h *Handler) getDataSourceTyped(ctx context.Context, req *getDataSourceRequest) (*getDataSourceResponse, *protocol.AWSError) {
	ds, err := h.store.GetDataSource(ctx, req.ApiId, req.Name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if ds == nil {
		return nil, notFoundErr("Data source " + req.Name + " not found.")
	}
	return &getDataSourceResponse{DataSource: ds}, nil
}

type listDataSourcesRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type listDataSourcesResponse struct {
	DataSources []*DataSource `json:"dataSources" cbor:"dataSources"`
}

func (h *Handler) listDataSourcesTyped(ctx context.Context, req *listDataSourcesRequest) (*listDataSourcesResponse, *protocol.AWSError) {
	ds, err := h.store.ListDataSources(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listDataSourcesResponse{DataSources: ds}, nil
}

type updateDataSourceRequest struct {
	ApiId string     `json:"apiId" cbor:"apiId"`
	Name  string     `json:"name" cbor:"name"`
	DS    DataSource `json:"dataSource" cbor:"dataSource"`
}

type updateDataSourceResponse struct {
	DataSource *DataSource `json:"dataSource" cbor:"dataSource"`
}

func (h *Handler) updateDataSourceTyped(ctx context.Context, req *updateDataSourceRequest) (*updateDataSourceResponse, *protocol.AWSError) {
	ds := req.DS
	ds.ApiId = req.ApiId
	ds.Name = req.Name
	if err := h.store.PutDataSource(ctx, req.ApiId, &ds); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateDataSourceResponse{DataSource: &ds}, nil
}

type deleteDataSourceRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Name  string `json:"name" cbor:"name"`
}

func (h *Handler) deleteDataSourceTyped(ctx context.Context, req *deleteDataSourceRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteDataSource(ctx, req.ApiId, req.Name); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

// ─── Functions ────────────────────────────────────────────────────────────────

type createFunctionRequest struct {
	ApiId string                `json:"apiId" cbor:"apiId"`
	Fn    FunctionConfiguration `json:"functionConfiguration" cbor:"functionConfiguration"`
}

type createFunctionResponse struct {
	FunctionConfiguration *FunctionConfiguration `json:"functionConfiguration" cbor:"functionConfiguration"`
}

func (h *Handler) createFunctionTyped(ctx context.Context, req *createFunctionRequest) (*createFunctionResponse, *protocol.AWSError) {
	if _, ok := h.validateAPICtx(ctx, req.ApiId); !ok {
		return nil, notFoundErr("GraphQL API " + req.ApiId + " not found.")
	}
	fn := req.Fn
	if fn.Name == "" {
		return nil, badReqErr("name is required")
	}
	fn.FunctionId = uuid.NewString()
	fn.ApiId = req.ApiId
	fn.FunctionArn = protocol.ARN(h.regionCtx(ctx), h.cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/functions/%s", req.ApiId, fn.FunctionId))
	if fn.FunctionVersion == "" {
		fn.FunctionVersion = "2018-05-29"
	}
	if err := h.store.PutFunction(ctx, req.ApiId, &fn); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createFunctionResponse{FunctionConfiguration: &fn}, nil
}

type getFunctionRequest struct {
	ApiId      string `json:"apiId" cbor:"apiId"`
	FunctionId string `json:"functionId" cbor:"functionId"`
}

type getFunctionResponse struct {
	FunctionConfiguration *FunctionConfiguration `json:"functionConfiguration" cbor:"functionConfiguration"`
}

func (h *Handler) getFunctionTyped(ctx context.Context, req *getFunctionRequest) (*getFunctionResponse, *protocol.AWSError) {
	fn, err := h.store.GetFunction(ctx, req.ApiId, req.FunctionId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if fn == nil {
		return nil, notFoundErr("Function " + req.FunctionId + " not found.")
	}
	return &getFunctionResponse{FunctionConfiguration: fn}, nil
}

type listFunctionsRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type listFunctionsResponse struct {
	Functions []*FunctionConfiguration `json:"functions" cbor:"functions"`
}

func (h *Handler) listFunctionsTyped(ctx context.Context, req *listFunctionsRequest) (*listFunctionsResponse, *protocol.AWSError) {
	fns, err := h.store.ListFunctions(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listFunctionsResponse{Functions: fns}, nil
}

type updateFunctionRequest struct {
	ApiId      string                `json:"apiId" cbor:"apiId"`
	FunctionId string                `json:"functionId" cbor:"functionId"`
	Fn         FunctionConfiguration `json:"functionConfiguration" cbor:"functionConfiguration"`
}

type updateFunctionResponse struct {
	FunctionConfiguration *FunctionConfiguration `json:"functionConfiguration" cbor:"functionConfiguration"`
}

func (h *Handler) updateFunctionTyped(ctx context.Context, req *updateFunctionRequest) (*updateFunctionResponse, *protocol.AWSError) {
	fn := req.Fn
	fn.ApiId = req.ApiId
	fn.FunctionId = req.FunctionId
	if err := h.store.PutFunction(ctx, req.ApiId, &fn); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateFunctionResponse{FunctionConfiguration: &fn}, nil
}

type deleteFunctionRequest struct {
	ApiId      string `json:"apiId" cbor:"apiId"`
	FunctionId string `json:"functionId" cbor:"functionId"`
}

func (h *Handler) deleteFunctionTyped(ctx context.Context, req *deleteFunctionRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteFunction(ctx, req.ApiId, req.FunctionId); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

// ─── Resolvers ────────────────────────────────────────────────────────────────

type createResolverRequest struct {
	ApiId    string   `json:"apiId" cbor:"apiId"`
	TypeName string   `json:"typeName" cbor:"typeName"`
	Resolver Resolver `json:"resolver" cbor:"resolver"`
}

type createResolverResponse struct {
	Resolver *Resolver `json:"resolver" cbor:"resolver"`
}

func (h *Handler) createResolverTyped(ctx context.Context, req *createResolverRequest) (*createResolverResponse, *protocol.AWSError) {
	r := req.Resolver
	if r.FieldName == "" {
		return nil, badReqErr("fieldName is required")
	}
	r.ApiId = req.ApiId
	r.TypeName = req.TypeName
	r.ResolverArn = protocol.ARN(h.regionCtx(ctx), h.cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/types/%s/resolvers/%s", req.ApiId, req.TypeName, r.FieldName))
	if err := h.store.PutResolver(ctx, req.ApiId, &r); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createResolverResponse{Resolver: &r}, nil
}

type getResolverRequest struct {
	ApiId     string `json:"apiId" cbor:"apiId"`
	TypeName  string `json:"typeName" cbor:"typeName"`
	FieldName string `json:"fieldName" cbor:"fieldName"`
}

type getResolverResponse struct {
	Resolver *Resolver `json:"resolver" cbor:"resolver"`
}

func (h *Handler) getResolverTyped(ctx context.Context, req *getResolverRequest) (*getResolverResponse, *protocol.AWSError) {
	r, err := h.store.GetResolver(ctx, req.ApiId, req.TypeName, req.FieldName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if r == nil {
		return nil, notFoundErr("Resolver " + req.TypeName + "." + req.FieldName + " not found.")
	}
	return &getResolverResponse{Resolver: r}, nil
}

type listResolversRequest struct {
	ApiId    string `json:"apiId" cbor:"apiId"`
	TypeName string `json:"typeName" cbor:"typeName"`
}

type listResolversResponse struct {
	Resolvers []*Resolver `json:"resolvers" cbor:"resolvers"`
}

func (h *Handler) listResolversTyped(ctx context.Context, req *listResolversRequest) (*listResolversResponse, *protocol.AWSError) {
	resolvers, err := h.store.ListResolvers(ctx, req.ApiId, req.TypeName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listResolversResponse{Resolvers: resolvers}, nil
}

type updateResolverRequest struct {
	ApiId     string   `json:"apiId" cbor:"apiId"`
	TypeName  string   `json:"typeName" cbor:"typeName"`
	FieldName string   `json:"fieldName" cbor:"fieldName"`
	Resolver  Resolver `json:"resolver" cbor:"resolver"`
}

type updateResolverResponse struct {
	Resolver *Resolver `json:"resolver" cbor:"resolver"`
}

func (h *Handler) updateResolverTyped(ctx context.Context, req *updateResolverRequest) (*updateResolverResponse, *protocol.AWSError) {
	r := req.Resolver
	r.ApiId = req.ApiId
	r.TypeName = req.TypeName
	r.FieldName = req.FieldName
	if err := h.store.PutResolver(ctx, req.ApiId, &r); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateResolverResponse{Resolver: &r}, nil
}

type deleteResolverRequest struct {
	ApiId     string `json:"apiId" cbor:"apiId"`
	TypeName  string `json:"typeName" cbor:"typeName"`
	FieldName string `json:"fieldName" cbor:"fieldName"`
}

func (h *Handler) deleteResolverTyped(ctx context.Context, req *deleteResolverRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteResolver(ctx, req.ApiId, req.TypeName, req.FieldName); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type listResolversByFunctionRequest struct {
	ApiId      string `json:"apiId" cbor:"apiId"`
	FunctionId string `json:"functionId" cbor:"functionId"`
}

type listResolversByFunctionResponse struct {
	Resolvers []*Resolver `json:"resolvers" cbor:"resolvers"`
}

func (h *Handler) listResolversByFunctionTyped(ctx context.Context, req *listResolversByFunctionRequest) (*listResolversByFunctionResponse, *protocol.AWSError) {
	resolvers, err := h.store.ListResolvers(ctx, req.ApiId, "")
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listResolversByFunctionResponse{Resolvers: resolvers}, nil
}

// ─── Types ────────────────────────────────────────────────────────────────────

type getTypeRequest struct {
	ApiId    string `json:"apiId" cbor:"apiId"`
	TypeName string `json:"typeName" cbor:"typeName"`
}

type getTypeResponse struct {
	Type *TypeDefinition `json:"type" cbor:"type"`
}

func (h *Handler) getTypeTyped(ctx context.Context, req *getTypeRequest) (*getTypeResponse, *protocol.AWSError) {
	t, err := h.store.GetType(ctx, req.ApiId, req.TypeName)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &getTypeResponse{Type: t}, nil
}

type createTypeRequest struct {
	ApiId   string         `json:"apiId" cbor:"apiId"`
	TypeDef TypeDefinition `json:"type" cbor:"type"`
}

type createTypeResponse struct {
	Type *TypeDefinition `json:"type" cbor:"type"`
}

func (h *Handler) createTypeTyped(ctx context.Context, req *createTypeRequest) (*createTypeResponse, *protocol.AWSError) {
	t := req.TypeDef
	t.Arn = protocol.ARN(h.regionCtx(ctx), h.cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/types/%s", req.ApiId, t.Name))
	if err := h.store.PutType(ctx, req.ApiId, &t); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createTypeResponse{Type: &t}, nil
}

type updateTypeRequest struct {
	ApiId    string         `json:"apiId" cbor:"apiId"`
	TypeName string         `json:"typeName" cbor:"typeName"`
	TypeDef  TypeDefinition `json:"type" cbor:"type"`
}

type updateTypeResponse struct {
	Type *TypeDefinition `json:"type" cbor:"type"`
}

func (h *Handler) updateTypeTyped(ctx context.Context, req *updateTypeRequest) (*updateTypeResponse, *protocol.AWSError) {
	t := req.TypeDef
	t.Name = req.TypeName
	if err := h.store.PutType(ctx, req.ApiId, &t); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateTypeResponse{Type: &t}, nil
}

type deleteTypeRequest struct {
	ApiId    string `json:"apiId" cbor:"apiId"`
	TypeName string `json:"typeName" cbor:"typeName"`
}

func (h *Handler) deleteTypeTyped(ctx context.Context, req *deleteTypeRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteType(ctx, req.ApiId, req.TypeName); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type listTypesRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type listTypesResponse struct {
	Types []*TypeDefinition `json:"types" cbor:"types"`
}

func (h *Handler) listTypesTyped(ctx context.Context, req *listTypesRequest) (*listTypesResponse, *protocol.AWSError) {
	types, err := h.store.ListTypes(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listTypesResponse{Types: types}, nil
}

// ─── Caching ──────────────────────────────────────────────────────────────────

type createApiCacheRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Ttl   int64  `json:"ttl" cbor:"ttl"`
	Type  string `json:"type" cbor:"type"`
}

type createApiCacheResponse struct {
	ApiCache *ApiCacheConfig `json:"apiCache" cbor:"apiCache"`
}

func (h *Handler) createApiCacheTyped(ctx context.Context, req *createApiCacheRequest) (*createApiCacheResponse, *protocol.AWSError) {
	cache := &ApiCacheConfig{ApiId: req.ApiId, Ttl: req.Ttl, Type: req.Type, Status: "AVAILABLE"}
	if err := h.store.PutApiCache(ctx, req.ApiId, cache); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createApiCacheResponse{ApiCache: cache}, nil
}

type getApiCacheRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type getApiCacheResponse struct {
	ApiCache *ApiCacheConfig `json:"apiCache" cbor:"apiCache"`
}

func (h *Handler) getApiCacheTyped(ctx context.Context, req *getApiCacheRequest) (*getApiCacheResponse, *protocol.AWSError) {
	cache, err := h.store.GetApiCache(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &getApiCacheResponse{ApiCache: cache}, nil
}

type updateApiCacheRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Ttl   int64  `json:"ttl" cbor:"ttl"`
	Type  string `json:"type" cbor:"type"`
}

type updateApiCacheResponse struct {
	ApiCache *ApiCacheConfig `json:"apiCache" cbor:"apiCache"`
}

func (h *Handler) updateApiCacheTyped(ctx context.Context, req *updateApiCacheRequest) (*updateApiCacheResponse, *protocol.AWSError) {
	cache := &ApiCacheConfig{ApiId: req.ApiId, Ttl: req.Ttl, Type: req.Type, Status: "AVAILABLE"}
	if err := h.store.PutApiCache(ctx, req.ApiId, cache); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateApiCacheResponse{ApiCache: cache}, nil
}

type deleteApiCacheRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

func (h *Handler) deleteApiCacheTyped(ctx context.Context, req *deleteApiCacheRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteApiCache(ctx, req.ApiId); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type flushApiCacheRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

func (h *Handler) flushApiCacheTyped(ctx context.Context, req *flushApiCacheRequest) (any, *protocol.AWSError) {
	return struct{}{}, nil
}

// ─── Environment Variables ────────────────────────────────────────────────────

type putGraphqlApiEnvVarsRequest struct {
	ApiId                string            `json:"apiId" cbor:"apiId"`
	EnvironmentVariables map[string]string `json:"environmentVariables" cbor:"environmentVariables"`
}

type putGraphqlApiEnvVarsResponse struct {
	EnvironmentVariables map[string]string `json:"environmentVariables" cbor:"environmentVariables"`
}

func (h *Handler) putGraphqlApiEnvVarsTyped(ctx context.Context, req *putGraphqlApiEnvVarsRequest) (*putGraphqlApiEnvVarsResponse, *protocol.AWSError) {
	ev := &EnvironmentVariables{ApiId: req.ApiId, EnvironmentVariables: req.EnvironmentVariables}
	if _, err := json.Marshal(ev); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := h.store.PutEnvironmentVariables(ctx, req.ApiId, ev); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &putGraphqlApiEnvVarsResponse{EnvironmentVariables: req.EnvironmentVariables}, nil
}

type getGraphqlApiEnvVarsRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type getGraphqlApiEnvVarsResponse struct {
	EnvironmentVariables map[string]string `json:"environmentVariables" cbor:"environmentVariables"`
}

func (h *Handler) getGraphqlApiEnvVarsTyped(ctx context.Context, req *getGraphqlApiEnvVarsRequest) (*getGraphqlApiEnvVarsResponse, *protocol.AWSError) {
	ev, err := h.store.GetEnvironmentVariables(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if ev == nil {
		return &getGraphqlApiEnvVarsResponse{EnvironmentVariables: map[string]string{}}, nil
	}
	return &getGraphqlApiEnvVarsResponse{EnvironmentVariables: ev.EnvironmentVariables}, nil
}

// ─── Merged APIs ──────────────────────────────────────────────────────────────

type associateSourceGraphqlApiRequest struct {
	MergedApiIdentifier        string          `json:"mergedApiIdentifier" cbor:"mergedApiIdentifier"`
	SourceApiIdentifier        string          `json:"sourceApiIdentifier" cbor:"sourceApiIdentifier"`
	Description                string          `json:"description" cbor:"description"`
	SourceApiAssociationConfig json.RawMessage `json:"sourceApiAssociationConfig" cbor:"sourceApiAssociationConfig"`
}

type associateSourceGraphqlApiResponse struct {
	SourceApiAssociation *SourceApiAssociation `json:"sourceApiAssociation" cbor:"sourceApiAssociation"`
}

func (h *Handler) associateSourceGraphqlApiTyped(ctx context.Context, req *associateSourceGraphqlApiRequest) (*associateSourceGraphqlApiResponse, *protocol.AWSError) {
	_, err := h.store.GetAPI(ctx, req.MergedApiIdentifier)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	_, err = h.store.GetAPI(ctx, req.SourceApiIdentifier)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	assocID := uuid.NewString()
	assoc := &SourceApiAssociation{
		AssociationId:              assocID,
		MergedApiId:                req.MergedApiIdentifier,
		SourceApiId:                req.SourceApiIdentifier,
		Description:                req.Description,
		SourceApiAssociationConfig: req.SourceApiAssociationConfig,
		SourceApiAssociationStatus: "SUCCESS",
	}
	if err := h.store.PutSourceApiAssociation(ctx, req.MergedApiIdentifier, assoc); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &associateSourceGraphqlApiResponse{SourceApiAssociation: assoc}, nil
}

type associateMergedGraphqlApiRequest struct {
	SourceApiIdentifier        string          `json:"sourceApiIdentifier" cbor:"sourceApiIdentifier"`
	MergedApiIdentifier        string          `json:"mergedApiIdentifier" cbor:"mergedApiIdentifier"`
	Description                string          `json:"description" cbor:"description"`
	SourceApiAssociationConfig json.RawMessage `json:"sourceApiAssociationConfig" cbor:"sourceApiAssociationConfig"`
}

type associateMergedGraphqlApiResponse struct {
	SourceApiAssociation *SourceApiAssociation `json:"sourceApiAssociation" cbor:"sourceApiAssociation"`
}

func (h *Handler) associateMergedGraphqlApiTyped(ctx context.Context, req *associateMergedGraphqlApiRequest) (*associateMergedGraphqlApiResponse, *protocol.AWSError) {
	_, err := h.store.GetAPI(ctx, req.MergedApiIdentifier)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	_, err = h.store.GetAPI(ctx, req.SourceApiIdentifier)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	assocID := uuid.NewString()
	assoc := &SourceApiAssociation{
		AssociationId:              assocID,
		MergedApiId:                req.MergedApiIdentifier,
		SourceApiId:                req.SourceApiIdentifier,
		Description:                req.Description,
		SourceApiAssociationConfig: req.SourceApiAssociationConfig,
		SourceApiAssociationStatus: "SUCCESS",
	}
	if err := h.store.PutSourceApiAssociation(ctx, req.MergedApiIdentifier, assoc); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &associateMergedGraphqlApiResponse{SourceApiAssociation: assoc}, nil
}

type getSourceApiAssociationRequest struct {
	MergedApiIdentifier string `json:"mergedApiIdentifier" cbor:"mergedApiIdentifier"`
	AssociationId       string `json:"associationId" cbor:"associationId"`
}

type getSourceApiAssociationResponse struct {
	SourceApiAssociation *SourceApiAssociation `json:"sourceApiAssociation" cbor:"sourceApiAssociation"`
}

func (h *Handler) getSourceApiAssociationTyped(ctx context.Context, req *getSourceApiAssociationRequest) (*getSourceApiAssociationResponse, *protocol.AWSError) {
	assoc, err := h.store.GetSourceApiAssociation(ctx, req.MergedApiIdentifier, req.AssociationId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if assoc == nil {
		return nil, notFoundErr("Source API association " + req.AssociationId + " not found.")
	}
	return &getSourceApiAssociationResponse{SourceApiAssociation: assoc}, nil
}

type listSourceApiAssociationsRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type listSourceApiAssociationsResponse struct {
	SourceApiAssociations []*SourceApiAssociation `json:"sourceApiAssociations" cbor:"sourceApiAssociations"`
}

func (h *Handler) listSourceApiAssociationsTyped(ctx context.Context, req *listSourceApiAssociationsRequest) (*listSourceApiAssociationsResponse, *protocol.AWSError) {
	assocs, err := h.store.ListSourceApiAssociations(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listSourceApiAssociationsResponse{SourceApiAssociations: assocs}, nil
}

type disassociateSourceGraphqlApiRequest struct {
	MergedApiIdentifier string `json:"mergedApiIdentifier" cbor:"mergedApiIdentifier"`
	AssociationId       string `json:"associationId" cbor:"associationId"`
}

func (h *Handler) disassociateSourceGraphqlApiTyped(ctx context.Context, req *disassociateSourceGraphqlApiRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteSourceApiAssociation(ctx, req.MergedApiIdentifier, req.AssociationId); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type disassociateMergedGraphqlApiRequest struct {
	SourceApiIdentifier string `json:"sourceApiIdentifier" cbor:"sourceApiIdentifier"`
	AssociationId       string `json:"associationId" cbor:"associationId"`
}

func (h *Handler) disassociateMergedGraphqlApiTyped(ctx context.Context, req *disassociateMergedGraphqlApiRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteSourceApiAssociation(ctx, req.SourceApiIdentifier, req.AssociationId); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

type startSchemaMergeRequest struct {
	MergedApiIdentifier string `json:"mergedApiIdentifier" cbor:"mergedApiIdentifier"`
	AssociationId       string `json:"associationId" cbor:"associationId"`
}

type startSchemaMergeResponse struct {
	SourceApiAssociation *SourceApiAssociation `json:"sourceApiAssociation" cbor:"sourceApiAssociation"`
}

func (h *Handler) startSchemaMergeTyped(ctx context.Context, req *startSchemaMergeRequest) (*startSchemaMergeResponse, *protocol.AWSError) {
	assoc, err := h.store.GetSourceApiAssociation(ctx, req.MergedApiIdentifier, req.AssociationId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if assoc == nil {
		return nil, notFoundErr("Source API association " + req.AssociationId + " not found.")
	}
	assoc.SourceApiAssociationStatus = "MERGE_SUCCESS"
	if err := h.store.PutSourceApiAssociation(ctx, req.MergedApiIdentifier, assoc); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &startSchemaMergeResponse{SourceApiAssociation: assoc}, nil
}

// ─── Evaluation ───────────────────────────────────────────────────────────────

type evaluateMappingTemplateRequest struct {
	Template string `json:"template" cbor:"template"`
	Context  string `json:"context" cbor:"context"`
}

type evaluateMappingTemplateResponse struct {
	EvaluationResult string `json:"evaluationResult" cbor:"evaluationResult"`
}

func (h *Handler) evaluateMappingTemplateTyped(ctx context.Context, req *evaluateMappingTemplateRequest) (*evaluateMappingTemplateResponse, *protocol.AWSError) {
	var ctxMap map[string]any
	if req.Context != "" {
		_ = json.Unmarshal([]byte(req.Context), &ctxMap)
	}
	result, err := h.vtlEvaluator.Evaluate(req.Template, ctxMap)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &evaluateMappingTemplateResponse{EvaluationResult: result}, nil
}

type evaluateCodeRequest struct {
	Code    string `json:"code" cbor:"code"`
	Context string `json:"context" cbor:"context"`
}

type evaluateCodeResponse struct {
	EvaluationResult string `json:"evaluationResult" cbor:"evaluationResult"`
}

func (h *Handler) evaluateCodeTyped(ctx context.Context, req *evaluateCodeRequest) (*evaluateCodeResponse, *protocol.AWSError) {
	var ctxMap map[string]any
	if req.Context != "" {
		_ = json.Unmarshal([]byte(req.Context), &ctxMap)
	}
	result, err := h.jsEvaluator.Evaluate(req.Code, "request", ctxMap)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &evaluateCodeResponse{EvaluationResult: result.EvaluationResult}, nil
}

// ─── Events API ───────────────────────────────────────────────────────────────

type createEventApiRequest struct {
	Name string `json:"name" cbor:"name"`
}

type createEventApiResponse struct {
	Api *EventApi `json:"api" cbor:"api"`
}

func (h *Handler) createEventApiTyped(ctx context.Context, req *createEventApiRequest) (*createEventApiResponse, *protocol.AWSError) {
	if req.Name == "" {
		return nil, badReqErr("name is required")
	}
	apiID := uuid.NewString()
	api := &EventApi{
		ApiId: apiID, Name: req.Name,
		ApiArn:  protocol.ARN(h.regionCtx(ctx), h.cfg.AccountID, "appsync", "apis/"+apiID),
		Created: h.clk.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		Dns: map[string]string{
			"HTTP":     fmt.Sprintf("%s.appsync-api.%s.amazonaws.com", apiID, h.cfg.Region),
			"REALTIME": fmt.Sprintf("%s.appsync-realtime-api.%s.amazonaws.com", apiID, h.cfg.Region),
		},
	}
	if err := h.store.PutEventApi(ctx, api); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createEventApiResponse{Api: api}, nil
}

type getEventApiRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type getEventApiResponse struct {
	Api *EventApi `json:"api" cbor:"api"`
}

func (h *Handler) getEventApiTyped(ctx context.Context, req *getEventApiRequest) (*getEventApiResponse, *protocol.AWSError) {
	api, err := h.store.GetEventApi(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundErr("Api " + req.ApiId + " not found.")
	}
	return &getEventApiResponse{Api: api}, nil
}

type listEventApisRequest struct{}

type listEventApisResponse struct {
	Apis []*EventApi `json:"apis" cbor:"apis"`
}

func (h *Handler) listEventApisTyped(ctx context.Context, _ *listEventApisRequest) (*listEventApisResponse, *protocol.AWSError) {
	apis, err := h.store.ListEventApis(ctx)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listEventApisResponse{Apis: apis}, nil
}

type updateEventApiRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Name  string `json:"name" cbor:"name"`
}

type updateEventApiResponse struct {
	Api *EventApi `json:"api" cbor:"api"`
}

func (h *Handler) updateEventApiTyped(ctx context.Context, req *updateEventApiRequest) (*updateEventApiResponse, *protocol.AWSError) {
	api, err := h.store.GetEventApi(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if api == nil {
		return nil, notFoundErr("Api " + req.ApiId + " not found.")
	}
	api.Name = req.Name
	if err := h.store.PutEventApi(ctx, api); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateEventApiResponse{Api: api}, nil
}

type deleteEventApiRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

func (h *Handler) deleteEventApiTyped(ctx context.Context, req *deleteEventApiRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteEventApi(ctx, req.ApiId); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

// ─── Channel Namespaces ───────────────────────────────────────────────────────

type createChannelNamespaceRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Name  string `json:"name" cbor:"name"`
}

type createChannelNamespaceResponse struct {
	ChannelNamespace *ChannelNamespace `json:"channelNamespace" cbor:"channelNamespace"`
}

func (h *Handler) createChannelNamespaceTyped(ctx context.Context, req *createChannelNamespaceRequest) (*createChannelNamespaceResponse, *protocol.AWSError) {
	ns := &ChannelNamespace{ApiId: req.ApiId, Name: req.Name}
	if err := h.store.PutChannelNamespace(ctx, req.ApiId, ns); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &createChannelNamespaceResponse{ChannelNamespace: ns}, nil
}

type getChannelNamespaceRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Name  string `json:"name" cbor:"name"`
}

type getChannelNamespaceResponse struct {
	ChannelNamespace *ChannelNamespace `json:"channelNamespace" cbor:"channelNamespace"`
}

func (h *Handler) getChannelNamespaceTyped(ctx context.Context, req *getChannelNamespaceRequest) (*getChannelNamespaceResponse, *protocol.AWSError) {
	ns, err := h.store.GetChannelNamespace(ctx, req.ApiId, req.Name)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if ns == nil {
		return nil, notFoundErr("Channel namespace " + req.Name + " not found.")
	}
	return &getChannelNamespaceResponse{ChannelNamespace: ns}, nil
}

type listChannelNamespacesRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
}

type listChannelNamespacesResponse struct {
	ChannelNamespaces []*ChannelNamespace `json:"channelNamespaces" cbor:"channelNamespaces"`
}

func (h *Handler) listChannelNamespacesTyped(ctx context.Context, req *listChannelNamespacesRequest) (*listChannelNamespacesResponse, *protocol.AWSError) {
	nss, err := h.store.ListChannelNamespaces(ctx, req.ApiId)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &listChannelNamespacesResponse{ChannelNamespaces: nss}, nil
}

type updateChannelNamespaceRequest struct {
	ApiId string           `json:"apiId" cbor:"apiId"`
	Name  string           `json:"name" cbor:"name"`
	NS    ChannelNamespace `json:"channelNamespace" cbor:"channelNamespace"`
}

type updateChannelNamespaceResponse struct {
	ChannelNamespace *ChannelNamespace `json:"channelNamespace" cbor:"channelNamespace"`
}

func (h *Handler) updateChannelNamespaceTyped(ctx context.Context, req *updateChannelNamespaceRequest) (*updateChannelNamespaceResponse, *protocol.AWSError) {
	ns := req.NS
	ns.ApiId = req.ApiId
	ns.Name = req.Name
	if err := h.store.PutChannelNamespace(ctx, req.ApiId, &ns); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &updateChannelNamespaceResponse{ChannelNamespace: &ns}, nil
}

type deleteChannelNamespaceRequest struct {
	ApiId string `json:"apiId" cbor:"apiId"`
	Name  string `json:"name" cbor:"name"`
}

func (h *Handler) deleteChannelNamespaceTyped(ctx context.Context, req *deleteChannelNamespaceRequest) (any, *protocol.AWSError) {
	if err := h.store.DeleteChannelNamespace(ctx, req.ApiId, req.Name); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return struct{}{}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *Handler) validateAPICtx(ctx context.Context, apiID string) (*GraphqlAPI, bool) {
	api, err := h.store.GetAPI(ctx, apiID)
	if err != nil || api == nil {
		return nil, false
	}
	return api, true
}

func notFoundErr(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "NotFoundException", Message: msg, HTTPStatus: 404}
}

func badReqErr(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "BadRequestException", Message: msg, HTTPStatus: 400}
}

func conflictErr(msg string) *protocol.AWSError {
	return &protocol.AWSError{Code: "ConcurrentModificationException", Message: msg, HTTPStatus: 409}
}
