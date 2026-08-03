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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/iampolicy"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/state"
)

// iamEnforceCacheEntry is one principal's compiled policy set. Compiling is
// the expensive half of an evaluation (JSON parse plus wildcard compilation),
// so it happens once per principal and is reused until an IAM mutation calls
// InvalidateIAMEnforceCache. A principal with no policies — and an access key
// that resolves to no principal at all — is cached too, so a denied request
// does not rescan the IAM namespaces on every retry.
type iamEnforceCacheEntry struct {
	statements   []iampolicy.Statement
	principalCtx map[string]string
	// compileErr is set when the principal's policies could not be parsed.
	// Enforcement fails closed on it rather than evaluating a partial set.
	compileErr error
}

// iamEnforceGeneration is bumped by every IAM mutation. Each middleware
// instance stamps its cache with the generation it was built at and discards
// the cache when the two diverge, which is how a policy change reaches an
// already-warm cache.
//
// A counter rather than a registry of invalidation callbacks: a registry has to
// be added to when a router is built and removed from when one goes away, and
// the removal half is easy to forget — a test binary builds hundreds of routers
// and would accumulate one live callback (and its cache) per router, with every
// IAM mutation walking all of them. A generation is O(1) to invalidate however
// many routers exist, needs no registration or teardown, and lets a dead
// router's cache be collected with the router.
var iamEnforceGeneration atomic.Uint64

// InvalidateIAMEnforceCache marks every compiled-policy cache stale. Called by
// the IAM service after any operation that changes what a principal is allowed
// to do.
func InvalidateIAMEnforceCache() {
	iamEnforceGeneration.Add(1)
}

// iamEnforceCache holds one middleware instance's compiled policies, valid only
// for the generation it was built at.
type iamEnforceCache struct {
	mu         sync.RWMutex
	generation uint64
	entries    map[string]*iamEnforceCacheEntry
}

// load returns the cached entry for accessKeyID, and the generation the caller
// must still be on for a later store to be accepted. A cache built before the
// last invalidation is dropped here rather than consulted.
func (c *iamEnforceCache) load(accessKeyID string) (*iamEnforceCacheEntry, uint64) {
	generation := iamEnforceGeneration.Load()

	c.mu.RLock()
	if c.generation == generation {
		entry := c.entries[accessKeyID]
		c.mu.RUnlock()
		return entry, generation
	}
	c.mu.RUnlock()

	c.mu.Lock()
	if c.generation != generation {
		c.entries = nil
		c.generation = generation
	}
	c.mu.Unlock()
	return nil, generation
}

// store records an entry compiled at generation, and drops it if an IAM
// mutation landed while it was being compiled — the entry may already describe
// policies that no longer exist, and recompiling on the next request is
// cheaper than reasoning about which half is stale.
func (c *iamEnforceCache) store(generation uint64, accessKeyID string, entry *iamEnforceCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if iamEnforceGeneration.Load() != generation {
		return
	}
	if c.generation != generation || c.entries == nil {
		c.entries = make(map[string]*iamEnforceCacheEntry)
		c.generation = generation
	}
	c.entries[accessKeyID] = entry
}

