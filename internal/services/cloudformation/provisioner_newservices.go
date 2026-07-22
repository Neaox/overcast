package cloudformation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
)

// ── AWS::RDS::DBInstance ───────────────────────────────────────────────────

type rdsDBInstanceHandler struct{}

func (h *rdsDBInstanceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	id, _ := props["DBInstanceIdentifier"].(string)
	if id == "" {
		id = fmt.Sprintf("%s-db", rCtx.StackName)
	}

	params := map[string]string{
		"Action":               "CreateDBInstance",
		"Version":              "2014-10-31",
		"DBInstanceIdentifier": id,
	}
	if v, _ := props["Engine"].(string); v != "" {
		params["Engine"] = v
	}
	if v, _ := props["MasterUsername"].(string); v != "" {
		params["MasterUsername"] = v
	}
	if v, _ := props["MasterUserPassword"].(string); v != "" {
		params["MasterUserPassword"] = v
	}
	if v, _ := props["DBInstanceClass"].(string); v != "" {
		params["DBInstanceClass"] = v
	}
	if v, _ := props["EngineVersion"].(string); v != "" {
		params["EngineVersion"] = v
	}
	if v := fmtPropString(props, "AllocatedStorage"); v != "" {
		params["AllocatedStorage"] = v
	}
	if v := fmtPropString(props, "Port"); v != "" {
		params["Port"] = v
	}
	if v, _ := props["StorageType"].(string); v != "" {
		params["StorageType"] = v
	}
	if v, _ := props["DBName"].(string); v != "" {
		params["DBName"] = v
	}
	if v, _ := props["DBClusterIdentifier"].(string); v != "" {
		params["DBClusterIdentifier"] = v
	}
	if v, _ := props["DBSubnetGroupName"].(string); v != "" {
		params["DBSubnetGroupName"] = v
	}
	if v, _ := props["DBParameterGroupName"].(string); v != "" {
		params["DBParameterGroupName"] = v
	}
	if v, ok := props["MultiAZ"]; ok {
		params["MultiAZ"] = cfnScalarString(v)
	}
	// VPCSecurityGroups
	if sgs, ok := props["VPCSecurityGroups"].([]any); ok {
		for i, sg := range sgs {
			if s, _ := sg.(string); s != "" {
				params[fmt.Sprintf("VpcSecurityGroupIds.member.%d", i+1)] = s
			}
		}
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDBInstance: %w", err)
	}

	body := rec.Body.String()
	arn := extractXMLValue(body, "DBInstanceArn")
	endpointAddr := extractXMLValue(body, "Address")
	endpointPort := extractXMLValue(body, "Port")

	if arn == "" {
		arn = fmt.Sprintf("arn:aws:rds:%s:%s:db:%s", rCtx.Region, rCtx.AccountID, id)
	}

	attrs := map[string]string{
		"DBInstanceArn":    arn,
		"Endpoint.Address": endpointAddr,
		"Endpoint.Port":    endpointPort,
	}
	return id, attrs, nil
}

