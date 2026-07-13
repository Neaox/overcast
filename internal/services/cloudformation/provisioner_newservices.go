package cloudformation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/Neaox/overcast/internal/config"
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
		params["MultiAZ"] = fmt.Sprintf("%v", v)
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
		params["DeletionProtection"] = fmt.Sprintf("%v", v)
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

func (h *appsyncGraphQLApiHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["AuthenticationType"].(string); v != "" {
		body["authenticationType"] = v
	}
	if v, ok := props["XrayEnabled"]; ok {
		body["xrayEnabled"] = v
	}
	if v, ok := props["AdditionalAuthenticationProviders"]; ok {
		body["additionalAuthenticationProviders"] = v
	}
	if v, ok := props["LogConfig"]; ok {
		body["logConfig"] = v
	}
	if v, ok := props["UserPoolConfig"]; ok {
		body["userPoolConfig"] = v
	}
	if v, ok := props["OpenIDConnectConfig"]; ok {
		body["openIDConnectConfig"] = v
	}
	if v, ok := props["LambdaAuthorizerConfig"]; ok {
		body["lambdaAuthorizerConfig"] = v
	}

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/v1/apis", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateGraphqlApi: %w", err)
	}

	var resp struct {
		GraphqlApi struct {
			ApiID string            `json:"apiId"`
			Arn   string            `json:"arn"`
			Name  string            `json:"name"`
			Uris  map[string]string `json:"uris"`
		} `json:"graphqlApi"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateGraphqlApi: parse response: %w", err)
	}

	apiID := resp.GraphqlApi.ApiID
	attrs := map[string]string{
		"ApiId":      apiID,
		"Arn":        resp.GraphqlApi.Arn,
		"GraphQLUrl": resp.GraphqlApi.Uris["GRAPHQL"],
	}
	return apiID, attrs, nil
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

	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["AuthenticationType"].(string); v != "" {
		body["authenticationType"] = v
	}
	if v, ok := props["XrayEnabled"]; ok {
		body["xrayEnabled"] = v
	}
	if v, ok := props["AdditionalAuthenticationProviders"]; ok {
		body["additionalAuthenticationProviders"] = v
	}
	if v, ok := props["LogConfig"]; ok {
		body["logConfig"] = v
	}
	if v, ok := props["UserPoolConfig"]; ok {
		body["userPoolConfig"] = v
	}
	if v, ok := props["OpenIDConnectConfig"]; ok {
		body["openIDConnectConfig"] = v
	}
	if v, ok := props["LambdaAuthorizerConfig"]; ok {
		body["lambdaAuthorizerConfig"] = v
	}

	data, _ := json.Marshal(body)
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/v1/apis/"+physicalID, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("UpdateGraphqlApi: %w", err)
	}

	return physicalID, nil, nil
}

// ── AWS::AppSync::DataSource ──────────────────────────────────────────────

type appsyncDataSourceHandler struct{}

func (h *appsyncDataSourceHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)

	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["Type"].(string); v != "" {
		body["type"] = v
	}
	if v, _ := props["ServiceRoleArn"].(string); v != "" {
		body["serviceRoleArn"] = v
	}
	if v, ok := props["LambdaConfig"]; ok {
		body["lambdaConfig"] = v
	}
	if v, ok := props["DynamoDBConfig"]; ok {
		body["dynamoDBConfig"] = v
	}
	if v, ok := props["HttpConfig"]; ok {
		body["httpConfig"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/v1/apis/%s/datasources", apiID)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateDataSource: %w", err)
	}

	var resp struct {
		DataSource struct {
			DataSourceArn string `json:"dataSourceArn"`
			Name          string `json:"name"`
		} `json:"dataSource"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateDataSource: parse response: %w", err)
	}

	arn := resp.DataSource.DataSourceArn
	name, _ := props["Name"].(string)
	physicalID := apiID + "/" + name

	attrs := map[string]string{
		"DataSourceArn": arn,
		"Name":          resp.DataSource.Name,
		"Ref":           arn,
	}
	return physicalID, attrs, nil
}

func (h *appsyncDataSourceHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 2)
	if len(parts) != 2 {
		return nil
	}
	path := fmt.Sprintf("/v1/apis/%s/datasources/%s", parts[0], parts[1])
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncDataSourceHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
}

// ── AWS::AppSync::Resolver ────────────────────────────────────────────────

type appsyncResolverHandler struct{}

func (h *appsyncResolverHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	apiID, _ := props["ApiId"].(string)
	typeName, _ := props["TypeName"].(string)
	fieldName, _ := props["FieldName"].(string)

	body := map[string]any{
		"fieldName": fieldName,
	}
	if v, _ := props["DataSourceName"].(string); v != "" {
		body["dataSourceName"] = v
	}
	if v, _ := props["Kind"].(string); v != "" {
		body["kind"] = v
	}
	if v, _ := props["RequestMappingTemplate"].(string); v != "" {
		body["requestMappingTemplate"] = v
	}
	if v, _ := props["ResponseMappingTemplate"].(string); v != "" {
		body["responseMappingTemplate"] = v
	}
	if v, ok := props["PipelineConfig"]; ok {
		body["pipelineConfig"] = v
	}
	if v, _ := props["Code"].(string); v != "" {
		body["code"] = v
	}
	if v, ok := props["Runtime"]; ok {
		body["runtime"] = v
	}

	data, _ := json.Marshal(body)
	path := fmt.Sprintf("/v1/apis/%s/types/%s/resolvers", apiID, typeName)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, path, "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateResolver: %w", err)
	}

	var resp struct {
		Resolver struct {
			ResolverArn string `json:"resolverArn"`
			FieldName   string `json:"fieldName"`
			TypeName    string `json:"typeName"`
		} `json:"resolver"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateResolver: parse response: %w", err)
	}

	arn := resp.Resolver.ResolverArn
	physicalID := apiID + "/" + typeName + "/" + fieldName

	attrs := map[string]string{
		"ResolverArn": arn,
		"FieldName":   fieldName,
		"TypeName":    typeName,
		"Ref":         arn,
	}
	return physicalID, attrs, nil
}

func (h *appsyncResolverHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	parts := strings.SplitN(physicalID, "/", 3)
	if len(parts) != 3 {
		return nil
	}
	path := fmt.Sprintf("/v1/apis/%s/types/%s/resolvers/%s", parts[0], parts[1], parts[2])
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}

func (h *appsyncResolverHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	return "", nil, errReplacementRequired
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
		params["AutomaticFailoverEnabled"] = fmt.Sprintf("%v", v)
	}
	if v := props["MultiAZEnabled"]; v != nil {
		params["MultiAZEnabled"] = fmt.Sprintf("%v", v)
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
		params["AutomaticFailoverEnabled"] = fmt.Sprintf("%v", v)
	}
	if v := props["MultiAZEnabled"]; v != nil {
		params["MultiAZEnabled"] = fmt.Sprintf("%v", v)
	}
	if v, _ := props["NotificationTopicArn"].(string); v != "" {
		params["NotificationTopicArn"] = v
	}
	if v := props["SnapshotRetentionLimit"]; v != nil {
		params["SnapshotRetentionLimit"] = fmt.Sprintf("%v", v)
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
	case float64:
		if t == float64(int(t)) {
			return fmt.Sprintf("%d", int(t))
		}
		return fmt.Sprintf("%g", t)
	case int:
		return fmt.Sprintf("%d", t)
	default:
		return fmt.Sprintf("%v", v)
	}
}