// IAMEnforce enforces opt-in IAM authorization.
func IAMEnforce(enabled bool, st state.Store, logger *zap.Logger) func(http.Handler) http.Handler {
	cache := &iamEnforceCache{}

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
			result := evaluateIAMDecision(r, st, parts.AccessKey, action, resource, cache)
			if reason, denied := iamDenialReason(result); denied {
				// A deny the evaluator could not reason about is a gap in
				// Overcast, not a decision the user asked for: say so at warn
				// level rather than burying it in debug output.
				if logger != nil && (result.compileErr != nil || len(result.Unsupported) > 0) {
					logger.Warn("iam enforcement denied a request it could not evaluate",
						zap.String("service", detectService(r)),
						zap.String("action", action),
						zap.String("reason", reason),
					)
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

// iamDenialReason maps an evaluation onto the enforcement decision, and
// explains it for the debug log. Enforcement is deliberately fail-closed: a
// policy construct the evaluator does not implement denies rather than
// silently granting access it cannot reason about (the simulator surfaces the
// same information as AWS's PolicyEvaluation error).
func iamDenialReason(result iamEnforceResult) (string, bool) {
	if result.compileErr != nil {
		return "principal policy could not be parsed: " + result.compileErr.Error(), true
	}
	if len(result.Unsupported) > 0 {
		return "policy uses a construct the evaluator does not implement: " + strings.Join(result.Unsupported, "; "), true
	}
	switch result.Decision {
	case iampolicy.DecisionAllowed:
		return "", false
	case iampolicy.DecisionExplicitDeny:
		return "explicit deny policy matched", true
	case iampolicy.DecisionImplicitDeny:
		fallthrough
	default:
		reason := "policy did not allow action"
		if len(result.MissingContextKeys) > 0 {
			reason += " (missing condition context keys: " + strings.Join(result.MissingContextKeys, ", ") + ")"
		}
		return reason, true
	}
}

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

// iamEnforceResult is an evaluation plus whatever stopped it being one.
type iamEnforceResult struct {
	iampolicy.Result
	compileErr error
}

func evaluateIAMDecision(r *http.Request, st state.Store, accessKeyID, action, resource string, cache *iamEnforceCache) iamEnforceResult {
	cached, generation := cache.load(accessKeyID)
	if cached == nil {
		docs, principalCtx := collectPrincipalPolicyDocumentsAndContext(r.Context(), st, accessKeyID)
		statements, err := iampolicy.Compile(docs)
		cached = &iamEnforceCacheEntry{
			statements:   statements,
			principalCtx: principalCtx,
			compileErr:   err,
		}
		cache.store(generation, accessKeyID, cached)
	}
	if cached.compileErr != nil {
		return iamEnforceResult{compileErr: cached.compileErr}
	}

	reqCtx := buildIAMRequestContext(r)
	for k, v := range cached.principalCtx {
		reqCtx[k] = v
	}

	return iamEnforceResult{Result: iampolicy.Evaluate(iampolicy.Input{
		Request: iampolicy.Request{
			Action:           action,
			Resource:         resource,
			Context:          reqCtx,
			PrincipalARN:     cached.principalCtx["aws:principalarn"],
			PrincipalAccount: cached.principalCtx["aws:principalaccount"],
		},
		Identity: cached.statements,
	})}
}

func collectPrincipalPolicyDocumentsAndContext(ctx context.Context, st state.Store, accessKeyID string) ([]iampolicy.SourcedDocument, map[string]string) {
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

		docs := make([]iampolicy.SourcedDocument, 0, len(user.InlinePolicies)+len(user.AttachedPolicies))
		docs = appendInlineAndAttachedPolicyDocs(ctx, st, docs, iampolicy.SourceTypeUser, user.InlinePolicies, user.AttachedPolicies)
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

func appendGroupPolicyDocuments(ctx context.Context, st state.Store, docs []iampolicy.SourcedDocument, userName string) []iampolicy.SourcedDocument {
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
		docs = appendInlineAndAttachedPolicyDocs(ctx, st, docs, iampolicy.SourceTypeGroup, group.InlinePolicies, group.AttachedPolicies)
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
func appendRoleSessionPolicyDocumentsWithContext(ctx context.Context, st state.Store, accessKeyID string) ([]iampolicy.SourcedDocument, map[string]string) {
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

	docs := make([]iampolicy.SourcedDocument, 0, len(role.InlinePolicies)+len(role.AttachedPolicies))
	docs = appendInlineAndAttachedPolicyDocs(ctx, st, docs, iampolicy.SourceTypeRole, role.InlinePolicies, role.AttachedPolicies)

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
	docs []iampolicy.SourcedDocument,
	inlineSourceType string,
	inline map[string]string,
	attached []struct {
		PolicyArn string `json:"PolicyArn"`
	},
) []iampolicy.SourcedDocument {
	for name, doc := range inline {
		docs = append(docs, iampolicy.SourcedDocument{
			Source:   iampolicy.SourceRef{ID: name, Type: inlineSourceType},
			Document: doc,
		})
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
			docs = append(docs, iampolicy.SourcedDocument{
				Source:   iampolicy.SourceRef{ID: policy.PolicyArn, Type: iampolicy.SourceTypeIAMPolicy},
				Document: managed.Document,
			})
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