func (h *rdsDBInstanceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":               "DeleteDBInstance",
		"Version":              "2014-10-31",
		"DBInstanceIdentifier": physicalID,
		"SkipFinalSnapshot":    "true",
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *rdsDBInstanceHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newID, _ := props["DBInstanceIdentifier"].(string); newID != "" {
			if oldID, _ := oldProps["DBInstanceIdentifier"].(string); oldID != "" && newID != oldID {
				return "", nil, errReplacementRequired
			}
		}
		if newEngine, _ := props["Engine"].(string); newEngine != "" {
			if oldEngine, _ := oldProps["Engine"].(string); oldEngine != "" && newEngine != oldEngine {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":               "ModifyDBInstance",
		"Version":              "2014-10-31",
		"DBInstanceIdentifier": physicalID,
	}
	if v := fmtPropString(props, "AllocatedStorage"); v != "" {
		params["AllocatedStorage"] = v
	}
	if v, _ := props["DBInstanceClass"].(string); v != "" {
		params["DBInstanceClass"] = v
	}
	if v, _ := props["MasterUserPassword"].(string); v != "" {
		params["MasterUserPassword"] = v
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("ModifyDBInstance: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::RDS::DBCluster ───────────────────────────────────────────────────

type rdsDBClusterHandler struct{}

func (h *rdsDBClusterHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	id, _ := props["DBClusterIdentifier"].(string)
	if id == "" {
		id = fmt.Sprintf("%s-cluster", rCtx.StackName)
	}

	params := map[string]string{
		"Action":              "CreateDBCluster",
		"Version":             "2014-10-31",
		"DBClusterIdentifier": id,
	}
	if v, _ := props["Engine"].(string); v != "" {
		params["Engine"] = v
	}
	if v, _ := props["MasterUsername"].(string); v != "" {
		params["MasterUsername"] = v
	}
	if v, _ := props["MasterUserPassword"].(string); v != "" {
		params["MasterUserPassword"] = v
	}
	if v, _ := props["EngineVersion"].(string); v != "" {
		params["EngineVersion"] = v
	}
	if v, _ := props["DatabaseName"].(string); v != "" {
		params["DatabaseName"] = v
	}
	if v, _ := props["StorageType"].(string); v != "" {
		params["StorageType"] = v
	}
	if v, _ := props["DBSubnetGroupName"].(string); v != "" {
		params["DBSubnetGroupName"] = v
	}
	if v := fmtPropString(props, "Port"); v != "" {
		params["Port"] = v
	}
	if sgs, ok := props["VpcSecurityGroupIds"].([]any); ok {
		for i, sg := range sgs {
			if s, _ := sg.(string); s != "" {
				params[fmt.Sprintf("VpcSecurityGroupIds.member.%d", i+1)] = s
			}
		}
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDBCluster: %w", err)
	}

	body := rec.Body.String()
	arn := extractXMLValue(body, "DBClusterArn")
	endpoint := extractXMLValue(body, "Endpoint")
	readerEndpoint := extractXMLValue(body, "ReaderEndpoint")
	port := extractXMLValue(body, "Port")

	if arn == "" {
		arn = fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s", rCtx.Region, rCtx.AccountID, id)
	}

	attrs := map[string]string{
		"DBClusterArn":         arn,
		"Endpoint.Address":     endpoint,
		"Endpoint.Port":        port,
		"ReadEndpoint.Address": readerEndpoint,
		"DBClusterResourceId":  fmt.Sprintf("cluster-%s", id),
	}
	return id, attrs, nil
}

func (h *rdsDBClusterHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":              "DeleteDBCluster",
		"Version":             "2014-10-31",
		"DBClusterIdentifier": physicalID,
		"SkipFinalSnapshot":   "true",
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *rdsDBClusterHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if newID, _ := props["DBClusterIdentifier"].(string); newID != "" && newID != physicalID {
		return "", nil, errReplacementRequired
	}
	if oldProps != nil {
		if newEngine, _ := props["Engine"].(string); newEngine != "" {
			if oldEngine, _ := oldProps["Engine"].(string); oldEngine != "" && newEngine != oldEngine {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":              "ModifyDBCluster",
		"Version":             "2014-10-31",
		"DBClusterIdentifier": physicalID,
	}
	if v, _ := props["MasterUserPassword"].(string); v != "" {
		params["MasterUserPassword"] = v
	}
	if v := fmtPropString(props, "BackupRetentionPeriod"); v != "" {
		params["BackupRetentionPeriod"] = v
	}
	if v := fmtPropString(props, "Port"); v != "" {
		params["Port"] = v
	}
	if v, _ := props["PreferredBackupWindow"].(string); v != "" {
		params["PreferredBackupWindow"] = v
	}
	if v, _ := props["PreferredMaintenanceWindow"].(string); v != "" {
		params["PreferredMaintenanceWindow"] = v
	}
	if v, _ := props["DBClusterParameterGroupName"].(string); v != "" {
		params["DBClusterParameterGroupName"] = v
	}
	if sgs, ok := props["VpcSecurityGroupIds"].([]any); ok {
		for i, sg := range sgs {
			if s, _ := sg.(string); s != "" {
				params[fmt.Sprintf("VpcSecurityGroupIds.VpcSecurityGroupId.%d", i+1)] = s
			}
		}
	}
	if v, ok := props["EnableCloudwatchLogsExports"]; ok {
		if logs, ok := v.([]any); ok {
			for i, l := range logs {
				if s, _ := l.(string); s != "" {
					params[fmt.Sprintf("CloudwatchLogsExportConfiguration.EnableLogTypes.member.%d", i+1)] = s
				}
			}
		}
	}
	if v, ok := props["DeletionProtection"]; ok {
		params["DeletionProtection"] = cfnScalarString(v)
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("ModifyDBCluster: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::RDS::DBSubnetGroup ───────────────────────────────────────────────

type rdsDBSubnetGroupHandler struct{}

func (h *rdsDBSubnetGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["DBSubnetGroupName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-subnetgrp", rCtx.StackName)
	}
	desc, _ := props["DBSubnetGroupDescription"].(string)
	if desc == "" {
		desc = name
	}

	params := map[string]string{
		"Action":                   "CreateDBSubnetGroup",
		"Version":                  "2014-10-31",
		"DBSubnetGroupName":        name,
		"DBSubnetGroupDescription": desc,
	}
	if subnets, ok := props["SubnetIds"].([]any); ok {
		for i, s := range subnets {
			if v, _ := s.(string); v != "" {
				params[fmt.Sprintf("SubnetIds.member.%d", i+1)] = v
			}
		}
	}

	_, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDBSubnetGroup: %w", err)
	}
	return name, nil, nil
}

func (h *rdsDBSubnetGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":            "DeleteDBSubnetGroup",
		"Version":           "2014-10-31",
		"DBSubnetGroupName": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *rdsDBSubnetGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::RDS::DBParameterGroup ────────────────────────────────────────────

type rdsDBParameterGroupHandler struct{}

func (h *rdsDBParameterGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["DBParameterGroupName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-pg", rCtx.StackName)
	}

	params := map[string]string{
		"Action":               "CreateDBParameterGroup",
		"Version":              "2014-10-31",
		"DBParameterGroupName": name,
	}
	if v, _ := props["Family"].(string); v != "" {
		params["DBParameterGroupFamily"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		params["Description"] = v
	}

	_, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDBParameterGroup: %w", err)
	}
	return name, nil, nil
}

func (h *rdsDBParameterGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":               "DeleteDBParameterGroup",
		"Version":              "2014-10-31",
		"DBParameterGroupName": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *rdsDBParameterGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if newName, _ := props["DBParameterGroupName"].(string); newName != "" && newName != physicalID {
		return "", nil, errReplacementRequired
	}
	if oldProps != nil {
		if newFamily, _ := props["Family"].(string); newFamily != "" {
			if oldFamily, _ := oldProps["Family"].(string); oldFamily != "" && newFamily != oldFamily {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":               "ModifyDBParameterGroup",
		"Version":              "2014-10-31",
		"DBParameterGroupName": physicalID,
	}

	if ps, ok := props["Parameters"]; ok {
		switch v := ps.(type) {
		case map[string]any:
			idx := 0
			for k, val := range v {
				idx++
				params[fmt.Sprintf("Parameters.member.%d.ParameterName", idx)] = k
				params[fmt.Sprintf("Parameters.member.%d.ParameterValue", idx)] = fmt.Sprintf("%v", val)
				params[fmt.Sprintf("Parameters.member.%d.ApplyMethod", idx)] = "immediate"
			}
		case []any:
			for i, p := range v {
				if pm, ok := p.(map[string]any); ok {
					if name, _ := pm["ParameterName"].(string); name != "" {
						params[fmt.Sprintf("Parameters.member.%d.ParameterName", i+1)] = name
					}
					if val := pm["ParameterValue"]; val != nil {
						params[fmt.Sprintf("Parameters.member.%d.ParameterValue", i+1)] = fmt.Sprintf("%v", val)
					}
					if apply, _ := pm["ApplyMethod"].(string); apply != "" {
						params[fmt.Sprintf("Parameters.member.%d.ApplyMethod", i+1)] = apply
					}
				}
			}
		}
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("ModifyDBParameterGroup: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::Kinesis::Stream ──────────────────────────────────────────────────

type kinesisStreamHandler struct{}

func (h *kinesisStreamHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["Name"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-stream", rCtx.StackName)
	}

	body := map[string]any{
		"StreamName": name,
	}
	if v, ok := props["ShardCount"]; ok {
		body["ShardCount"] = v
	} else {
		body["ShardCount"] = 1
	}

	_, err := internalJSON(ctx, router, rCtx.Region, "Kinesis_20131202.CreateStream", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateStream: %w", err)
	}

	arn := fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/%s", rCtx.Region, rCtx.AccountID, name)
	attrs := map[string]string{
		"Arn":  arn,
		"Name": name,
	}
	return name, attrs, nil
}

func (h *kinesisStreamHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"StreamName": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "Kinesis_20131202.DeleteStream", body)
	return nil
}

func (h *kinesisStreamHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newName, _ := props["Name"].(string); newName != "" {
			if oldName, _ := oldProps["Name"].(string); oldName != "" && newName != oldName {
				return "", nil, errReplacementRequired
			}
		}
	}
	return "", nil, errReplacementRequired
}

// ── AWS::Cognito::UserPool ────────────────────────────────────────────────

type cognitoUserPoolHandler struct{}

func (h *cognitoUserPoolHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	poolName, _ := props["UserPoolName"].(string)
	if poolName == "" {
		poolName = fmt.Sprintf("%s-pool", rCtx.StackName)
	}

	body := map[string]any{
		"PoolName": poolName,
	}
	if v, ok := props["Policies"]; ok {
		body["Policies"] = v
	}
	if v, ok := props["UsernameAttributes"]; ok {
		body["UsernameAttributes"] = v
	}
	if v, ok := props["AutoVerifiedAttributes"]; ok {
		body["AutoVerifiedAttributes"] = v
	}
	if v, ok := props["Schema"]; ok {
		body["Schema"] = v
	}
	if v, ok := props["VerificationMessageTemplate"]; ok {
		body["VerificationMessageTemplate"] = v
	}
	if v, ok := props["AdminCreateUserConfig"]; ok {
		body["AdminCreateUserConfig"] = v
	}
	if v, ok := props["EmailConfiguration"]; ok {
		body["EmailConfiguration"] = v
	}
	if v, ok := props["MfaConfiguration"]; ok {
		body["MfaConfiguration"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSCognitoIdentityProviderService.CreateUserPool", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateUserPool: %w", err)
	}

	var resp struct {
		UserPool struct {
			ID   string `json:"Id"`
			Name string `json:"Name"`
			Arn  string `json:"Arn"`
		} `json:"UserPool"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateUserPool: parse response: %w", err)
	}

	poolID := resp.UserPool.ID
	arn := resp.UserPool.Arn
	providerName := fmt.Sprintf("cognito-idp.%s.amazonaws.com/%s", rCtx.Region, poolID)

	attrs := map[string]string{
		"ProviderName": providerName,
		"ProviderURL":  fmt.Sprintf("https://%s", providerName),
		"Arn":          arn,
		"UserPoolId":   poolID,
	}
	return poolID, attrs, nil
}

func (h *cognitoUserPoolHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	body := map[string]any{"UserPoolId": physicalID}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AWSCognitoIdentityProviderService.DeleteUserPool", body)
	return nil
}

// ── AWS::Cognito::UserPoolClient ──────────────────────────────────────────

type cognitoUserPoolClientHandler struct{}

func (h *cognitoUserPoolClientHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	userPoolID, _ := props["UserPoolId"].(string)
	clientName, _ := props["ClientName"].(string)
	if clientName == "" {
		clientName = fmt.Sprintf("%s-client", rCtx.StackName)
	}

	body := map[string]any{
		"UserPoolId": userPoolID,
		"ClientName": clientName,
	}
	if v, ok := props["GenerateSecret"]; ok {
		body["GenerateSecret"] = v
	}
	if v, ok := props["ExplicitAuthFlows"]; ok {
		body["ExplicitAuthFlows"] = v
	}
	if v, ok := props["SupportedIdentityProviders"]; ok {
		body["SupportedIdentityProviders"] = v
	}
	if v, ok := props["CallbackURLs"]; ok {
		body["CallbackURLs"] = v
	}
	if v, ok := props["LogoutURLs"]; ok {
		body["LogoutURLs"] = v
	}
	if v, ok := props["AllowedOAuthFlows"]; ok {
		body["AllowedOAuthFlows"] = v
	}
	if v, ok := props["AllowedOAuthScopes"]; ok {
		body["AllowedOAuthScopes"] = v
	}
	if v, ok := props["AllowedOAuthFlowsUserPoolClient"]; ok {
		body["AllowedOAuthFlowsUserPoolClient"] = v
	}
	if v, ok := props["AccessTokenValidity"]; ok {
		body["AccessTokenValidity"] = v
	}
	if v, ok := props["IdTokenValidity"]; ok {
		body["IdTokenValidity"] = v
	}
	if v, ok := props["RefreshTokenValidity"]; ok {
		body["RefreshTokenValidity"] = v
	}
	if v, ok := props["TokenValidityUnits"]; ok {
		body["TokenValidityUnits"] = v
	}

	rec, err := internalJSON(ctx, router, rCtx.Region, "AWSCognitoIdentityProviderService.CreateUserPoolClient", body)
	if err != nil {
		return "", nil, fmt.Errorf("CreateUserPoolClient: %w", err)
	}

	var resp struct {
		UserPoolClient struct {
			ClientID     string `json:"ClientId"`
			ClientName   string `json:"ClientName"`
			ClientSecret string `json:"ClientSecret"`
		} `json:"UserPoolClient"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateUserPoolClient: parse response: %w", err)
	}

	clientID := resp.UserPoolClient.ClientID
	attrs := map[string]string{
		"ClientId":     clientID,
		"Name":         resp.UserPoolClient.ClientName,
		"ClientSecret": resp.UserPoolClient.ClientSecret,
		"Ref":          clientID,
	}
	// Encode UserPoolId in physicalID so Delete can recover it.
	physicalID := userPoolID + "/" + clientID
	return physicalID, attrs, nil
}

func (h *cognitoUserPoolClientHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	body := map[string]any{
		"UserPoolId": parts[0],
		"ClientId":   parts[1],
	}
	_, _ = internalJSON(ctx, router, rCtx.Region, "AWSCognitoIdentityProviderService.DeleteUserPoolClient", body)
	return nil
}

// ── AWS::AppSync::GraphQLApi ──────────────────────────────────────────────

type appsyncGraphQLApiHandler struct{}

type appsyncGraphQLApiResponse struct {
	GraphqlApi struct {
		ApiID string            `json:"apiId"`
		Arn   string            `json:"arn"`
		Uris  map[string]string `json:"uris"`
		Dns   map[string]string `json:"dns"`
	} `json:"graphqlApi"`
}

type appsyncApiKeyResponse struct {
	ApiKey struct {
		ID string `json:"id"`
	} `json:"apiKey"`
}

type appsyncFunctionResponse struct {
	FunctionConfiguration struct {
		FunctionID     string `json:"functionId"`
		FunctionArn    string `json:"functionArn"`
		Name           string `json:"name"`
		DataSourceName string `json:"dataSourceName"`
	} `json:"functionConfiguration"`
}

type appsyncDataSourceResponse struct {
	DataSource struct {
		DataSourceArn string `json:"dataSourceArn"`
		Name          string `json:"name"`
	} `json:"dataSource"`
}

type appsyncResolverResponse struct {
	Resolver struct {
		ResolverArn string `json:"resolverArn"`
		FieldName   string `json:"fieldName"`
		TypeName    string `json:"typeName"`
	} `json:"resolver"`
}

type appsyncDomainNameResponse struct {
	DomainNameConfig struct {
		DomainName        string `json:"domainName"`
		AppsyncDomainName string `json:"appsyncDomainName"`
		HostedZoneID      string `json:"hostedZoneId"`
	} `json:"domainNameConfig"`
}

type appsyncApiAssociationResponse struct {
	ApiAssociation struct {
		DomainName        string `json:"domainName"`
		ApiID             string `json:"apiId"`
		AssociationStatus string `json:"associationStatus"`
	} `json:"apiAssociation"`
}

type appsyncApiCacheResponse struct {
	ApiCache struct {
		ApiID              string `json:"apiId"`
		Type               string `json:"type"`
		ApiCachingBehavior string `json:"apiCachingBehavior"`
		Status             string `json:"status"`
	} `json:"apiCache"`
}

type appsyncSourceApiAssociationResponse struct {
	SourceApiAssociation struct {
		AssociationID                    string `json:"associationId"`
		AssociationArn                   string `json:"associationArn"`
		SourceApiID                      string `json:"sourceApiId"`
		SourceApiArn                     string `json:"sourceApiArn"`
		MergedApiID                      string `json:"mergedApiId"`
		MergedApiArn                     string `json:"mergedApiArn"`
		SourceApiAssociationStatus       string `json:"sourceApiAssociationStatus"`
		SourceApiAssociationStatusDetail string `json:"sourceApiAssociationStatusDetail"`
		LastSuccessfulMergeDate          int64  `json:"lastSuccessfulMergeDate"`
	} `json:"sourceApiAssociation"`
}

type appsyncEventsApiResponse struct {
	API struct {
		ApiID  string            `json:"apiId"`
		ApiArn string            `json:"apiArn"`
		Name   string            `json:"name"`
		Dns    map[string]string `json:"dns"`
	} `json:"api"`
}

type appsyncChannelNamespaceResponse struct {
	ChannelNamespace struct {
		ApiID               string `json:"apiId"`
		Name                string `json:"name"`
		ChannelNamespaceArn string `json:"channelNamespaceArn"`
	} `json:"channelNamespace"`
}

func appsyncEventsRESTJSON(ctx context.Context, router http.Handler, region, method, path, opName string, body map[string]any, out any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s: marshal request: %w", opName, err)
		}
	}
	rec, err := internalAppSyncEventsRequest(ctx, router, region, method, path, "application/json", data)
	if err != nil {
		return fmt.Errorf("%s: %w", opName, err)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return fmt.Errorf("%s: parse response: %w", opName, err)
		}
	}
	return nil
}

func internalAppSyncEventsRequest(ctx context.Context, router http.Handler, region, method, path, contentType string, body []byte) (*httptest.ResponseRecorder, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if region != "" {
		req.Header.Set("X-Overcast-Region", region)
	}
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential=overcast/20250101/"+region+"/appsync/aws4_request, SignedHeaders=host, Signature=overcast")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return rec, fmt.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	return rec, nil
}

func appsyncRESTJSON(ctx context.Context, router http.Handler, region, method, path, opName string, body map[string]any, out any) error {
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return fmt.Errorf("%s: marshal request: %w", opName, err)
		}
	}
	rec, err := internalRequest(ctx, router, region, method, path, "application/json", data)
	if err != nil {
		return fmt.Errorf("%s: %w", opName, err)
	}
	if out != nil {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			return fmt.Errorf("%s: parse response: %w", opName, err)
		}
	}
	return nil
}

func appsyncSplitPhysicalID(resource, physicalID string, want int) ([]string, error) {
	parts := strings.SplitN(physicalID, "/", want)
	if len(parts) != want {
		return nil, fmt.Errorf("%s: invalid physical ID %q", resource, physicalID)
	}
	for _, part := range parts {
		if part == "" {
			return nil, fmt.Errorf("%s: invalid physical ID %q", resource, physicalID)
		}
	}
	return parts, nil
}

func appsyncGraphQLApiBody(props map[string]any) map[string]any {
	body := map[string]any{}
	copyStringProp(body, props, "Name", "name")
	copyStringProp(body, props, "AuthenticationType", "authenticationType")
	copyStringProp(body, props, "ApiType", "apiType")
	copyStringProp(body, props, "Visibility", "visibility")
	copyStringProp(body, props, "OwnerContact", "ownerContact")
	copyStringProp(body, props, "WafWebAclArn", "wafWebAclArn")
	copyStringProp(body, props, "MergedApiExecutionRoleArn", "mergedApiExecutionRoleArn")
	copyStringProp(body, props, "IntrospectionConfig", "introspectionConfig")
	copyAnyProp(body, props, "XrayEnabled", "xrayEnabled")
	copyAnyProp(body, props, "QueryDepthLimit", "queryDepthLimit")
	copyAnyProp(body, props, "ResolverCountLimit", "resolverCountLimit")
	copyAnyProp(body, props, "AdditionalAuthenticationProviders", "additionalAuthenticationProviders")
	copyAnyProp(body, props, "LogConfig", "logConfig")
	copyAnyProp(body, props, "UserPoolConfig", "userPoolConfig")
	copyAnyProp(body, props, "OpenIDConnectConfig", "openIDConnectConfig")
	copyAnyProp(body, props, "LambdaAuthorizerConfig", "lambdaAuthorizerConfig")
	copyAnyProp(body, props, "EnhancedMetricsConfig", "enhancedMetricsConfig")
	if tags := cfnTagListToMap(props["Tags"]); len(tags) > 0 {
		body["tags"] = tags
	}
	return body
}

func cfnTagListToMap(raw any) map[string]string {
	if raw == nil {
		return nil
	}
	out := map[string]string{}
	add := func(key string, value any) {
		if key == "" {
			return
		}
		out[key] = fmt.Sprintf("%v", value)
	}
	switch tags := raw.(type) {
	case []any:
		for _, item := range tags {
			switch tag := item.(type) {
			case map[string]any:
				key, _ := tag["Key"].(string)
				add(key, tag["Value"])
			case Tag:
				add(tag.Key, tag.Value)
			}
		}
	case []Tag:
		for _, tag := range tags {
			add(tag.Key, tag.Value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyStringProp(dst map[string]any, props map[string]any, cfnName, jsonName string) {
	if v, _ := props[cfnName].(string); v != "" {
		dst[jsonName] = v
	}
}

func copyAnyProp(dst map[string]any, props map[string]any, cfnName, jsonName string) {
	if v, ok := props[cfnName]; ok {
		dst[jsonName] = v
	}
}

func copyConvertedCFProp(dst map[string]any, props map[string]any, cfnName, jsonName string) {
	if v, ok := props[cfnName]; ok {
		dst[jsonName] = convertCFKeysToAPI(v)
	}
}

func appsyncGraphQLApiAttrs(resp appsyncGraphQLApiResponse) map[string]string {
	api := resp.GraphqlApi
	return map[string]string{
		"Ref":                api.Arn,
		"ApiId":              api.ApiID,
		"Arn":                api.Arn,
		"GraphQLEndpointArn": api.Arn,
		"GraphQLUrl":         api.Uris["GRAPHQL"],
		"RealtimeUrl":        api.Uris["REALTIME"],
		"GraphQLDns":         api.Dns["GRAPHQL"],
		"RealtimeDns":        api.Dns["REALTIME"],
	}
}

func appsyncApiKeyAttrs(cfg *config.Config, region, apiID, keyID string) map[string]string {
	arn := protocol.ARN(region, cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/apikeys/%s", apiID, keyID))
	return map[string]string{"Ref": arn, "Arn": arn, "ApiKey": keyID, "ApiKeyId": keyID}
}

func appsyncFunctionAttrs(resp appsyncFunctionResponse) map[string]string {
	fn := resp.FunctionConfiguration
	return map[string]string{
		"Ref":            fn.FunctionArn,
		"FunctionArn":    fn.FunctionArn,
		"FunctionId":     fn.FunctionID,
		"Name":           fn.Name,
		"DataSourceName": fn.DataSourceName,
	}
}

func appsyncDataSourceBody(props map[string]any) map[string]any {
	body := map[string]any{}
	copyStringProp(body, props, "Name", "name")
	copyStringProp(body, props, "Type", "type")
	copyStringProp(body, props, "ServiceRoleArn", "serviceRoleArn")
	copyStringProp(body, props, "Description", "description")
	copyAnyProp(body, props, "LambdaConfig", "lambdaConfig")
	copyAnyProp(body, props, "DynamoDBConfig", "dynamodbConfig")
	copyAnyProp(body, props, "HttpConfig", "httpConfig")
	copyAnyProp(body, props, "OpenSearchServiceConfig", "openSearchServiceConfig")
	copyAnyProp(body, props, "ElasticsearchConfig", "elasticsearchConfig")
	copyAnyProp(body, props, "RelationalDatabaseConfig", "relationalDatabaseConfig")
	copyAnyProp(body, props, "EventBridgeConfig", "eventBridgeConfig")
	copyAnyProp(body, props, "MetricsConfig", "metricsConfig")
	return body
}

func appsyncDataSourceAttrs(resp appsyncDataSourceResponse) map[string]string {
	ds := resp.DataSource
	return map[string]string{"Ref": ds.DataSourceArn, "DataSourceArn": ds.DataSourceArn, "Name": ds.Name}
}

func appsyncResolverBody(ctx context.Context, router http.Handler, region string, props map[string]any) (map[string]any, error) {
	body := map[string]any{}
	copyStringProp(body, props, "FieldName", "fieldName")
	copyStringProp(body, props, "DataSourceName", "dataSourceName")
	copyStringProp(body, props, "Kind", "kind")
	if err := copyStringOrS3Prop(ctx, router, region, body, props, "RequestMappingTemplate", "RequestMappingTemplateS3Location", "requestMappingTemplate"); err != nil {
		return nil, fmt.Errorf("Resolver RequestMappingTemplateS3Location: %w", err)
	}
	if err := copyStringOrS3Prop(ctx, router, region, body, props, "ResponseMappingTemplate", "ResponseMappingTemplateS3Location", "responseMappingTemplate"); err != nil {
		return nil, fmt.Errorf("Resolver ResponseMappingTemplateS3Location: %w", err)
	}
	if err := copyStringOrS3Prop(ctx, router, region, body, props, "Code", "CodeS3Location", "code"); err != nil {
		return nil, fmt.Errorf("Resolver CodeS3Location: %w", err)
	}
	copyAnyProp(body, props, "PipelineConfig", "pipelineConfig")
	copyAnyProp(body, props, "Runtime", "runtime")
	copyAnyProp(body, props, "MaxBatchSize", "maxBatchSize")
	copyAnyProp(body, props, "SyncConfig", "syncConfig")
	copyAnyProp(body, props, "CachingConfig", "cachingConfig")
	copyAnyProp(body, props, "MetricsConfig", "metricsConfig")
	return body, nil
}

func appsyncResolverAttrs(resp appsyncResolverResponse) map[string]string {
	res := resp.Resolver
	return map[string]string{"Ref": res.ResolverArn, "ResolverArn": res.ResolverArn, "FieldName": res.FieldName, "TypeName": res.TypeName}
}

// ── AWS::AppSync::Api (Events API) ─────────────────────────────────────────

type appsyncEventsApiHandler struct{}

func (h *appsyncEventsApiHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	var resp appsyncEventsApiResponse
	if err := appsyncEventsRESTJSON(ctx, router, rCtx.Region, http.MethodPost, "/v2/apis", "CreateApi", appsyncEventsApiBody(props), &resp); err != nil {
		return "", nil, err
	}
	apiID := resp.API.ApiID
	return apiID, appsyncEventsApiAttrs(resp), nil
}

func (h *appsyncEventsApiHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, err := internalAppSyncEventsRequest(ctx, router, rCtx.Region, http.MethodDelete, "/v2/apis/"+url.PathEscape(physicalID), "", nil)
	return err
}

func (h *appsyncEventsApiHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	var resp appsyncEventsApiResponse
	path := "/v2/apis/" + url.PathEscape(physicalID)
	if err := appsyncEventsRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateApi", appsyncEventsApiBody(props), &resp); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncEventsApiAttrs(resp), nil
}

func appsyncEventsApiBody(props map[string]any) map[string]any {
	body := map[string]any{}
	copyStringProp(body, props, "Name", "name")
	copyStringProp(body, props, "OwnerContact", "ownerContact")
	copyStringProp(body, props, "WafWebAclArn", "wafWebAclArn")
	copyConvertedCFProp(body, props, "EventConfig", "eventConfig")
	copyAnyProp(body, props, "XrayEnabled", "xrayEnabled")
	if tags := cfnTagListToMap(props["Tags"]); len(tags) > 0 {
		body["tags"] = tags
	}
	return body
}

func appsyncEventsApiAttrs(resp appsyncEventsApiResponse) map[string]string {
	api := resp.API
	attrs := map[string]string{
		"Ref":    api.ApiArn,
		"ApiId":  api.ApiID,
		"ApiArn": api.ApiArn,
		"Name":   api.Name,
	}
	if api.Dns != nil {
		attrs["Dns.Http"] = api.Dns["HTTP"]
		attrs["Dns.Realtime"] = api.Dns["REALTIME"]
	}
	return attrs
}

// ── AWS::AppSync::ChannelNamespace ─────────────────────────────────────────

type appsyncChannelNamespaceHandler struct{}

func (h *appsyncChannelNamespaceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	name, _ := props["Name"].(string)
	if apiID == "" || name == "" {
		return "", nil, fmt.Errorf("ChannelNamespace: ApiId and Name are required")
	}
	path := fmt.Sprintf("/v2/apis/%s/channelNamespaces", url.PathEscape(apiID))
	var resp appsyncChannelNamespaceResponse
	body, err := appsyncChannelNamespaceBody(ctx, router, rCtx.Region, props)
	if err != nil {
		return "", nil, err
	}
	if err := appsyncEventsRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "CreateChannelNamespace", body, &resp); err != nil {
		return "", nil, err
	}
	return apiID + "/" + name, appsyncChannelNamespaceAttrs(resp), nil
}

func (h *appsyncChannelNamespaceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts, err := appsyncSplitPhysicalID("ChannelNamespace", physicalID, 2)
	if err != nil {
		return nil
	}
	path := fmt.Sprintf("/v2/apis/%s/channelNamespaces/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	_, err = internalAppSyncEventsRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncChannelNamespaceHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts, err := appsyncSplitPhysicalID("ChannelNamespace", physicalID, 2)
	if err != nil {
		return "", nil, err
	}
	if apiID, _ := props["ApiId"].(string); apiID != "" && apiID != parts[0] {
		return "", nil, errReplacementRequired
	}
	if name, _ := props["Name"].(string); name != "" && name != parts[1] {
		return "", nil, errReplacementRequired
	}
	path := fmt.Sprintf("/v2/apis/%s/channelNamespaces/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	var resp appsyncChannelNamespaceResponse
	body, err := appsyncChannelNamespaceBody(ctx, router, rCtx.Region, props)
	if err != nil {
		return "", nil, err
	}
	if err := appsyncEventsRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateChannelNamespace", body, &resp); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncChannelNamespaceAttrs(resp), nil
}

func appsyncChannelNamespaceBody(ctx context.Context, router http.Handler, region string, props map[string]any) (map[string]any, error) {
	body := map[string]any{}
	copyStringProp(body, props, "Name", "name")
	if err := copyStringOrS3Prop(ctx, router, region, body, props, "CodeHandlers", "CodeS3Location", "codeHandlers"); err != nil {
		return nil, fmt.Errorf("ChannelNamespace CodeS3Location: %w", err)
	}
	copyConvertedCFProp(body, props, "HandlerConfigs", "handlerConfigs")
	copyConvertedCFProp(body, props, "PublishAuthModes", "publishAuthModes")
	copyConvertedCFProp(body, props, "SubscribeAuthModes", "subscribeAuthModes")
	if tags := cfnTagListToMap(props["Tags"]); len(tags) > 0 {
		body["tags"] = tags
	}
	return body, nil
}

func appsyncChannelNamespaceAttrs(resp appsyncChannelNamespaceResponse) map[string]string {
	ns := resp.ChannelNamespace
	return map[string]string{
		"Ref":                 ns.ChannelNamespaceArn,
		"ApiId":               ns.ApiID,
		"Name":                ns.Name,
		"ChannelNamespaceArn": ns.ChannelNamespaceArn,
	}
}

func (h *appsyncGraphQLApiHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	var resp appsyncGraphQLApiResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, "/v1/apis", "CreateGraphqlApi", appsyncGraphQLApiBody(props), &resp); err != nil {
		return "", nil, err
	}

	apiID := resp.GraphqlApi.ApiID
	if err := appsyncPutEnvironmentVariables(ctx, router, rCtx.Region, apiID, props); err != nil {
		return "", nil, err
	}
	return apiID, appsyncGraphQLApiAttrs(resp), nil
}

func (h *appsyncGraphQLApiHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/v1/apis/"+physicalID, "", nil)
	return err
}

func (h *appsyncGraphQLApiHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if oldProps != nil {
		if newType, _ := props["ApiType"].(string); newType != "" {
			if oldType, _ := oldProps["ApiType"].(string); oldType != "" && newType != oldType {
				return "", nil, errReplacementRequired
			}
		}
	}

	var resp appsyncGraphQLApiResponse
	path := "/v1/apis/" + url.PathEscape(physicalID)
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateGraphqlApi", appsyncGraphQLApiBody(props), &resp); err != nil {
		return "", nil, err
	}
	if err := appsyncPutEnvironmentVariables(ctx, router, rCtx.Region, physicalID, props); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncGraphQLApiAttrs(resp), nil
}

func appsyncPutEnvironmentVariables(ctx context.Context, router http.Handler, region, apiID string, props map[string]any) error {
	raw, ok := props["EnvironmentVariables"]
	if !ok {
		return nil
	}
	vars, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	converted := make(map[string]string, len(vars))
	for key, value := range vars {
		converted[key] = fmt.Sprintf("%v", value)
	}
	path := fmt.Sprintf("/v1/apis/%s/environmentVariables", url.PathEscape(apiID))
	return appsyncRESTJSON(ctx, router, region, http.MethodPut, path, "PutGraphqlApiEnvironmentVariables", map[string]any{"environmentVariables": converted}, nil)
}

// ── AWS::AppSync::GraphQLSchema ────────────────────────────────────────────

type appsyncGraphQLSchemaHandler struct{}

func (h *appsyncGraphQLSchemaHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	definition, _ := props["Definition"].(string)
	if apiID == "" {
		return "", nil, fmt.Errorf("GraphQLSchema: ApiId is required")
	}
	if definition == "" {
		if location, _ := props["DefinitionS3Location"].(string); location != "" {
			fetched, err := appsyncFetchS3BackedProperty(ctx, router, rCtx.Region, location)
			if err != nil {
				return "", nil, fmt.Errorf("GraphQLSchema DefinitionS3Location: %w", err)
			}
			definition = fetched
		}
	}
	if definition == "" {
		return "", nil, fmt.Errorf("GraphQLSchema: Definition is required")
	}
	body := map[string]any{"definition": base64.StdEncoding.EncodeToString([]byte(definition))}
	path := fmt.Sprintf("/v1/apis/%s/schemacreation", url.PathEscape(apiID))
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "StartSchemaCreation", body, nil); err != nil {
		return "", nil, err
	}
	physicalID := apiID + "/schema"
	return physicalID, map[string]string{
		"Ref": apiID + "GraphQLSchema",
		"Id":  physicalID,
	}, nil
}

func (h *appsyncGraphQLSchemaHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	return nil
}

func (h *appsyncGraphQLSchemaHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts, err := appsyncSplitPhysicalID("GraphQLSchema", physicalID, 2)
	if err != nil {
		return "", nil, err
	}
	if apiID, _ := props["ApiId"].(string); apiID != "" && apiID != parts[0] {
		return "", nil, errReplacementRequired
	}
	return h.Create(ctx, router, cfg, props, rCtx)
}

// ── AWS::AppSync::ApiKey ───────────────────────────────────────────────────

type appsyncApiKeyHandler struct{}

func (h *appsyncApiKeyHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	if apiID == "" {
		return "", nil, fmt.Errorf("ApiKey: ApiId is required")
	}
	body := map[string]any{}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, ok := props["Expires"]; ok {
		body["expires"] = v
	}
	path := fmt.Sprintf("/v1/apis/%s/apikeys", url.PathEscape(apiID))
	var resp appsyncApiKeyResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "CreateApiKey", body, &resp); err != nil {
		return "", nil, err
	}
	keyID := resp.ApiKey.ID
	return apiID + "/" + keyID, appsyncApiKeyAttrs(cfg, rCtx.Region, apiID, keyID), nil
}

func (h *appsyncApiKeyHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts, err := appsyncSplitPhysicalID("ApiKey", physicalID, 2)
	if err != nil {
		return nil
	}
	path := fmt.Sprintf("/v1/apis/%s/apikeys/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	_, err = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncApiKeyHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts, err := appsyncSplitPhysicalID("ApiKey", physicalID, 2)
	if err != nil {
		return "", nil, err
	}
	if apiID, _ := props["ApiId"].(string); apiID != "" && apiID != parts[0] {
		return "", nil, errReplacementRequired
	}
	body := map[string]any{}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, ok := props["Expires"]; ok {
		body["expires"] = v
	}
	path := fmt.Sprintf("/v1/apis/%s/apikeys/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateApiKey", body, nil); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncApiKeyAttrs(cfg, rCtx.Region, parts[0], parts[1]), nil
}

// ── AWS::AppSync::FunctionConfiguration ───────────────────────────────────

type appsyncFunctionConfigurationHandler struct{}

func (h *appsyncFunctionConfigurationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	if apiID == "" {
		return "", nil, fmt.Errorf("FunctionConfiguration: ApiId is required")
	}
	body, err := appsyncFunctionBody(ctx, router, rCtx.Region, props)
	if err != nil {
		return "", nil, err
	}
	path := fmt.Sprintf("/v1/apis/%s/functions", url.PathEscape(apiID))
	var resp appsyncFunctionResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "CreateFunction", body, &resp); err != nil {
		return "", nil, err
	}
	fn := resp.FunctionConfiguration
	return apiID + "/" + fn.FunctionID, appsyncFunctionAttrs(resp), nil
}

func (h *appsyncFunctionConfigurationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts, err := appsyncSplitPhysicalID("FunctionConfiguration", physicalID, 2)
	if err != nil {
		return nil
	}
	path := fmt.Sprintf("/v1/apis/%s/functions/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	_, err = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncFunctionConfigurationHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts, err := appsyncSplitPhysicalID("FunctionConfiguration", physicalID, 2)
	if err != nil {
		return "", nil, err
	}
	if apiID, _ := props["ApiId"].(string); apiID != "" && apiID != parts[0] {
		return "", nil, errReplacementRequired
	}
	body, err := appsyncFunctionBody(ctx, router, rCtx.Region, props)
	if err != nil {
		return "", nil, err
	}
	path := fmt.Sprintf("/v1/apis/%s/functions/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	var resp appsyncFunctionResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateFunction", body, &resp); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncFunctionAttrs(resp), nil
}

func appsyncFunctionBody(ctx context.Context, router http.Handler, region string, props map[string]any) (map[string]any, error) {
	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["DataSourceName"].(string); v != "" {
		body["dataSourceName"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if v, _ := props["FunctionVersion"].(string); v != "" {
		body["functionVersion"] = v
	}
	if err := copyStringOrS3Prop(ctx, router, region, body, props, "RequestMappingTemplate", "RequestMappingTemplateS3Location", "requestMappingTemplate"); err != nil {
		return nil, fmt.Errorf("FunctionConfiguration RequestMappingTemplateS3Location: %w", err)
	}
	if err := copyStringOrS3Prop(ctx, router, region, body, props, "ResponseMappingTemplate", "ResponseMappingTemplateS3Location", "responseMappingTemplate"); err != nil {
		return nil, fmt.Errorf("FunctionConfiguration ResponseMappingTemplateS3Location: %w", err)
	}
	if v, ok := props["MaxBatchSize"]; ok {
		body["maxBatchSize"] = v
	}
	if err := copyStringOrS3Prop(ctx, router, region, body, props, "Code", "CodeS3Location", "code"); err != nil {
		return nil, fmt.Errorf("FunctionConfiguration CodeS3Location: %w", err)
	}
	if v, ok := props["Runtime"]; ok {
		body["runtime"] = v
	}
	if v, ok := props["SyncConfig"]; ok {
		body["syncConfig"] = v
	}
	return body, nil
}

func copyStringOrS3Prop(ctx context.Context, router http.Handler, region string, dst map[string]any, props map[string]any, inlineName, s3Name, jsonName string) error {
	if v, _ := props[inlineName].(string); v != "" {
		dst[jsonName] = v
		return nil
	}
	location, _ := props[s3Name].(string)
	if location == "" {
		return nil
	}
	fetched, err := appsyncFetchS3BackedProperty(ctx, router, region, location)
	if err != nil {
		return err
	}
	dst[jsonName] = fetched
	return nil
}

func appsyncFetchS3BackedProperty(ctx context.Context, router http.Handler, region, location string) (string, error) {
	u, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse location %q: %w", location, err)
	}
	path := u.Path
	if u.Scheme == "s3" {
		path = "/" + u.Host + u.Path
	}
	if path == "" || path == "/" {
		return "", fmt.Errorf("invalid S3 location %q", location)
	}
	rec, err := internalRequest(ctx, router, region, http.MethodGet, path, "", nil)
	if err != nil {
		return "", err
	}
	return rec.Body.String(), nil
}

// ── AWS::AppSync::DataSource ──────────────────────────────────────────────

type appsyncDataSourceHandler struct{}

func (h *appsyncDataSourceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	if apiID == "" {
		return "", nil, fmt.Errorf("DataSource: ApiId is required")
	}
	name, _ := props["Name"].(string)
	path := fmt.Sprintf("/v1/apis/%s/datasources", url.PathEscape(apiID))
	var resp appsyncDataSourceResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "CreateDataSource", appsyncDataSourceBody(props), &resp); err != nil {
		return "", nil, err
	}
	return apiID + "/" + name, appsyncDataSourceAttrs(resp), nil
}

func (h *appsyncDataSourceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts, err := appsyncSplitPhysicalID("DataSource", physicalID, 2)
	if err != nil {
		return nil
	}
	path := fmt.Sprintf("/v1/apis/%s/datasources/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	_, err = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncDataSourceHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts, err := appsyncSplitPhysicalID("DataSource", physicalID, 2)
	if err != nil {
		return "", nil, err
	}
	if apiID, _ := props["ApiId"].(string); apiID != "" && apiID != parts[0] {
		return "", nil, errReplacementRequired
	}
	if name, _ := props["Name"].(string); name != "" && name != parts[1] {
		return "", nil, errReplacementRequired
	}
	path := fmt.Sprintf("/v1/apis/%s/datasources/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	var resp appsyncDataSourceResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateDataSource", appsyncDataSourceBody(props), &resp); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncDataSourceAttrs(resp), nil
}

// ── AWS::AppSync::Resolver ────────────────────────────────────────────────

type appsyncResolverHandler struct{}

func (h *appsyncResolverHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	typeName, _ := props["TypeName"].(string)
	fieldName, _ := props["FieldName"].(string)
	if apiID == "" || typeName == "" || fieldName == "" {
		return "", nil, fmt.Errorf("Resolver: ApiId, TypeName, and FieldName are required")
	}
	body, err := appsyncResolverBody(ctx, router, rCtx.Region, props)
	if err != nil {
		return "", nil, err
	}
	path := fmt.Sprintf("/v1/apis/%s/types/%s/resolvers", url.PathEscape(apiID), url.PathEscape(typeName))
	var resp appsyncResolverResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "CreateResolver", body, &resp); err != nil {
		return "", nil, err
	}
	return apiID + "/" + typeName + "/" + fieldName, appsyncResolverAttrs(resp), nil
}

func (h *appsyncResolverHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts, err := appsyncSplitPhysicalID("Resolver", physicalID, 3)
	if err != nil {
		return nil
	}
	path := fmt.Sprintf("/v1/apis/%s/types/%s/resolvers/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), url.PathEscape(parts[2]))
	_, err = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncResolverHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts, err := appsyncSplitPhysicalID("Resolver", physicalID, 3)
	if err != nil {
		return "", nil, err
	}
	if apiID, _ := props["ApiId"].(string); apiID != "" && apiID != parts[0] {
		return "", nil, errReplacementRequired
	}
	if typeName, _ := props["TypeName"].(string); typeName != "" && typeName != parts[1] {
		return "", nil, errReplacementRequired
	}
	if fieldName, _ := props["FieldName"].(string); fieldName != "" && fieldName != parts[2] {
		return "", nil, errReplacementRequired
	}
	body, err := appsyncResolverBody(ctx, router, rCtx.Region, props)
	if err != nil {
		return "", nil, err
	}
	path := fmt.Sprintf("/v1/apis/%s/types/%s/resolvers/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]), url.PathEscape(parts[2]))
	var resp appsyncResolverResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateResolver", body, &resp); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncResolverAttrs(resp), nil
}

// ── AWS::AppSync::DomainName ───────────────────────────────────────────────

type appsyncDomainNameHandler struct{}

func (h *appsyncDomainNameHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	domainName, _ := props["DomainName"].(string)
	if domainName == "" {
		return "", nil, fmt.Errorf("DomainName: DomainName is required")
	}
	body := map[string]any{}
	copyStringProp(body, props, "DomainName", "domainName")
	copyStringProp(body, props, "CertificateArn", "certificateArn")
	copyStringProp(body, props, "Description", "description")
	var resp appsyncDomainNameResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, "/v1/domainnames", "CreateDomainName", body, &resp); err != nil {
		return "", nil, err
	}
	attrs := appsyncDomainNameAttrs(cfg, rCtx.Region, resp)
	return domainName, attrs, nil
}

func (h *appsyncDomainNameHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/v1/domainnames/"+url.PathEscape(physicalID), "", nil)
	return err
}

func (h *appsyncDomainNameHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if domainName, _ := props["DomainName"].(string); domainName != "" && domainName != physicalID {
		return "", nil, errReplacementRequired
	}
	body := map[string]any{}
	copyStringProp(body, props, "Description", "description")
	var resp appsyncDomainNameResponse
	path := "/v1/domainnames/" + url.PathEscape(physicalID)
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateDomainName", body, &resp); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncDomainNameAttrs(cfg, rCtx.Region, resp), nil
}

func appsyncDomainNameAttrs(cfg *config.Config, region string, resp appsyncDomainNameResponse) map[string]string {
	dn := resp.DomainNameConfig
	arn := protocol.ARN(region, cfg.AccountID, "appsync", "domainnames/"+dn.DomainName)
	return map[string]string{
		"Ref":               dn.DomainName,
		"DomainName":        dn.DomainName,
		"DomainNameArn":     arn,
		"AppSyncDomainName": dn.AppsyncDomainName,
		"AppsyncDomainName": dn.AppsyncDomainName,
		"HostedZoneId":      dn.HostedZoneID,
	}
}

// ── AWS::AppSync::DomainNameApiAssociation ─────────────────────────────────

type appsyncDomainNameApiAssociationHandler struct{}

func (h *appsyncDomainNameApiAssociationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	domainName, _ := props["DomainName"].(string)
	apiID, _ := props["ApiId"].(string)
	if domainName == "" || apiID == "" {
		return "", nil, fmt.Errorf("DomainNameApiAssociation: DomainName and ApiId are required")
	}
	path := fmt.Sprintf("/v1/domainnames/%s/apiassociation", url.PathEscape(domainName))
	var resp appsyncApiAssociationResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "AssociateApi", map[string]any{"apiId": apiID}, &resp); err != nil {
		return "", nil, err
	}
	physicalID := domainName + "/" + apiID
	return physicalID, map[string]string{"Ref": physicalID, "DomainName": resp.ApiAssociation.DomainName, "ApiId": resp.ApiAssociation.ApiID, "AssociationStatus": resp.ApiAssociation.AssociationStatus}, nil
}

func (h *appsyncDomainNameApiAssociationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts, err := appsyncSplitPhysicalID("DomainNameApiAssociation", physicalID, 2)
	if err != nil {
		return nil
	}
	path := fmt.Sprintf("/v1/domainnames/%s/apiassociation", url.PathEscape(parts[0]))
	_, err = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncDomainNameApiAssociationHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	parts, err := appsyncSplitPhysicalID("DomainNameApiAssociation", physicalID, 2)
	if err != nil {
		return "", nil, err
	}
	if domainName, _ := props["DomainName"].(string); domainName != "" && domainName != parts[0] {
		return "", nil, errReplacementRequired
	}
	return h.Create(ctx, router, cfg, props, rCtx)
}

// ── AWS::AppSync::ApiCache ─────────────────────────────────────────────────

type appsyncApiCacheHandler struct{}

func (h *appsyncApiCacheHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	if apiID == "" {
		return "", nil, fmt.Errorf("ApiCache: ApiId is required")
	}
	path := fmt.Sprintf("/v1/apis/%s/ApiCaches", url.PathEscape(apiID))
	var resp appsyncApiCacheResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "CreateApiCache", appsyncApiCacheBody(props), &resp); err != nil {
		return "", nil, err
	}
	return apiID, appsyncApiCacheAttrs(resp), nil
}

func (h *appsyncApiCacheHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/v1/apis/"+url.PathEscape(physicalID)+"/ApiCaches", "", nil)
	return err
}

func (h *appsyncApiCacheHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if apiID, _ := props["ApiId"].(string); apiID != "" && apiID != physicalID {
		return "", nil, errReplacementRequired
	}
	path := fmt.Sprintf("/v1/apis/%s/ApiCaches/update", url.PathEscape(physicalID))
	var resp appsyncApiCacheResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "UpdateApiCache", appsyncApiCacheBody(props), &resp); err != nil {
		return "", nil, err
	}
	return physicalID, appsyncApiCacheAttrs(resp), nil
}

func appsyncApiCacheBody(props map[string]any) map[string]any {
	body := map[string]any{}
	copyStringProp(body, props, "Type", "type")
	copyStringProp(body, props, "ApiCachingBehavior", "apiCachingBehavior")
	copyStringProp(body, props, "HealthMetricsConfig", "healthMetricsConfig")
	copyAnyProp(body, props, "Ttl", "ttl")
	copyAnyProp(body, props, "TransitEncryptionEnabled", "transitEncryptionEnabled")
	copyAnyProp(body, props, "AtRestEncryptionEnabled", "atRestEncryptionEnabled")
	return body
}

func appsyncApiCacheAttrs(resp appsyncApiCacheResponse) map[string]string {
	cache := resp.ApiCache
	return map[string]string{"Ref": cache.ApiID, "ApiId": cache.ApiID, "Status": cache.Status, "Type": cache.Type, "ApiCachingBehavior": cache.ApiCachingBehavior}
}

// ── AWS::AppSync::SourceApiAssociation ─────────────────────────────────────

type appsyncSourceApiAssociationHandler struct{}

func (h *appsyncSourceApiAssociationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	mergedAPI, _ := props["MergedApiIdentifier"].(string)
	sourceAPI, _ := props["SourceApiIdentifier"].(string)
	if mergedAPI == "" || sourceAPI == "" {
		return "", nil, fmt.Errorf("SourceApiAssociation: MergedApiIdentifier and SourceApiIdentifier are required")
	}
	body := map[string]any{"sourceApiIdentifier": sourceAPI}
	copyStringProp(body, props, "Description", "description")
	copyAnyProp(body, props, "SourceApiAssociationConfig", "sourceApiAssociationConfig")
	path := fmt.Sprintf("/v1/mergedApis/%s/sourceApiAssociations", url.PathEscape(mergedAPI))
	var resp appsyncSourceApiAssociationResponse
	if err := appsyncRESTJSON(ctx, router, rCtx.Region, http.MethodPost, path, "AssociateSourceGraphqlApi", body, &resp); err != nil {
		return "", nil, err
	}
	assoc := resp.SourceApiAssociation
	return mergedAPI + "/" + assoc.AssociationID, appsyncSourceApiAssociationAttrs(resp), nil
}

func (h *appsyncSourceApiAssociationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts, err := appsyncSplitPhysicalID("SourceApiAssociation", physicalID, 2)
	if err != nil {
		return nil
	}
	path := fmt.Sprintf("/v1/mergedApis/%s/sourceApiAssociations/%s", url.PathEscape(parts[0]), url.PathEscape(parts[1]))
	_, err = internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncSourceApiAssociationHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

func appsyncSourceApiAssociationAttrs(resp appsyncSourceApiAssociationResponse) map[string]string {
	assoc := resp.SourceApiAssociation
	return map[string]string{
		"Ref":                              assoc.AssociationArn,
		"AssociationArn":                   assoc.AssociationArn,
		"AssociationId":                    assoc.AssociationID,
		"SourceApiId":                      assoc.SourceApiID,
		"SourceApiArn":                     assoc.SourceApiArn,
		"MergedApiId":                      assoc.MergedApiID,
		"MergedApiArn":                     assoc.MergedApiArn,
		"SourceApiAssociationStatus":       assoc.SourceApiAssociationStatus,
		"SourceApiAssociationStatusDetail": assoc.SourceApiAssociationStatusDetail,
		"LastSuccessfulMergeDate":          strconv.FormatInt(assoc.LastSuccessfulMergeDate, 10),
	}
}

// ── AWS::CloudFront::Distribution ─────────────────────────────────────────

type cloudfrontDistributionHandler struct{}

func (h *cloudfrontDistributionHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	distConfig, _ := props["DistributionConfig"].(map[string]any)
	if distConfig == nil {
		distConfig = props // Some templates put config at the top level.
	}

	if _, ok := distConfig["CallerReference"].(string); !ok {
		distConfig["CallerReference"] = fmt.Sprintf("%s-%d", rCtx.StackName, len(rCtx.Resources))
	}
	if _, ok := distConfig["Enabled"].(bool); !ok {
		distConfig["Enabled"] = true
	}
	ensureCloudFrontDistributionDefaults(distConfig)

	xmlData, err := marshalCloudFrontDistributionConfigXML(distConfig)
	if err != nil {
		return "", nil, fmt.Errorf("CloudFront: marshal config: %w", err)
	}

	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/2020-05-31/distribution", "application/xml", xmlData)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDistribution: %w", err)
	}

	body := rec.Body.String()
	id := extractXMLValue(body, "Id")
	domainName := extractXMLValue(body, "DomainName")

	attrs := map[string]string{
		"DomainName": domainName,
		"Id":         id,
	}
	return id, attrs, nil
}

func ensureCloudFrontDistributionDefaults(distConfig map[string]any) {
	origins := cloudFrontListItems(distConfig["Origins"])
	if len(origins) == 0 {
		distConfig["Origins"] = []any{map[string]any{"Id": "default", "DomainName": "localhost"}}
		origins = cloudFrontListItems(distConfig["Origins"])
	}
	dcb, _ := distConfig["DefaultCacheBehavior"].(map[string]any)
	if dcb == nil {
		dcb = map[string]any{}
		distConfig["DefaultCacheBehavior"] = dcb
	}
	if _, ok := dcb["ViewerProtocolPolicy"].(string); !ok {
		dcb["ViewerProtocolPolicy"] = "allow-all"
	}
	if target, _ := dcb["TargetOriginId"].(string); target == "" && len(origins) > 0 {
		if first, _ := origins[0].(map[string]any); first != nil {
			dcb["TargetOriginId"], _ = first["Id"].(string)
		}
	}
}

func marshalCloudFrontDistributionConfigXML(distConfig map[string]any) ([]byte, error) {
	return marshalCFNXML("DistributionConfig", distConfig, cloudFrontTopLevelList, cloudFrontItemName, cfnXMLItemsWrapper)
}

func cloudFrontTopLevelList(name string, value any) ([]any, bool) {
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	switch name {
	case "Aliases", "CacheBehaviors", "CustomErrorResponses", "Origins", "OriginGroups":
		return items, true
	}
	return nil, false
}

func cloudFrontListItems(value any) []any {
	switch v := value.(type) {
	case []any:
		return v
	case map[string]any:
		if items, ok := v["Items"].([]any); ok {
			return items
		}
	}
	return nil
}

func cloudFrontItemName(parent string) string {
	switch parent {
	case "Aliases":
		return "CNAME"
	case "AllowedMethods", "CachedMethods":
		return "Method"
	case "CacheBehaviors":
		return "CacheBehavior"
	case "CustomErrorResponses":
		return "CustomErrorResponse"
	case "CustomHeaders":
		return "OriginCustomHeader"
	case "FunctionAssociations":
		return "FunctionAssociation"
	case "GeoRestriction":
		return "Location"
	case "LambdaFunctionAssociations":
		return "LambdaFunctionAssociation"
	case "OriginGroups":
		return "OriginGroup"
	case "Origins":
		return "Origin"
	case "Members":
		return "OriginGroupMember"
	case "StatusCodes":
		return "StatusCode"
	}
	return "Item"
}

func (h *cloudfrontDistributionHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	basePath := "/2020-05-31/distribution/" + physicalID

	// Step 1: GET distribution to get current ETag.
	rec, err := cfInternalRequest(ctx, router, rCtx.Region, http.MethodGet, basePath, "", nil, nil)
	if err != nil {
		return nil // Already deleted or doesn't exist.
	}
	etag := rec.Header().Get("ETag")

	// Step 2: If distribution is enabled, disable it first.
	if strings.Contains(rec.Body.String(), "<Enabled>true</Enabled>") {
		// Get config to modify.
		cfgRec, err := cfInternalRequest(ctx, router, rCtx.Region, http.MethodGet, basePath+"/config", "", nil, nil)
		if err != nil {
			return fmt.Errorf("GetDistributionConfig: %w", err)
		}
		cfgEtag := cfgRec.Header().Get("ETag")
		cfgBody := cfgRec.Body.String()

		// Replace Enabled=true with Enabled=false.
		cfgBody = strings.Replace(cfgBody, "<Enabled>true</Enabled>", "<Enabled>false</Enabled>", 1)

		// PUT updated config.
		putRec, err := cfInternalRequest(ctx, router, rCtx.Region, http.MethodPut, basePath+"/config", "application/xml", []byte(cfgBody), map[string]string{"If-Match": cfgEtag})
		if err != nil {
			return fmt.Errorf("UpdateDistribution (disable): %w", err)
		}
		etag = putRec.Header().Get("ETag")
	}

	// Step 3: DELETE with If-Match.
	_, err = cfInternalRequest(ctx, router, rCtx.Region, http.MethodDelete, basePath, "", nil, map[string]string{"If-Match": etag})
	return err
}

func (h *cloudfrontDistributionHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// cfInternalRequest is like internalRequest but supports additional headers
// (needed for CloudFront's If-Match/ETag flow).
func cfInternalRequest(ctx context.Context, router http.Handler, region, method, path, contentType string, body []byte, headers map[string]string) (*httptest.ResponseRecorder, error) {
	req, err := http.NewRequestWithContext(ctx, method, path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if region != "" {
		req.Header.Set("X-Overcast-Region", region)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code >= 400 {
		return rec, fmt.Errorf("HTTP %d: %s", rec.Code, rec.Body.String())
	}
	return rec, nil
}

// ── AWS::SES::Template ────────────────────────────────────────────────────

type sesTemplateHandler struct{}

func (h *sesTemplateHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	// CloudFormation's AWS::SES::Template wraps the template inside a
	// "Template" property.
	tmpl, _ := props["Template"].(map[string]any)
	if tmpl == nil {
		tmpl = props
	}

	name, _ := tmpl["TemplateName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-template", rCtx.StackName)
	}

	params := map[string]string{
		"Action":                "CreateTemplate",
		"Template.TemplateName": name,
	}
	if v, _ := tmpl["SubjectPart"].(string); v != "" {
		params["Template.SubjectPart"] = v
	}
	if v, _ := tmpl["TextPart"].(string); v != "" {
		params["Template.TextPart"] = v
	}
	if v, _ := tmpl["HtmlPart"].(string); v != "" {
		params["Template.HtmlPart"] = v
	}

	_, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateTemplate: %w", err)
	}

	attrs := map[string]string{
		"Id": name,
	}
	return name, attrs, nil
}

func (h *sesTemplateHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":       "DeleteTemplate",
		"TemplateName": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────

// fmtPropString converts a numeric or string property to a string suitable
// ── AWS::ElastiCache::CacheCluster ───────────────────────────────────────────

type elastiCacheCacheClusterHandler struct{}

func (h *elastiCacheCacheClusterHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	id, _ := props["CacheClusterId"].(string)
	if id == "" {
		id = fmt.Sprintf("%s-cache", rCtx.StackName)
	}
	params := map[string]string{
		"Action":         "CreateCacheCluster",
		"Version":        "2015-02-02",
		"CacheClusterId": id,
	}
	if v, _ := props["Engine"].(string); v != "" {
		params["Engine"] = v
	}
	if v, _ := props["EngineVersion"].(string); v != "" {
		params["EngineVersion"] = v
	}
	if v, _ := props["CacheNodeType"].(string); v != "" {
		params["CacheNodeType"] = v
	}
	if v := fmtPropString(props, "NumCacheNodes"); v != "" {
		params["NumCacheNodes"] = v
	}
	if v, _ := props["CacheSubnetGroupName"].(string); v != "" {
		params["CacheSubnetGroupName"] = v
	}
	if v, _ := props["ReplicationGroupId"].(string); v != "" {
		params["ReplicationGroupId"] = v
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateCacheCluster: %w", err)
	}
	body := rec.Body.String()
	arn := extractXMLValue(body, "ARN")
	endpointAddr := extractXMLValue(body, "Address")
	endpointPort := extractXMLValue(body, "Port")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticache:%s:%s:cluster:%s", rCtx.Region, rCtx.AccountID, id)
	}
	return id, map[string]string{
		"Arn":                           arn,
		"ConfigurationEndpoint.Address": endpointAddr,
		"ConfigurationEndpoint.Port":    endpointPort,
	}, nil
}

func (h *elastiCacheCacheClusterHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":         "DeleteCacheCluster",
		"Version":        "2015-02-02",
		"CacheClusterId": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

// ── AWS::ElastiCache::ServerlessCache ────────────────────────────────────────

type elastiCacheServerlessCacheHandler struct{}

func (h *elastiCacheServerlessCacheHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["ServerlessCacheName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-serverless-cache", rCtx.StackName)
	}
	params := map[string]string{
		"Action":              "CreateServerlessCache",
		"Version":             "2015-02-02",
		"ServerlessCacheName": name,
	}
	addElastiCacheServerlessParams(params, props)
	if tags, ok := props["Tags"].([]any); ok {
		for i, item := range tags {
			if tag, ok := item.(map[string]any); ok {
				if key, _ := tag["Key"].(string); key != "" {
					params[fmt.Sprintf("Tags.Tag.%d.Key", i+1)] = key
					params[fmt.Sprintf("Tags.Tag.%d.Value", i+1)] = fmt.Sprintf("%v", tag["Value"])
				}
			}
		}
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateServerlessCache: %w", err)
	}
	body := rec.Body.String()
	arn := extractXMLValue(body, "ARN")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticache:%s:%s:serverlesscache:%s", rCtx.Region, rCtx.AccountID, name)
	}
	return name, map[string]string{
		"ARN":                    arn,
		"Arn":                    arn,
		"Endpoint.Address":       extractXMLValue(body, "Address"),
		"Endpoint.Port":          extractXMLValue(body, "Port"),
		"ReaderEndpoint.Address": extractXMLValue(body, "Address"),
		"ReaderEndpoint.Port":    extractXMLValue(body, "Port"),
		"FullEngineVersion":      extractXMLValue(body, "FullEngineVersion"),
		"Status":                 extractXMLValue(body, "Status"),
		"CreateTime":             extractXMLValue(body, "CreateTime"),
	}, nil
}

func (h *elastiCacheServerlessCacheHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":              "DeleteServerlessCache",
		"Version":             "2015-02-02",
		"ServerlessCacheName": physicalID,
	}
	if v := fmtPropString(map[string]any{}, "FinalSnapshotName"); v != "" {
		params["FinalSnapshotName"] = v
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *elastiCacheServerlessCacheHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if newName, _ := props["ServerlessCacheName"].(string); newName != "" && newName != physicalID {
		return "", nil, errReplacementRequired
	}
	if oldProps != nil {
		for _, key := range []string{"KmsKeyId", "SubnetIds", "SnapshotArnsToRestore"} {
			if fmt.Sprintf("%v", props[key]) != fmt.Sprintf("%v", oldProps[key]) && props[key] != nil {
				return "", nil, errReplacementRequired
			}
		}
	}
	params := map[string]string{
		"Action":              "ModifyServerlessCache",
		"Version":             "2015-02-02",
		"ServerlessCacheName": physicalID,
	}
	addElastiCacheServerlessParams(params, props)
	delete(params, "KmsKeyId")
	delete(params, "NetworkType")
	for key := range params {
		if strings.HasPrefix(key, "SubnetIds.") || strings.HasPrefix(key, "SnapshotArnsToRestore.") {
			delete(params, key)
		}
	}
	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("ModifyServerlessCache: %w", err)
	}
	body := rec.Body.String()
	arn := extractXMLValue(body, "ARN")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticache:%s:%s:serverlesscache:%s", rCtx.Region, rCtx.AccountID, physicalID)
	}
	return physicalID, map[string]string{
		"ARN":                    arn,
		"Arn":                    arn,
		"Endpoint.Address":       extractXMLValue(body, "Address"),
		"Endpoint.Port":          extractXMLValue(body, "Port"),
		"ReaderEndpoint.Address": extractXMLValue(body, "Address"),
		"ReaderEndpoint.Port":    extractXMLValue(body, "Port"),
		"FullEngineVersion":      extractXMLValue(body, "FullEngineVersion"),
		"Status":                 extractXMLValue(body, "Status"),
		"CreateTime":             extractXMLValue(body, "CreateTime"),
	}, nil
}

func addElastiCacheServerlessParams(params map[string]string, props map[string]any) {
	for _, key := range []string{"Engine", "MajorEngineVersion", "Description", "DailySnapshotTime", "KmsKeyId", "NetworkType", "SnapshotRetentionLimit", "UserGroupId"} {
		if v := fmtPropString(props, key); v != "" {
			params[key] = v
		}
	}
	if limits, ok := props["CacheUsageLimits"].(map[string]any); ok {
		if data, ok := limits["DataStorage"].(map[string]any); ok {
			if v := fmtPropString(data, "Maximum"); v != "" {
				params["CacheUsageLimits.DataStorage.Maximum"] = v
			}
			if v := fmtPropString(data, "Unit"); v != "" {
				params["CacheUsageLimits.DataStorage.Unit"] = v
			}
		}
		if ecpu, ok := limits["ECPUPerSecond"].(map[string]any); ok {
			if v := fmtPropString(ecpu, "Maximum"); v != "" {
				params["CacheUsageLimits.ECPUPerSecond.Maximum"] = v
			}
		}
	}
	if subnets, ok := props["SubnetIds"].([]any); ok {
		for i, subnet := range subnets {
			if id, _ := subnet.(string); id != "" {
				params[fmt.Sprintf("SubnetIds.SubnetId.%d", i+1)] = id
			}
		}
	}
	if groups, ok := props["SecurityGroupIds"].([]any); ok {
		for i, group := range groups {
			if id, _ := group.(string); id != "" {
				params[fmt.Sprintf("SecurityGroupIds.SecurityGroupId.%d", i+1)] = id
			}
		}
	}
	if snapshots, ok := props["SnapshotArnsToRestore"].([]any); ok {
		for i, snapshot := range snapshots {
			if arn, _ := snapshot.(string); arn != "" {
				params[fmt.Sprintf("SnapshotArnsToRestore.SnapshotArn.%d", i+1)] = arn
			}
		}
	}
}

// ── AWS::ElastiCache::ReplicationGroup ────────────────────────────────────────

type elastiCacheReplicationGroupHandler struct{}

func (h *elastiCacheReplicationGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	id, _ := props["ReplicationGroupId"].(string)
	if id == "" {
		id = fmt.Sprintf("%s-rg", rCtx.StackName)
	}
	params := map[string]string{
		"Action":                      "CreateReplicationGroup",
		"Version":                     "2015-02-02",
		"ReplicationGroupId":          id,
		"ReplicationGroupDescription": fmt.Sprintf("%v", props["ReplicationGroupDescription"]),
	}
	if v, _ := props["CacheNodeType"].(string); v != "" {
		params["CacheNodeType"] = v
	}
	if v := props["AutomaticFailoverEnabled"]; v != nil {
		params["AutomaticFailoverEnabled"] = cfnScalarString(v)
	}
	if v := props["MultiAZEnabled"]; v != nil {
		params["MultiAZEnabled"] = cfnScalarString(v)
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateReplicationGroup: %w", err)
	}
	body := rec.Body.String()
	arn := extractXMLValue(body, "ARN")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticache:%s:%s:replicationgroup:%s", rCtx.Region, rCtx.AccountID, id)
	}
	return id, map[string]string{
		"Arn":                           arn,
		"PrimaryEndPoint.Address":       extractXMLValue(body, "Address"),
		"PrimaryEndPoint.Port":          extractXMLValue(body, "Port"),
		"ConfigurationEndPoint.Address": extractXMLValue(body, "Address"),
		"ConfigurationEndPoint.Port":    extractXMLValue(body, "Port"),
	}, nil
}

func (h *elastiCacheReplicationGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":             "DeleteReplicationGroup",
		"Version":            "2015-02-02",
		"ReplicationGroupId": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *elastiCacheReplicationGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	if newID, _ := props["ReplicationGroupId"].(string); newID != "" && newID != physicalID {
		return "", nil, errReplacementRequired
	}
	if oldProps != nil {
		if newEngine, _ := props["Engine"].(string); newEngine != "" {
			if oldEngine, _ := oldProps["Engine"].(string); oldEngine != "" && newEngine != oldEngine {
				return "", nil, errReplacementRequired
			}
		}
		if newSubnet, _ := props["CacheSubnetGroupName"].(string); newSubnet != "" {
			if oldSubnet, _ := oldProps["CacheSubnetGroupName"].(string); oldSubnet != "" && newSubnet != oldSubnet {
				return "", nil, errReplacementRequired
			}
		}
	}

	params := map[string]string{
		"Action":             "ModifyReplicationGroup",
		"Version":            "2015-02-02",
		"ReplicationGroupId": physicalID,
	}
	if v, _ := props["ReplicationGroupDescription"].(string); v != "" {
		params["ReplicationGroupDescription"] = v
	}
	if v, _ := props["CacheNodeType"].(string); v != "" {
		params["CacheNodeType"] = v
	}
	if v := props["AutomaticFailoverEnabled"]; v != nil {
		params["AutomaticFailoverEnabled"] = cfnScalarString(v)
	}
	if v := props["MultiAZEnabled"]; v != nil {
		params["MultiAZEnabled"] = cfnScalarString(v)
	}
	if v, _ := props["NotificationTopicArn"].(string); v != "" {
		params["NotificationTopicArn"] = v
	}
	if v := props["SnapshotRetentionLimit"]; v != nil {
		params["SnapshotRetentionLimit"] = cfnScalarString(v)
	}
	if v, _ := props["SnapshotWindow"].(string); v != "" {
		params["SnapshotWindow"] = v
	}
	if v, _ := props["PreferredMaintenanceWindow"].(string); v != "" {
		params["PreferredMaintenanceWindow"] = v
	}
	if sgs, ok := props["SecurityGroupIds"].([]any); ok {
		for i, sg := range sgs {
			if s, _ := sg.(string); s != "" {
				params[fmt.Sprintf("SecurityGroupIds.SecurityGroupId.%d", i+1)] = s
			}
		}
	}

	if _, err := internalQuery(ctx, router, rCtx.Region, params); err != nil {
		return "", nil, fmt.Errorf("ModifyReplicationGroup: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::ElastiCache::SubnetGroup ─────────────────────────────────────────────

type elastiCacheSubnetGroupHandler struct{}

func (h *elastiCacheSubnetGroupHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["CacheSubnetGroupName"].(string)
	if name == "" {
		name = fmt.Sprintf("%s-sngrp", rCtx.StackName)
	}
	desc, _ := props["CacheSubnetGroupDescription"].(string)
	params := map[string]string{
		"Action":                      "CreateCacheSubnetGroup",
		"Version":                     "2015-02-02",
		"CacheSubnetGroupName":        name,
		"CacheSubnetGroupDescription": desc,
	}
	if subnets, ok := props["SubnetIds"].([]any); ok {
		for i, s := range subnets {
			if id, _ := s.(string); id != "" {
				params[fmt.Sprintf("SubnetIds.SubnetIdentifier.%d", i+1)] = id
			}
		}
	}

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateCacheSubnetGroup: %w", err)
	}
	body := rec.Body.String()
	arn := extractXMLValue(body, "ARN")
	if arn == "" {
		arn = fmt.Sprintf("arn:aws:elasticache:%s:%s:subnetgroup:%s", rCtx.Region, rCtx.AccountID, name)
	}
	return name, map[string]string{"Arn": arn}, nil
}

func (h *elastiCacheSubnetGroupHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":               "DeleteCacheSubnetGroup",
		"Version":              "2015-02-02",
		"CacheSubnetGroupName": physicalID,
	}
	_, _ = internalQuery(ctx, router, rCtx.Region, params)
	return nil
}

func (h *elastiCacheSubnetGroupHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── fmtPropString ──────────────────────────────────────────────────────────────

// for Query-protocol form params (e.g. AllocatedStorage might be float64
// from JSON unmarshalling).
func fmtPropString(props map[string]any, key string) string {
	v, ok := props[key]
	if !ok {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return cfnScalarString(v)
	}
}
