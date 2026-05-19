package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

type iamEnforceCacheEntry struct {
	statements   []iamPolicyStatement
	principalCtx map[string]string
}

var (
	iamEnforceInvalidatorMu sync.Mutex
	iamEnforceInvalidators  []func()
)

func InvalidateIAMEnforceCache() {
	iamEnforceInvalidatorMu.Lock()
	invalidators := make([]func(), len(iamEnforceInvalidators))
	copy(invalidators, iamEnforceInvalidators)
	iamEnforceInvalidatorMu.Unlock()
	for _, fn := range invalidators {
		fn()
	}
}

func registerIAMEnforceInvalidator(fn func()) {
	iamEnforceInvalidatorMu.Lock()
	iamEnforceInvalidators = append(iamEnforceInvalidators, fn)
	iamEnforceInvalidatorMu.Unlock()
}

// IAMEnforce enforces opt-in IAM authorization.
func IAMEnforce(enabled bool, st state.Store, logger *zap.Logger) func(http.Handler) http.Handler {
	var cacheMu sync.RWMutex
	var cache map[string]*iamEnforceCacheEntry
	invalidate := func() {
		cacheMu.Lock()
		cache = nil
		cacheMu.Unlock()
	}
	registerIAMEnforceInvalidator(invalidate)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled || shouldBypassIAM(r) {
				next.ServeHTTP(w, r)
				return
			}

			if !isSignedIAMRequest(r) {
				denyIAMRequest(w, r, logger, "unsigned request")
				return
			}

			if st == nil {
				next.ServeHTTP(w, r)
				return
			}

			parts, signed, sigErr := extractSigV4Parts(r)
			if sigErr != nil || !signed || strings.TrimSpace(parts.AccessKey) == "" {
				denyIAMRequest(w, r, logger, "missing or malformed SigV4 access key")
				return
			}

			action := requestIAMAction(r)
			if action == "" {
				// If action cannot be inferred (rare custom route), do not block.
				next.ServeHTTP(w, r)
				return
			}

			resource := requestIAMResource(r)
			decision := evaluateIAMDecision(r, st, parts.AccessKey, action, resource, &cacheMu, &cache)
			if decision != iamDecisionAllow {
				reason := "policy did not allow action"
				if decision == iamDecisionDeny {
					reason = "explicit deny policy matched"
				}
				denyIAMRequest(w, r, logger, reason)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

const (
	iamUsersNamespace    = "iam:users"
	iamPoliciesNamespace = "iam:policies"
	iamGroupsNamespace   = "iam:groups"
	iamRolesNamespace    = "iam:roles"
	iamSessionsNamespace = "iam:sessions"
	defaultIAMAccountID  = "000000000000"
)

type iamDecision int

const (
	iamDecisionNoMatch iamDecision = iota
	iamDecisionAllow
	iamDecisionDeny
)

type iamUserRecord struct {
	UserName   string `json:"UserName"`
	UserID     string `json:"UserId"`
	Arn        string `json:"Arn"`
	AccessKeys []struct {
		AccessKeyID     string `json:"AccessKeyId"`
		SecretAccessKey string `json:"SecretAccessKey"`
	} `json:"AccessKeys"`
	InlinePolicies   map[string]string `json:"InlinePolicies"`
	AttachedPolicies []struct {
		PolicyArn string `json:"PolicyArn"`
	} `json:"AttachedPolicies"`
}

type iamGroupRecord struct {
	Members          []string          `json:"Members"`
	InlinePolicies   map[string]string `json:"InlinePolicies"`
	AttachedPolicies []struct {
		PolicyArn string `json:"PolicyArn"`
	} `json:"AttachedPolicies"`
}

// iamRoleSessionRecord is the record stored in iam:sessions when a role is assumed via STS.
// Keyed by the temporary access key ID.
type iamRoleSessionRecord struct {
	RoleArn         string `json:"RoleArn"`
	RoleName        string `json:"RoleName"`
	SecretAccessKey string `json:"SecretAccessKey"`
}

type iamRoleRecord struct {
	RoleName         string            `json:"RoleName"`
	Arn              string            `json:"Arn"`
	InlinePolicies   map[string]string `json:"InlinePolicies"`
	AttachedPolicies []struct {
		PolicyArn string `json:"PolicyArn"`
	} `json:"AttachedPolicies"`
}

type iamManagedPolicyRecord struct {
	Document string `json:"Document"`
}

type iamPolicyDocument struct {
	Statement []iamPolicyStatement
}

type iamPolicyStatement struct {
	Effect      string
	Action      []string
	NotAction   []string
	Resource    []string
	NotResource []string
	Condition   map[string]map[string][]string // operator → key → values
}

func denyIAMRequest(w http.ResponseWriter, r *http.Request, logger *zap.Logger, reason string) {
	if logger != nil {
		logger.Debug("iam enforcement denied request",
			zap.String("method", r.Method),
			zap.String("path", r.URL.Path),
			zap.String("service", detectService(r)),
			zap.String("reason", reason),
		)
	}
	writeIAMAccessDenied(w, r)
}

func requestIAMAction(r *http.Request) string {
	svc := detectService(r)
	if svc == "" || svc == "internal" || svc == "metrics" || svc == "events" {
		return ""
	}
	if svc == "lambda" {
		if op := requestLambdaIAMOperation(r); op != "" {
			return svc + ":" + op
		}
	}

	if action := strings.TrimSpace(r.URL.Query().Get("Action")); action != "" {
		return svc + ":" + action
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
		_ = r.ParseForm()
		if action := strings.TrimSpace(r.Form.Get("Action")); action != "" {
			return svc + ":" + action
		}
	}

	if target := strings.TrimSpace(r.Header.Get("X-Amz-Target")); target != "" {
		op := target
		if idx := strings.LastIndex(op, "."); idx >= 0 && idx+1 < len(op) {
			op = op[idx+1:]
		}
		if op != "" {
			return svc + ":" + op
		}
	}

	if op := strings.TrimSpace(detectOperation(r)); op != "" {
		return svc + ":" + op
	}

	return ""
}

func requestIAMResource(r *http.Request) string {
	svc := detectService(r)
	fields := newIAMRequestFieldResolver()
	switch svc {
	case "s3":
		return requestS3IAMResource(r)
	case "sqs":
		return requestSQSIAMResource(r, fields)
	case "sns":
		return requestSNSIAMResource(r, fields)
	case "dynamodb":
		return requestDynamoDBIAMResource(r, fields)
	case "cloudformation":
		return requestCloudFormationIAMResource(r, fields)
	case "ecs":
		return requestECSIAMResource(r, fields)
	case "logs":
		return requestLogsIAMResource(r, fields)
	case "ecr":
		return requestECRIAMResource(r, fields)
	case "secretsmanager":
		return requestSecretsManagerIAMResource(r, fields)
	case "stepfunctions":
		return requestStepFunctionsIAMResource(r, fields)
	case "kinesis":
		return requestKinesisIAMResource(r, fields)
	case "firehose":
		return requestFirehoseIAMResource(r, fields)
	case "kms":
		return requestKMSIAMResource(r, fields)
	case "ssm":
		return requestSSMIAMResource(r, fields)
	case "lambda":
		return requestLambdaIAMResource(r, fields)
	case "cloudwatch":
		return requestCloudWatchIAMResource(r, fields)
	case "pipes":
		return requestPipesIAMResource(r)
	case "ec2":
		return requestEC2IAMResource(r, fields)
	case "events":
		return requestEventBridgeIAMResource(r, fields)
	case "rds":
		return requestRDSIAMResource(r, fields)
	case "cognito":
		return requestCognitoIAMResource(r, fields)
	case "apigateway":
		return requestAPIGatewayIAMResource(r)
	case "route53":
		return requestRoute53IAMResource(r, fields)
	case "elbv2":
		return requestELBv2IAMResource(r, fields)
	default:
		return "*"
	}
}

func requestLambdaIAMOperation(r *http.Request) string {
	trimmedPath := strings.Trim(strings.TrimSpace(r.URL.Path), "/")
	if trimmedPath == "" {
		return ""
	}
	parts := strings.Split(trimmedPath, "/")
	if len(parts) < 2 {
		return ""
	}
	if parts[1] == "layers" {
		if len(parts) == 4 && parts[3] == "versions" {
			switch r.Method {
			case http.MethodPost:
				return "PublishLayerVersion"
			case http.MethodGet:
				return "ListLayerVersions"
			}
		}
		if len(parts) == 5 && parts[3] == "versions" {
			switch r.Method {
			case http.MethodGet:
				return "GetLayerVersion"
			case http.MethodDelete:
				return "DeleteLayerVersion"
			}
		}
		return ""
	}
	if parts[1] != "functions" {
		return ""
	}

	if len(parts) == 2 {
		switch r.Method {
		case http.MethodGet:
			return "ListFunctions"
		case http.MethodPost:
			return "CreateFunction"
		}
		return ""
	}

	if len(parts) == 3 {
		switch r.Method {
		case http.MethodGet:
			return "GetFunction"
		case http.MethodDelete:
			return "DeleteFunction"
		}
		return ""
	}

	switch parts[3] {
	case "invocations":
		if r.Method == http.MethodPost {
			return "InvokeFunction"
		}
	case "response-streaming-invocations", "invoke-with-progress":
		if r.Method == http.MethodPost {
			return "InvokeFunction"
		}
	case "versions":
		switch r.Method {
		case http.MethodPost:
			return "PublishVersion"
		case http.MethodGet:
			return "ListVersionsByFunction"
		}
	case "aliases":
		if len(parts) == 4 {
			switch r.Method {
			case http.MethodPost:
				return "CreateAlias"
			case http.MethodGet:
				return "ListAliases"
			}
		}
		if len(parts) >= 5 {
			switch r.Method {
			case http.MethodGet:
				return "GetAlias"
			case http.MethodPut:
				return "UpdateAlias"
			case http.MethodDelete:
				return "DeleteAlias"
			}
		}
	case "code":
		if r.Method == http.MethodPut {
			return "UpdateFunctionCode"
		}
	case "code-signing-config":
		if r.Method == http.MethodGet {
			return "GetFunctionCodeSigningConfig"
		}
	case "source":
		switch r.Method {
		case http.MethodGet:
			return "GetFunctionSource"
		case http.MethodPut:
			return "PutFunctionSource"
		}
	case "provisioned-concurrency":
		switch r.Method {
		case http.MethodGet:
			return "GetProvisionedConcurrencyConfig"
		case http.MethodPut:
			return "PutProvisionedConcurrencyConfig"
		}
	case "test-events":
		if len(parts) == 4 && r.Method == http.MethodGet {
			return "ListTestEvents"
		}
		if len(parts) >= 5 {
			switch r.Method {
			case http.MethodPut:
				return "PutTestEvent"
			case http.MethodDelete:
				return "DeleteTestEvent"
			}
		}
	case "configuration":
		switch r.Method {
		case http.MethodGet:
			return "GetFunctionConfiguration"
		case http.MethodPut:
			return "UpdateFunctionConfiguration"
		}
	case "concurrency":
		switch r.Method {
		case http.MethodGet:
			return "GetFunctionConcurrency"
		case http.MethodPut:
			return "PutFunctionConcurrency"
		case http.MethodDelete:
			return "DeleteFunctionConcurrency"
		}
	}

	return ""
}

func requestS3IAMResource(r *http.Request) string {
	trimmed := strings.Trim(strings.TrimSpace(r.URL.Path), "/")
	if trimmed == "" {
		return "*"
	}

	parts := strings.SplitN(trimmed, "/", 2)
	bucket := strings.TrimSpace(parts[0])
	if bucket == "" {
		return "*"
	}
	if len(parts) == 1 {
		return fmt.Sprintf("arn:aws:s3:::%s", bucket)
	}
	objectKey := strings.TrimSpace(parts[1])
	if objectKey == "" {
		return fmt.Sprintf("arn:aws:s3:::%s", bucket)
	}
	return fmt.Sprintf("arn:aws:s3:::%s/%s", bucket, objectKey)
}

func requestSQSIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	action := requestIAMAction(r)
	if strings.EqualFold(action, "sqs:CreateQueue") {
		queueName := fields.field(r, "QueueName")
		if queueName == "" {
			return "*"
		}
		return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", region, defaultIAMAccountID, queueName)
	}

	queueURL := fields.field(r, "QueueUrl")
	if queueURL == "" {
		return "*"
	}
	accountID, queueName := parseSQSQueueURL(queueURL)
	if queueName == "" {
		return "*"
	}
	if accountID == "" {
		accountID = defaultIAMAccountID
	}

	return fmt.Sprintf("arn:aws:sqs:%s:%s:%s", region, accountID, queueName)
}

func requestSNSIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	action := requestIAMAction(r)
	if strings.EqualFold(action, "sns:CreateTopic") {
		topicName := fields.field(r, "Name")
		if topicName == "" {
			return "*"
		}
		return fmt.Sprintf("arn:aws:sns:%s:%s:%s", region, defaultIAMAccountID, topicName)
	}

	if topicArn := fields.field(r, "TopicArn"); topicArn != "" {
		return topicArn
	}
	if targetArn := fields.field(r, "TargetArn"); targetArn != "" {
		return targetArn
	}

	return "*"
}

func requestDynamoDBIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	tableName := fields.field(r, "TableName")
	if tableName == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:dynamodb:%s:%s:table/%s", region, defaultIAMAccountID, tableName)
}

func requestCloudFormationIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	stackID := fields.field(r, "StackId")
	if stackID != "" {
		if strings.HasPrefix(stackID, "arn:aws:cloudformation:") {
			return stackID
		}
		stackID = strings.TrimPrefix(stackID, "stack/")
		if i := strings.Index(stackID, "/"); i >= 0 {
			stackID = stackID[:i]
		}
		if stackID != "" {
			return fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/*", region, defaultIAMAccountID, stackID)
		}
	}

	stackName := fields.field(r, "StackName")
	if stackName == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:cloudformation:%s:%s:stack/%s/*", region, defaultIAMAccountID, stackName)
}

func requestECSIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	cluster := fields.field(r, "cluster")
	if cluster == "" {
		cluster = fields.firstJSONArrayStringField(r, "clusters")
	}
	if cluster == "" {
		cluster = fields.field(r, "clusterName")
	}
	if cluster == "" {
		return "*"
	}

	if strings.HasPrefix(cluster, "arn:aws:ecs:") {
		return cluster
	}

	cluster = strings.TrimPrefix(cluster, "cluster/")
	if i := strings.LastIndex(cluster, "/"); i >= 0 && i+1 < len(cluster) {
		cluster = cluster[i+1:]
	}
	if cluster == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/%s", region, defaultIAMAccountID, cluster)
}

func requestLogsIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	logGroupName := fields.field(r, "logGroupName")
	if logGroupName == "" {
		return "*"
	}

	logStreamName := fields.field(r, "logStreamName")
	if logStreamName == "" {
		return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:*", region, defaultIAMAccountID, logGroupName)
	}

	return fmt.Sprintf("arn:aws:logs:%s:%s:log-group:%s:log-stream:%s", region, defaultIAMAccountID, logGroupName, logStreamName)
}

func requestECRIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if resourceArn := fields.field(r, "resourceArn"); resourceArn != "" {
		return resourceArn
	}

	repositoryName := fields.field(r, "repositoryName")
	if repositoryName == "" {
		repositoryName = fields.firstJSONArrayStringField(r, "repositoryNames")
	}
	if repositoryName == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", region, defaultIAMAccountID, repositoryName)
}

func requestSecretsManagerIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	secretID := fields.field(r, "SecretId")
	if secretID == "" {
		secretID = fields.field(r, "Name")
	}
	if secretID == "" {
		return "*"
	}

	if strings.HasPrefix(secretID, "arn:aws:secretsmanager:") {
		return secretID
	}

	return fmt.Sprintf("arn:aws:secretsmanager:%s:%s:secret:%s", region, defaultIAMAccountID, secretID)
}

func requestStepFunctionsIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	stateMachineArn := fields.field(r, "stateMachineArn")
	if stateMachineArn != "" {
		if strings.HasPrefix(stateMachineArn, "arn:aws:states:") {
			return stateMachineArn
		}
		return fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:%s", region, defaultIAMAccountID, stateMachineArn)
	}

	name := fields.field(r, "name")
	if name == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:%s", region, defaultIAMAccountID, name)
}

func requestKinesisIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if streamARN := fields.field(r, "StreamARN"); streamARN != "" {
		return streamARN
	}

	streamName := fields.field(r, "StreamName")
	if streamName == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:kinesis:%s:%s:stream/%s", region, defaultIAMAccountID, streamName)
}

func requestFirehoseIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if streamARN := fields.field(r, "DeliveryStreamARN"); streamARN != "" {
		return streamARN
	}

	streamName := fields.field(r, "DeliveryStreamName")
	if streamName == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/%s", region, defaultIAMAccountID, streamName)
}

func requestSSMIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	name := fields.field(r, "Name")
	if name == "" {
		return "*"
	}
	name = strings.TrimPrefix(name, "/")
	if name == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:ssm:%s:%s:parameter/%s", region, defaultIAMAccountID, name)
}

func requestKMSIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	keyID := fields.field(r, "KeyId")
	if keyID == "" {
		return "*"
	}

	if strings.HasPrefix(keyID, "arn:aws:kms:") {
		return keyID
	}

	if strings.HasPrefix(keyID, "alias/") {
		return fmt.Sprintf("arn:aws:kms:%s:%s:%s", region, defaultIAMAccountID, keyID)
	}

	keyID = strings.TrimPrefix(keyID, "key/")
	if keyID == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", region, defaultIAMAccountID, keyID)
}

func requestLambdaIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)
	trimmedPath := strings.Trim(strings.TrimSpace(r.URL.Path), "/")
	parts := strings.Split(trimmedPath, "/")
	if len(parts) >= 3 && parts[1] == "layers" {
		layerName, err := url.PathUnescape(parts[2])
		if err != nil {
			layerName = parts[2]
		}
		if layerName == "" {
			return "*"
		}
		if len(parts) >= 5 && parts[3] == "versions" {
			version := strings.TrimSpace(parts[4])
			if version != "" {
				return fmt.Sprintf("arn:aws:lambda:%s:%s:layer:%s:%s", region, defaultIAMAccountID, layerName, version)
			}
		}
		return fmt.Sprintf("arn:aws:lambda:%s:%s:layer:%s", region, defaultIAMAccountID, layerName)
	}

	functionName := fields.field(r, "FunctionName")
	if functionName == "" {
		if len(parts) >= 3 && parts[1] == "functions" {
			if decoded, err := url.PathUnescape(parts[2]); err == nil {
				functionName = decoded
			} else {
				functionName = parts[2]
			}
		}
	}
	if functionName == "" {
		return "*"
	}

	if strings.HasPrefix(functionName, "arn:aws:lambda:") {
		return functionName
	}

	functionName = strings.TrimPrefix(functionName, "function:")
	if functionName == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:lambda:%s:%s:function:%s", region, defaultIAMAccountID, functionName)
}

func requestCloudWatchIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if resourceARN := fields.field(r, "ResourceARN"); resourceARN != "" {
		return resourceARN
	}

	alarmName := fields.field(r, "AlarmName")
	if alarmName == "" {
		alarmName = fields.field(r, "AlarmNames.member.1")
	}
	if alarmName == "" {
		return "*"
	}

	return fmt.Sprintf("arn:aws:cloudwatch:%s:%s:alarm:%s", region, defaultIAMAccountID, alarmName)
}

func requestPipesIAMResource(r *http.Request) string {
	region := iamRegionOrDefault(r)
	trimmedPath := strings.Trim(strings.TrimSpace(r.URL.Path), "/")
	parts := strings.Split(trimmedPath, "/")
	if len(parts) < 3 || parts[0] != "v1" || parts[1] != "pipes" {
		return "*"
	}
	name, err := url.PathUnescape(parts[2])
	if err != nil {
		name = parts[2]
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "*"
	}
	return fmt.Sprintf("arn:aws:pipes:%s:%s:pipe/%s", region, defaultIAMAccountID, name)
}

func requestEC2IAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if vpcID := fields.field(r, "VpcId"); vpcID != "" {
		return fmt.Sprintf("arn:aws:ec2:%s:%s:vpc/%s", region, defaultIAMAccountID, vpcID)
	}
	if instanceID := fields.field(r, "InstanceId"); instanceID != "" {
		return fmt.Sprintf("arn:aws:ec2:%s:%s:instance/%s", region, defaultIAMAccountID, instanceID)
	}
	if instanceIDs := fields.firstJSONArrayStringField(r, "InstanceIds"); instanceIDs != "" {
		return fmt.Sprintf("arn:aws:ec2:%s:%s:instance/%s", region, defaultIAMAccountID, instanceIDs)
	}
	if groupID := fields.field(r, "GroupId"); groupID != "" {
		return fmt.Sprintf("arn:aws:ec2:%s:%s:security-group/%s", region, defaultIAMAccountID, groupID)
	}
	if niID := fields.field(r, "NetworkInterfaceId"); niID != "" {
		return fmt.Sprintf("arn:aws:ec2:%s:%s:network-interface/%s", region, defaultIAMAccountID, niID)
	}
	if keyName := fields.field(r, "KeyName"); keyName != "" {
		return fmt.Sprintf("arn:aws:ec2:%s:%s:key-pair/%s", region, defaultIAMAccountID, keyName)
	}
	if allocationID := fields.field(r, "AllocationId"); allocationID != "" {
		return fmt.Sprintf("arn:aws:ec2:%s:%s:elastic-ip/%s", region, defaultIAMAccountID, allocationID)
	}

	return "*"
}

func requestEventBridgeIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if eventBusName := fields.field(r, "EventBusName"); eventBusName != "" {
		return fmt.Sprintf("arn:aws:events:%s:%s:event-bus/%s", region, defaultIAMAccountID, eventBusName)
	}
	if name := fields.field(r, "Name"); name != "" {
		return fmt.Sprintf("arn:aws:events:%s:%s:rule/%s/%s", region, defaultIAMAccountID, "default", name)
	}
	if rule := fields.field(r, "Rule"); rule != "" {
		if eventBusName := fields.field(r, "EventBusName"); eventBusName != "" {
			return fmt.Sprintf("arn:aws:events:%s:%s:rule/%s/%s", region, defaultIAMAccountID, eventBusName, rule)
		}
		return fmt.Sprintf("arn:aws:events:%s:%s:rule/%s/%s", region, defaultIAMAccountID, "default", rule)
	}

	return "*"
}

func requestRDSIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if dbInstanceID := fields.field(r, "DBInstanceIdentifier"); dbInstanceID != "" {
		return fmt.Sprintf("arn:aws:rds:%s:%s:db:%s", region, defaultIAMAccountID, dbInstanceID)
	}
	if dbClusterID := fields.field(r, "DBClusterIdentifier"); dbClusterID != "" {
		return fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s", region, defaultIAMAccountID, dbClusterID)
	}

	return "*"
}

func requestCognitoIAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if userPoolID := fields.field(r, "UserPoolId"); userPoolID != "" {
		return fmt.Sprintf("arn:aws:cognito-idp:%s:%s:userpool/%s", region, defaultIAMAccountID, userPoolID)
	}
	if identityPoolID := fields.field(r, "IdentityPoolId"); identityPoolID != "" {
		return fmt.Sprintf("arn:aws:cognito-identity:%s:%s:identitypool/%s", region, defaultIAMAccountID, identityPoolID)
	}

	return "*"
}

func requestAPIGatewayIAMResource(r *http.Request) string {
	region := iamRegionOrDefault(r)
	trimmedPath := strings.Trim(strings.TrimSpace(r.URL.Path), "/")
	parts := strings.Split(trimmedPath, "/")

	if len(parts) >= 2 && parts[0] == "restapis" {
		restAPIID := parts[1]
		if restAPIID != "" {
			return fmt.Sprintf("arn:aws:apigateway:%s:%s:/restapis/%s", region, defaultIAMAccountID, restAPIID)
		}
	}

	if len(parts) >= 3 && parts[0] == "v2" && parts[1] == "apis" {
		apiID := parts[2]
		if apiID != "" {
			return fmt.Sprintf("arn:aws:apigateway:%s:%s:/apis/%s", region, defaultIAMAccountID, apiID)
		}
	}

	return "*"
}

func requestRoute53IAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	if hostedZoneID := fields.field(r, "HostedZoneId"); hostedZoneID != "" {
		id := strings.TrimPrefix(hostedZoneID, "/hostedzone/")
		return fmt.Sprintf("arn:aws:route53:::hostedzone/%s", id)
	}
	if id := fields.field(r, "Id"); id != "" {
		id = strings.TrimPrefix(id, "/hostedzone/")
		if id != "" {
			return fmt.Sprintf("arn:aws:route53:::hostedzone/%s", id)
		}
	}
	return "*"
}

func requestELBv2IAMResource(r *http.Request, fields *iamRequestFieldResolver) string {
	region := iamRegionOrDefault(r)

	if lbARN := fields.field(r, "LoadBalancerArn"); lbARN != "" {
		if strings.HasPrefix(lbARN, "arn:aws:elasticloadbalancing:") {
			return lbARN
		}
		return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:loadbalancer/%s", region, defaultIAMAccountID, lbARN)
	}
	if tgARN := fields.field(r, "TargetGroupArn"); tgARN != "" {
		if strings.HasPrefix(tgARN, "arn:aws:elasticloadbalancing:") {
			return tgARN
		}
		return fmt.Sprintf("arn:aws:elasticloadbalancing:%s:%s:targetgroup/%s", region, defaultIAMAccountID, tgARN)
	}
	if resourceARN := fields.field(r, "ResourceArns.member.1"); resourceARN != "" {
		return resourceARN
	}

	return "*"
}

func requestedRegionFromSigV4(r *http.Request) string {
	if parts := credentialScope(r); len(parts) >= 3 {
		if region := strings.TrimSpace(parts[2]); region != "" {
			return strings.ToLower(region)
		}
	}
	return ""
}

func iamRegionOrDefault(r *http.Request) string {
	region := requestedRegionFromSigV4(r)
	if region == "" {
		return "us-east-1"
	}
	return region
}

type iamRequestFieldResolver struct {
	formParsed bool
	jsonLoaded bool
	jsonBody   map[string]any
}

func newIAMRequestFieldResolver() *iamRequestFieldResolver {
	return &iamRequestFieldResolver{}
}

func (f *iamRequestFieldResolver) field(r *http.Request, key string) string {
	if v := strings.TrimSpace(r.URL.Query().Get(key)); v != "" {
		return v
	}

	if strings.Contains(strings.ToLower(r.Header.Get("Content-Type")), "application/x-www-form-urlencoded") {
		if !f.formParsed {
			_ = r.ParseForm()
			f.formParsed = true
		}
		if v := strings.TrimSpace(r.Form.Get(key)); v != "" {
			return v
		}
	}

	payload := f.loadJSONBody(r)
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func (f *iamRequestFieldResolver) firstJSONArrayStringField(r *http.Request, key string) string {
	payload := f.loadJSONBody(r)
	if payload == nil {
		return ""
	}
	v, ok := payload[key]
	if !ok {
		return ""
	}
	a, ok := v.([]any)
	if !ok || len(a) == 0 {
		return ""
	}
	s, ok := a[0].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func (f *iamRequestFieldResolver) loadJSONBody(r *http.Request) map[string]any {
	if f.jsonLoaded {
		return f.jsonBody
	}
	f.jsonLoaded = true

	if r.Body == nil {
		return nil
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(body))

	if len(bytes.TrimSpace(body)) == 0 {
		return nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	f.jsonBody = payload
	return f.jsonBody
}

func parseSQSQueueURL(queueURL string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(queueURL))
	if err != nil {
		return "", ""
	}
	segments := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(segments) >= 2 {
		return strings.TrimSpace(segments[len(segments)-2]), strings.TrimSpace(segments[len(segments)-1])
	}
	if len(segments) == 1 {
		return "", strings.TrimSpace(segments[0])
	}
	return "", ""
}

func evaluateIAMDecision(r *http.Request, st state.Store, accessKeyID, action, resource string, cacheMu *sync.RWMutex, cache *map[string]*iamEnforceCacheEntry) iamDecision {
	reqCtx := buildIAMRequestContext(r)

	cached := loadIAMEnforceCacheEntry(accessKeyID, cacheMu, cache)
	if cached == nil {
		docs, principalCtx := collectPrincipalPolicyDocumentsAndContext(r.Context(), st, accessKeyID)
		if len(docs) == 0 {
			return iamDecisionNoMatch
		}
		statements := compilePolicyStatements(docs)
		cached = &iamEnforceCacheEntry{
			statements:   statements,
			principalCtx: principalCtx,
		}
		storeIAMEnforceCacheEntry(accessKeyID, cached, cacheMu, cache)
	}

	for k, v := range cached.principalCtx {
		reqCtx[k] = v
	}
	decision := iamDecisionNoMatch
	for _, stmt := range cached.statements {
		if !statementMatchesAction(action, stmt) || !statementMatchesResource(resource, stmt, reqCtx) {
			continue
		}
		condMet, unknownOp := evaluateConditions(stmt.Condition, reqCtx)
		if unknownOp {
			return iamDecisionDeny
		}
		if !condMet {
			continue
		}
		if strings.EqualFold(stmt.Effect, "Deny") {
			return iamDecisionDeny
		}
		if strings.EqualFold(stmt.Effect, "Allow") {
			decision = iamDecisionAllow
		}
	}

	return decision
}

func loadIAMEnforceCacheEntry(accessKeyID string, mu *sync.RWMutex, cache *map[string]*iamEnforceCacheEntry) *iamEnforceCacheEntry {
	mu.RLock()
	defer mu.RUnlock()
	if *cache == nil {
		return nil
	}
	return (*cache)[accessKeyID]
}

func storeIAMEnforceCacheEntry(accessKeyID string, entry *iamEnforceCacheEntry, mu *sync.RWMutex, cache *map[string]*iamEnforceCacheEntry) {
	mu.Lock()
	defer mu.Unlock()
	if *cache == nil {
		*cache = make(map[string]*iamEnforceCacheEntry)
	}
	(*cache)[accessKeyID] = entry
}

func compilePolicyStatements(docs []string) []iamPolicyStatement {
	out := make([]iamPolicyStatement, 0)
	for _, raw := range docs {
		doc, ok := parseIAMPolicyDocument(raw)
		if !ok {
			continue
		}
		out = append(out, doc.Statement...)
	}
	return out
}

func statementMatchesAction(action string, stmt iamPolicyStatement) bool {
	hasAction := len(stmt.Action) > 0
	hasNotAction := len(stmt.NotAction) > 0
	if hasAction && hasNotAction {
		return false
	}
	if hasAction {
		return matchesAnyPattern(action, stmt.Action)
	}
	if hasNotAction {
		return !matchesAnyPattern(action, stmt.NotAction)
	}
	return false
}

func statementMatchesResource(resource string, stmt iamPolicyStatement, reqCtx map[string]string) bool {
	hasResource := len(stmt.Resource) > 0
	hasNotResource := len(stmt.NotResource) > 0
	if hasResource && hasNotResource {
		return false
	}
	if hasResource {
		expanded := expandPolicyVariablesList(stmt.Resource, reqCtx)
		return matchesAnyPattern(resource, expanded)
	}
	if hasNotResource {
		expanded := expandPolicyVariablesList(stmt.NotResource, reqCtx)
		return !matchesAnyPattern(resource, expanded)
	}
	return false
}

// expandPolicyVariablesList returns a new slice with IAM policy variables
// (e.g. ${aws:username}) substituted from reqCtx in every element.
func expandPolicyVariablesList(patterns []string, reqCtx map[string]string) []string {
	out := make([]string, len(patterns))
	for i, p := range patterns {
		out[i] = expandPolicyVariables(p, reqCtx)
	}
	return out
}

// expandPolicyVariables replaces ${varname} tokens in s with corresponding
// values from reqCtx (case-insensitive key lookup). Unknown variables are
// left unexpanded so they will not match any real ARN segment.
func expandPolicyVariables(s string, reqCtx map[string]string) string {
	if !strings.Contains(s, "${") {
		return s
	}
	var buf strings.Builder
	buf.Grow(len(s))
	for i := 0; i < len(s); {
		start := strings.Index(s[i:], "${")
		if start == -1 {
			buf.WriteString(s[i:])
			break
		}
		buf.WriteString(s[i : i+start])
		i += start + 2 // skip "${"
		end := strings.Index(s[i:], "}")
		if end == -1 {
			// Unclosed variable reference — emit literally.
			buf.WriteString("${")
			continue
		}
		rawName := s[i : i+end]
		varName := strings.ToLower(rawName)
		i += end + 1 // skip past "}"
		if val, ok := reqCtx[varName]; ok {
			buf.WriteString(val)
		} else {
			// Unknown variable — leave unexpanded so it won't match any ARN.
			buf.WriteString("${")
			buf.WriteString(rawName)
			buf.WriteByte('}')
		}
	}
	return buf.String()
}

func collectPrincipalPolicyDocumentsAndContext(ctx context.Context, st state.Store, accessKeyID string) ([]string, map[string]string) {
	users, err := st.Scan(ctx, iamUsersNamespace, "")
	if err != nil {
		return nil, nil
	}

	for _, kv := range users {
		var user iamUserRecord
		if err := json.Unmarshal([]byte(kv.Value), &user); err != nil {
			continue
		}
		if !userHasAccessKey(user, accessKeyID) {
			continue
		}
		if strings.TrimSpace(user.UserName) == "" {
			user.UserName = kv.Key
		}

		docs := make([]string, 0, len(user.InlinePolicies)+len(user.AttachedPolicies))
		docs = appendInlineAndAttachedPolicyDocs(ctx, st, docs, user.InlinePolicies, user.AttachedPolicies)
		docs = appendGroupPolicyDocuments(ctx, st, docs, user.UserName)

		principalArn := strings.TrimSpace(user.Arn)
		if principalArn == "" {
			principalArn = fmt.Sprintf("arn:aws:iam::%s:user/%s", defaultIAMAccountID, user.UserName)
		}
		principalAccount := accountFromARN(principalArn)
		if principalAccount == "" {
			principalAccount = defaultIAMAccountID
		}

		userID := strings.TrimSpace(user.UserID)
		if userID == "" {
			userID = accessKeyID
		}

		principalCtx := map[string]string{
			"aws:principalarn":     principalArn,
			"aws:principalaccount": principalAccount,
			"aws:userid":           userID,
			"aws:username":         user.UserName,
			"aws:principaltype":    "User",
		}

		return docs, principalCtx
	}

	return appendRoleSessionPolicyDocumentsWithContext(ctx, st, accessKeyID)
}

func appendGroupPolicyDocuments(ctx context.Context, st state.Store, docs []string, userName string) []string {
	if strings.TrimSpace(userName) == "" {
		return docs
	}

	groups, err := st.Scan(ctx, iamGroupsNamespace, "")
	if err != nil {
		return docs
	}

	for _, kv := range groups {
		var group iamGroupRecord
		if err := json.Unmarshal([]byte(kv.Value), &group); err != nil {
			continue
		}
		if !groupHasMember(group, userName) {
			continue
		}
		docs = appendInlineAndAttachedPolicyDocs(ctx, st, docs, group.InlinePolicies, group.AttachedPolicies)
	}

	return docs
}

func groupHasMember(group iamGroupRecord, userName string) bool {
	for _, member := range group.Members {
		if member == userName {
			return true
		}
	}
	return false
}

// appendRoleSessionPolicyDocuments resolves a temporary access key issued by
// STS AssumeRole to its originating role, then appends that role's inline and
// attached managed policy documents.  Returns nil when the key is not found in
// iam:sessions.
func appendRoleSessionPolicyDocumentsWithContext(ctx context.Context, st state.Store, accessKeyID string) ([]string, map[string]string) {
	sessionRaw, found, err := st.Get(ctx, iamSessionsNamespace, accessKeyID)
	if err != nil || !found {
		return nil, nil
	}

	var session iamRoleSessionRecord
	if err := json.Unmarshal([]byte(sessionRaw), &session); err != nil {
		return nil, nil
	}

	roleName := strings.TrimSpace(session.RoleName)
	if roleName == "" {
		// Try to derive the role name from the ARN as fallback.
		if idx := strings.LastIndex(session.RoleArn, "/"); idx >= 0 {
			roleName = session.RoleArn[idx+1:]
		}
	}
	if roleName == "" {
		return nil, nil
	}

	roleRaw, found, err := st.Get(ctx, iamRolesNamespace, roleName)
	if err != nil || !found {
		return nil, nil
	}

	var role iamRoleRecord
	if err := json.Unmarshal([]byte(roleRaw), &role); err != nil {
		return nil, nil
	}

	docs := make([]string, 0, len(role.InlinePolicies)+len(role.AttachedPolicies))
	docs = appendInlineAndAttachedPolicyDocs(ctx, st, docs, role.InlinePolicies, role.AttachedPolicies)

	principalArn := strings.TrimSpace(session.RoleArn)
	if principalArn == "" {
		principalArn = strings.TrimSpace(role.Arn)
	}
	if principalArn == "" {
		principalArn = fmt.Sprintf("arn:aws:iam::%s:role/%s", defaultIAMAccountID, roleName)
	}
	principalAccount := accountFromARN(principalArn)
	if principalAccount == "" {
		principalAccount = defaultIAMAccountID
	}

	principalCtx := map[string]string{
		"aws:principalarn":     principalArn,
		"aws:principalaccount": principalAccount,
		"aws:userid":           accessKeyID,
		"aws:username":         roleName,
		"aws:principaltype":    "AssumedRole",
	}

	return docs, principalCtx
}

func accountFromARN(arn string) string {
	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return ""
	}
	return strings.TrimSpace(parts[4])
}

func appendInlineAndAttachedPolicyDocs(
	ctx context.Context,
	st state.Store,
	docs []string,
	inline map[string]string,
	attached []struct {
		PolicyArn string `json:"PolicyArn"`
	},
) []string {
	for _, doc := range inline {
		docs = append(docs, doc)
	}
	for _, policy := range attached {
		if strings.TrimSpace(policy.PolicyArn) == "" {
			continue
		}
		managedRaw, found, getErr := st.Get(ctx, iamPoliciesNamespace, policy.PolicyArn)
		if getErr != nil || !found {
			continue
		}
		var managed iamManagedPolicyRecord
		if err := json.Unmarshal([]byte(managedRaw), &managed); err != nil {
			continue
		}
		if strings.TrimSpace(managed.Document) != "" {
			docs = append(docs, managed.Document)
		}
	}
	return docs
}

func userHasAccessKey(user iamUserRecord, accessKeyID string) bool {
	for _, key := range user.AccessKeys {
		if key.AccessKeyID == accessKeyID {
			return true
		}
	}
	return false
}

func parseIAMPolicyDocument(raw string) (iamPolicyDocument, bool) {
	type wireStatement struct {
		Effect      string                     `json:"Effect"`
		Action      json.RawMessage            `json:"Action"`
		NotAction   json.RawMessage            `json:"NotAction"`
		Resource    json.RawMessage            `json:"Resource"`
		NotResource json.RawMessage            `json:"NotResource"`
		Condition   map[string]json.RawMessage `json:"Condition"`
	}
	type wireDocument struct {
		Statement json.RawMessage `json:"Statement"`
	}

	var wd wireDocument
	if err := json.Unmarshal([]byte(raw), &wd); err != nil {
		return iamPolicyDocument{}, false
	}

	var statements []wireStatement
	if err := json.Unmarshal(wd.Statement, &statements); err != nil {
		var single wireStatement
		if err2 := json.Unmarshal(wd.Statement, &single); err2 != nil {
			return iamPolicyDocument{}, false
		}
		statements = []wireStatement{single}
	}

	out := iamPolicyDocument{Statement: make([]iamPolicyStatement, 0, len(statements))}
	for _, ws := range statements {
		actions, okA := parseStringOrStringArray(ws.Action)
		notActions, okNA := parseStringOrStringArray(ws.NotAction)
		resources, okR := parseStringOrStringArray(ws.Resource)
		notResources, okNR := parseStringOrStringArray(ws.NotResource)

		if okA && okNA {
			continue
		}
		if okR && okNR {
			continue
		}
		if (!okA && !okNA) || (!okR && !okNR) {
			continue
		}
		cond := parseConditionBlock(ws.Condition)
		out.Statement = append(out.Statement, iamPolicyStatement{
			Effect:      ws.Effect,
			Action:      actions,
			NotAction:   notActions,
			Resource:    resources,
			NotResource: notResources,
			Condition:   cond,
		})
	}

	return out, len(out.Statement) > 0
}

func parseStringOrStringArray(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, false
	}
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		if strings.TrimSpace(single) == "" {
			return nil, false
		}
		return []string{single}, true
	}
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, v := range arr {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out, len(out) > 0
}

func matchesAnyPattern(value string, patterns []string) bool {
	if len(patterns) == 0 {
		return false
	}
	for _, p := range patterns {
		if p == "*" {
			return true
		}
		if ok, err := path.Match(p, value); err == nil && ok {
			return true
		}
		if wildcardMatch(p, value) {
			return true
		}
	}
	return false
}

func wildcardMatch(pattern, value string) bool {
	if pattern == value {
		return true
	}
	if !strings.Contains(pattern, "*") {
		return false
	}

	parts := strings.Split(pattern, "*")
	idx := 0
	for i, part := range parts {
		if part == "" {
			continue
		}
		pos := strings.Index(value[idx:], part)
		if pos < 0 {
			return false
		}
		if i == 0 && idx == 0 && pos != 0 {
			return false
		}
		idx += pos + len(part)
	}

	last := parts[len(parts)-1]
	if last != "" && !strings.HasSuffix(value, last) {
		return false
	}

	return true
}

func shouldBypassIAM(r *http.Request) bool {
	if r.Method == http.MethodOptions {
		return true
	}

	path := r.URL.Path
	if strings.HasPrefix(path, "/_") {
		return true
	}
	if path == "/api" || strings.HasPrefix(path, "/api/") {
		return true
	}
	return false
}

func isSignedIAMRequest(r *http.Request) bool {
	if strings.TrimSpace(r.Header.Get("Authorization")) != "" {
		return true
	}
	q := r.URL.Query()
	if q.Get("X-Amz-Signature") != "" {
		return true
	}
	if q.Get("X-Amz-Algorithm") != "" {
		return true
	}
	return false
}

func writeIAMAccessDenied(w http.ResponseWriter, r *http.Request) {
	svc := detectService(r)
	switch svc {
	case "s3", "cloudfront":
		protocol.WriteXMLError(w, r, &protocol.AWSError{
			Code:       "AccessDenied",
			Message:    "Access Denied",
			HTTPStatus: http.StatusForbidden,
		})
	case "sns", "iam", "sts", "ec2", "cloudformation", "rds", "ses", "cloudwatch", "acm", "kinesis", "kms", "ssm", "stepfunctions", "ecs", "ecr", "glue", "firehose", "athena", "elasticache", "msk", "waf", "shield", "autoscaling", "route53", "elbv2", "organizations":
		protocol.WriteQueryXMLError(w, r, &protocol.AWSError{
			Code:       "AccessDenied",
			Message:    "User is not authorized to perform this action",
			HTTPStatus: http.StatusForbidden,
		})
	default:
		protocol.WriteJSONError(w, r, &protocol.AWSError{
			Code:       "AccessDeniedException",
			Message:    "User is not authorized to perform this action",
			HTTPStatus: http.StatusForbidden,
		})
	}
}

// ─── Condition evaluation ─────────────────────────────────────────────────────

// parseConditionBlock converts the wire-format Condition map (operator →
// {key: string|[]string}) into the canonical form used by evaluateConditions.
func parseConditionBlock(raw map[string]json.RawMessage) map[string]map[string][]string {
	if len(raw) == 0 {
		return nil
	}
	out := make(map[string]map[string][]string, len(raw))
	for op, keyRaw := range raw {
		var pairs map[string]json.RawMessage
		if err := json.Unmarshal(keyRaw, &pairs); err != nil {
			continue
		}
		keyMap := make(map[string][]string, len(pairs))
		for k, vRaw := range pairs {
			vals, ok := parseStringOrStringArray(vRaw)
			if !ok {
				continue
			}
			keyMap[k] = vals
		}
		if len(keyMap) > 0 {
			out[op] = keyMap
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildIAMRequestContext constructs the set of IAM condition context keys that
// are derivable from the HTTP request alone (no store access needed).
// Supported keys: aws:RequestedRegion, aws:SourceIp, aws:CurrentTime.
func buildIAMRequestContext(r *http.Request) map[string]string {
	ctx := make(map[string]string, 3)

	// aws:RequestedRegion — extracted from the SigV4 credential scope.
	if region := requestedRegionFromSigV4(r); region != "" {
		ctx["aws:requestedregion"] = region
	}

	// aws:SourceIp — client IP with port stripped.
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr
	}
	if ip = strings.TrimSpace(ip); ip != "" {
		ctx["aws:sourceip"] = ip
	}

	// aws:CurrentTime — derived from SigV4 timestamp when available.
	dateRaw := strings.TrimSpace(r.Header.Get("X-Amz-Date"))
	if dateRaw == "" {
		dateRaw = strings.TrimSpace(r.URL.Query().Get("X-Amz-Date"))
	}
	if dateRaw != "" {
		if ts, err := time.Parse("20060102T150405Z", dateRaw); err == nil {
			ctx["aws:currenttime"] = ts.UTC().Format(time.RFC3339)
		}
	}

	// aws:RequestedContentLength — Content-Length of the request body, when set.
	if cl := r.ContentLength; cl >= 0 {
		ctx["aws:requestedcontentlength"] = strconv.FormatInt(cl, 10)
	}

	return ctx
}

// evaluateConditions evaluates all condition blocks against reqCtx.
// All operator blocks must be satisfied (AND across operators).
// Within an operator, all key→values pairs must be satisfied (AND across keys;
// OR across the values for each key).
//
// Returns (conditionMet, unknownOperator).
// unknownOperator=true signals the caller to deny (fail-closed per plan doc).
func evaluateConditions(cond map[string]map[string][]string, reqCtx map[string]string) (bool, bool) {
	if len(cond) == 0 {
		return true, false
	}
	for operator, keyValues := range cond {
		for key, values := range keyValues {
			// Normalise key to lowercase for case-insensitive comparison.
			// Expand policy variables in each policy value before comparison.
			expandedValues := expandPolicyVariablesList(values, reqCtx)
			ctxVal, ok := reqCtx[strings.ToLower(key)]
			effectiveOperator := operator
			if strings.HasSuffix(operator, "IfExists") {
				effectiveOperator = strings.TrimSuffix(operator, "IfExists")
				if !ok {
					// IfExists: missing context keys satisfy the condition.
					continue
				}
			}

			if effectiveOperator == "Null" {
				matched, known := applyNullCondition(ok, expandedValues)
				if !known {
					return false, true
				}
				if !matched {
					return false, false
				}
				continue
			}

			if !ok {
				// Context key is not available: condition is unsatisfied.
				return false, false
			}
			matched, known := applyConditionOperator(effectiveOperator, ctxVal, expandedValues)
			if !known {
				return false, true
			}
			if !matched {
				return false, false
			}
		}
	}
	return true, false
}

// applyConditionOperator tests a single condition key value against the
// provided set of policy values using the given operator.
// Returns (matched, knownOperator).
func applyConditionOperator(operator, ctxVal string, policyVals []string) (bool, bool) {
	switch operator {
	case "StringEquals":
		for _, v := range policyVals {
			if ctxVal == v {
				return true, true
			}
		}
		return false, true

	case "StringNotEquals":
		for _, v := range policyVals {
			if ctxVal == v {
				return false, true
			}
		}
		return true, true

	case "StringEqualsIgnoreCase":
		for _, v := range policyVals {
			if strings.EqualFold(ctxVal, v) {
				return true, true
			}
		}
		return false, true

	case "StringNotEqualsIgnoreCase":
		for _, v := range policyVals {
			if strings.EqualFold(ctxVal, v) {
				return false, true
			}
		}
		return true, true

	case "StringLike":
		for _, v := range policyVals {
			if wildcardMatch(v, ctxVal) {
				return true, true
			}
		}
		return false, true

	case "StringNotLike":
		for _, v := range policyVals {
			if wildcardMatch(v, ctxVal) {
				return false, true
			}
		}
		return true, true

	case "ArnEquals", "ArnLike":
		for _, v := range policyVals {
			if wildcardMatch(v, ctxVal) {
				return true, true
			}
		}
		return false, true

	case "ArnNotEquals", "ArnNotLike":
		for _, v := range policyVals {
			if wildcardMatch(v, ctxVal) {
				return false, true
			}
		}
		return true, true

	case "Bool":
		for _, v := range policyVals {
			if strings.EqualFold(ctxVal, v) {
				return true, true
			}
		}
		return false, true

	case "IpAddress":
		for _, v := range policyVals {
			if ipMatchesCIDR(ctxVal, v) {
				return true, true
			}
		}
		return false, true

	case "NotIpAddress":
		for _, v := range policyVals {
			if ipMatchesCIDR(ctxVal, v) {
				return false, true
			}
		}
		return true, true

	case "NumericEquals", "NumericNotEquals", "NumericLessThan", "NumericLessThanEquals", "NumericGreaterThan", "NumericGreaterThanEquals":
		ctxNum, err := strconv.ParseFloat(ctxVal, 64)
		if err != nil {
			return false, true
		}
		switch operator {
		case "NumericNotEquals":
			for _, v := range policyVals {
				pv, err := strconv.ParseFloat(v, 64)
				if err != nil {
					continue
				}
				if ctxNum == pv {
					return false, true
				}
			}
			return true, true
		default:
			for _, v := range policyVals {
				pv, err := strconv.ParseFloat(v, 64)
				if err != nil {
					continue
				}
				if numericCompareMatches(operator, ctxNum, pv) {
					return true, true
				}
			}
			return false, true
		}

	case "DateEquals", "DateNotEquals", "DateLessThan", "DateLessThanEquals", "DateGreaterThan", "DateGreaterThanEquals":
		ctxTime, ok := parseIAMTime(ctxVal)
		if !ok {
			return false, true
		}
		switch operator {
		case "DateNotEquals":
			for _, v := range policyVals {
				policyTime, ok := parseIAMTime(v)
				if !ok {
					continue
				}
				if ctxTime.Equal(policyTime) {
					return false, true
				}
			}
			return true, true
		default:
			for _, v := range policyVals {
				policyTime, ok := parseIAMTime(v)
				if !ok {
					continue
				}
				if dateCompareMatches(operator, ctxTime, policyTime) {
					return true, true
				}
			}
			return false, true
		}

	default:
		return false, false // unknown operator — caller must deny
	}
}

func parseIAMTime(raw string) (time.Time, bool) {
	ts := strings.TrimSpace(raw)
	if ts == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

func dateCompareMatches(operator string, left, right time.Time) bool {
	switch operator {
	case "DateEquals", "DateNotEquals":
		return left.Equal(right)
	case "DateLessThan":
		return left.Before(right)
	case "DateLessThanEquals":
		return left.Before(right) || left.Equal(right)
	case "DateGreaterThan":
		return left.After(right)
	case "DateGreaterThanEquals":
		return left.After(right) || left.Equal(right)
	default:
		return false
	}
}

func numericCompareMatches(operator string, left, right float64) bool {
	switch operator {
	case "NumericEquals":
		return left == right
	case "NumericLessThan":
		return left < right
	case "NumericLessThanEquals":
		return left <= right
	case "NumericGreaterThan":
		return left > right
	case "NumericGreaterThanEquals":
		return left >= right
	default:
		return false
	}
}

func applyNullCondition(keyPresent bool, policyVals []string) (bool, bool) {
	for _, v := range policyVals {
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true":
			if !keyPresent {
				return true, true
			}
		case "false":
			if keyPresent {
				return true, true
			}
		default:
			// Ignore unsupported literal values in a mixed array.
		}
	}

	// If all values are unsupported, treat as not matched but known.
	return false, true
}

// ipMatchesCIDR reports whether ipStr is contained within cidrOrIP.
// cidrOrIP may be a CIDR block (e.g. "10.0.0.0/8") or a plain IP address.
func ipMatchesCIDR(ipStr, cidrOrIP string) bool {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return false
	}
	// Try CIDR first.
	if strings.Contains(cidrOrIP, "/") {
		_, ipNet, err := net.ParseCIDR(cidrOrIP)
		if err != nil {
			return false
		}
		return ipNet.Contains(ip)
	}
	// Plain IP equality.
	peer := net.ParseIP(cidrOrIP)
	if peer == nil {
		return false
	}
	return ip.Equal(peer)
}
