// Package events provides the internal event bus used for cross-service
// notifications (e.g. S3 → SQS, SNS → SQS, SQS → Lambda).
//
// Architecture
//
//	Publisher (e.g. S3 handler)
//	    └─ bus.Publish(ctx, Event{...})
//	           │
//	           ▼  (goroutine per subscriber)
//	    Subscriber (e.g. S3 notification dispatcher)
//	           └─ reads per-resource config, routes to a Sink
//
// The bus is the only component shared across service packages.
// Services publish; dispatchers subscribe; sinks deliver.
// No service package imports another service package.
package events

import "time"

// Type identifies the kind of event. Values follow the AWS event name
// convention so they can be stored in notification filter rules verbatim.
type Type string

const (
	// All is a wildcard that receives every event regardless of type.
	// Used by the SSE event-stream endpoint to fan out all events to connected clients.
	All Type = "*"

	// S3ObjectCreated fires after a successful PutObject or CopyObject.
	S3ObjectCreated Type = "s3:ObjectCreated:*"
	// S3ObjectRemoved fires after a successful DeleteObject.
	S3ObjectRemoved Type = "s3:ObjectRemoved:*"

	// DynamoDBStreamInsert fires when a new item is inserted into a stream-enabled table.
	DynamoDBStreamInsert Type = "dynamodb:insert"
	// DynamoDBStreamModify fires when an existing item is overwritten in a stream-enabled table.
	DynamoDBStreamModify Type = "dynamodb:modify"
	// DynamoDBStreamRemove fires when an item is deleted from a stream-enabled table.
	DynamoDBStreamRemove Type = "dynamodb:remove"
	// DynamoDBStreamRecord fires alongside every Insert/Modify/Remove event and
	// carries the stream record in the AWS Streams / Lambda event shape (uppercase
	// Keys/NewImage/OldImage). Intended for observability: the "dynamodb" sub-object
	// can be compared directly against ESM filter patterns in the event console.
	DynamoDBStreamRecord Type = "dynamodb:StreamRecord"

	// SNSMessagePublished fires when a Publish API call is received and the topic is resolved,
	// before fan-out begins. Used by the UI to show "something hit this topic" independently
	// of whether there are any active subscribers.
	SNSMessagePublished Type = "sns:Published"

	// SNSMessageDelivered fires after SNS successfully delivers a notification to an SQS subscriber.
	SNSMessageDelivered Type = "sns:Notification"

	// SNSEmailDelivered fires after SNS successfully delivers a notification via email (email or email-json protocol).
	SNSEmailDelivered Type = "sns:EmailDelivered"

	// SNSSMSDelivered fires after SNS successfully delivers a notification via the sms protocol.
	SNSSMSDelivered Type = "sns:SMSDelivered"

	// SNSWebhookDelivered fires after SNS successfully captures an http/https subscription delivery in the inbox.
	SNSWebhookDelivered Type = "sns:WebhookDelivered"

	// SNSPushDelivered fires after SNS successfully captures an application (mobile push) subscription delivery in the inbox.
	SNSPushDelivered Type = "sns:PushDelivered"

	// InboxDelivered fires after the built-in SMTP capture server stores an inbound message.
	// Source is always "inbox".
	InboxDelivered Type = "inbox:Delivered"

	// PipesDelivered fires after a Pipe successfully delivers a stream record to its target queue.
	PipesDelivered Type = "pipes:Delivered"
	// PipesStateChanged fires when a Pipe transitions between lifecycle states (e.g. CREATING→RUNNING, RUNNING→DELETING).
	PipesStateChanged Type = "pipes:StateChanged"

	// ---- Resource lifecycle events ----------------------------------------.
	// These fire when resources are created or deleted, allowing connected
	// clients (e.g. the topology map) to update without polling.

	// S3BucketCreated fires after a bucket is successfully created.
	S3BucketCreated Type = "s3:BucketCreated"
	// S3BucketDeleted fires after a bucket is successfully deleted.
	S3BucketDeleted Type = "s3:BucketDeleted"
	// S3NotificationConfigured fires after a bucket's notification config is changed.
	S3NotificationConfigured Type = "s3:NotificationConfigured"

	// SQSQueueCreated fires after a new SQS queue is created.
	SQSQueueCreated Type = "sqs:QueueCreated"
	// SQSQueueDeleted fires after an SQS queue is deleted.
	SQSQueueDeleted Type = "sqs:QueueDeleted"
	// SQSQueuePurged fires after all messages in a queue are deleted via PurgeQueue.
	SQSQueuePurged Type = "sqs:QueuePurged"
	// SQSMessageSent fires when a message is successfully written to a queue via
	// SendMessage or SendMessageBatch. The map and detail page use this to update
	// immediately rather than waiting for a polling interval.
	SQSMessageSent Type = "sqs:MessageSent"
	// SQSMessageInflight fires when a message transitions from visible to in-flight
	// (i.e. when ReceiveMessage is called and a visibility timeout is applied).
	SQSMessageInflight Type = "sqs:MessageInflight"
	// SQSMessageVisible fires when an in-flight message's visibility timeout expires
	// and the message becomes visible to consumers again.
	SQSMessageVisible Type = "sqs:MessageVisible"
	// SQSMessageDeleted fires when a message is successfully deleted from a queue.
	SQSMessageDeleted Type = "sqs:MessageDeleted"
	// SQSMessageDLQ fires when a message is moved to a dead letter queue because
	// its ApproximateReceiveCount reached the RedrivePolicy maxReceiveCount.
	SQSMessageDLQ Type = "sqs:MessageDLQ"
	// SQSMessageRedrive fires when messages are redriven from a DLQ back to the
	// source queue via StartMessageMoveTask.
	SQSMessageRedrive Type = "sqs:MessageRedrive"

	// SNSTopicCreated fires after a new SNS topic is created.
	SNSTopicCreated Type = "sns:TopicCreated"
	// SNSTopicDeleted fires after an SNS topic is deleted.
	SNSTopicDeleted Type = "sns:TopicDeleted"
	// SNSSubscriptionCreated fires after a new subscription is created.
	SNSSubscriptionCreated Type = "sns:SubscriptionCreated"
	// SNSSubscriptionDeleted fires after a subscription is deleted.
	SNSSubscriptionDeleted Type = "sns:SubscriptionDeleted"

	// DynamoDBTableCreated fires after a new DynamoDB table is created.
	DynamoDBTableCreated Type = "dynamodb:TableCreated"
	// DynamoDBTableDeleted fires after a DynamoDB table is deleted.
	DynamoDBTableDeleted Type = "dynamodb:TableDeleted"
	// DynamoDBStreamUpdated fires after a DynamoDB table's stream config changes.
	DynamoDBStreamUpdated Type = "dynamodb:StreamUpdated"
	// DynamoDBItemMutated fires after a successful PutItem, UpdateItem, or DeleteItem,
	// regardless of whether the table has a DynamoDB Stream enabled. Used by the UI to
	// keep the items query fresh without polling.
	DynamoDBItemMutated Type = "dynamodb:ItemMutated"

	// LambdaFunctionCreated fires after a new Lambda function is created.
	LambdaFunctionCreated Type = "lambda:FunctionCreated"
	// LambdaFunctionDeleted fires after a Lambda function is deleted.
	LambdaFunctionDeleted Type = "lambda:FunctionDeleted"
	// LambdaFunctionUpdated fires after a Lambda function's code or configuration is updated.
	LambdaFunctionUpdated Type = "lambda:FunctionUpdated"
	// LambdaInstanceAcquired fires when a Lambda invocation starts (instance moves to running).
	LambdaInstanceAcquired Type = "lambda:InstanceAcquired"
	// LambdaInstanceReady fires when a Lambda container finishes starting and is
	// ready to receive the runtime. Transitions status from "starting" to "initializing".
	LambdaInstanceReady Type = "lambda:InstanceReady"
	// LambdaInstanceInitializing fires when the container's runtime interface has
	// connected (first GET /next). Transitions status from "initializing" to "running".
	LambdaInstanceInitializing Type = "lambda:InstanceInitializing"
	// LambdaInstanceReleased fires when a Lambda invocation completes (instance moves to idle).
	LambdaInstanceReleased Type = "lambda:InstanceReleased"
	// LambdaInstanceEvicted fires when an idle Lambda instance is evicted after the 15-minute idle TTL.
	LambdaInstanceEvicted Type = "lambda:InstanceEvicted"

	// LambdaESMRecordFiltered fires when one or more records/messages are dropped
	// by an EventSourceMapping's FilterCriteria before being sent to Lambda.
	// Payload: LambdaESMEventPayload.
	LambdaESMRecordFiltered Type = "lambda:ESMRecordFiltered"
	// LambdaESMRecordMatched fires when a DynamoDB stream record matches an
	// EventSourceMapping's FilterCriteria and is allowed into an invocation batch.
	// Payload: LambdaESMEventPayload.
	LambdaESMRecordMatched Type = "lambda:ESMRecordMatched"

	// LambdaESMInvoked fires when a record batch passes filter criteria and
	// Lambda is about to be called. Payload: LambdaESMEventPayload.
	LambdaESMInvoked Type = "lambda:ESMInvoked"

	// LambdaImagePulling fires when a Docker image pull begins for a Lambda
	// function or runtime. Emitted at most once per image (coalesced via
	// sync.Once). Payload: LambdaImagePullPayload.
	LambdaImagePulling Type = "lambda:ImagePulling"
	// LambdaImagePullComplete fires when a Docker image pull finishes, whether
	// successful or not. Payload: LambdaImagePullPayload (Error empty = success).
	LambdaImagePullComplete Type = "lambda:ImagePullComplete"

	// LogGroupCreated fires after a new CloudWatch Logs log group is created.
	LogGroupCreated Type = "logs:LogGroupCreated"
	// LogGroupDeleted fires after a CloudWatch Logs log group is deleted.
	LogGroupDeleted Type = "logs:LogGroupDeleted"
	// LogStreamCreated fires after a new CloudWatch Logs log stream is created.
	LogStreamCreated Type = "logs:LogStreamCreated"
	// LogStreamDeleted fires after a CloudWatch Logs log stream is deleted.
	LogStreamDeleted Type = "logs:LogStreamDeleted"
	// LogEventsWritten fires when PutLogEvents appends new events to a log stream.
	// Payload: LogEventsWrittenPayload.
	LogEventsWritten Type = "logs:LogEventsWritten"

	// ---- IAM resource lifecycle events ----------------------------------------.

	// IAMRoleCreated fires after a new IAM role is created.
	IAMRoleCreated Type = "iam:RoleCreated"
	// IAMRoleDeleted fires after an IAM role is deleted.
	IAMRoleDeleted Type = "iam:RoleDeleted"
	// IAMUserCreated fires after a new IAM user is created.
	IAMUserCreated Type = "iam:UserCreated"
	// IAMUserDeleted fires after an IAM user is deleted.
	IAMUserDeleted Type = "iam:UserDeleted"
	// IAMPolicyCreated fires after a new managed IAM policy is created.
	IAMPolicyCreated Type = "iam:PolicyCreated"
	// IAMPolicyDeleted fires after a managed IAM policy is deleted.
	IAMPolicyDeleted Type = "iam:PolicyDeleted"

	// ---- STS informational events ----------------------------------------.

	// STSRoleAssumed fires when AssumeRole or AssumeRoleWithWebIdentity succeeds.
	STSRoleAssumed Type = "sts:RoleAssumed"

	// ---- SSM parameter lifecycle events ----------------------------------------.

	// SSMParameterCreated fires after a new SSM parameter is created via PutParameter.
	SSMParameterCreated Type = "ssm:ParameterCreated"
	// SSMParameterUpdated fires after an SSM parameter value is updated via PutParameter.
	SSMParameterUpdated Type = "ssm:ParameterUpdated"
	// SSMParameterDeleted fires after an SSM parameter is deleted.
	SSMParameterDeleted Type = "ssm:ParameterDeleted"

	// ---- KMS key lifecycle events ----------------------------------------.

	// KMSKeyCreated fires after a new KMS key is created.
	KMSKeyCreated Type = "kms:KeyCreated"
	// KMSKeyDeleted fires after a KMS key deletion is scheduled.
	KMSKeyDeleted Type = "kms:KeyDeleted"
	// KMSKeyStateChanged fires when a key is enabled, disabled, or deletion cancelled.
	KMSKeyStateChanged Type = "kms:KeyStateChanged"

	// ---- Secrets Manager lifecycle events ----------------------------------------.

	// SecretCreated fires after a new secret is created.
	SecretCreated Type = "secretsmanager:SecretCreated"
	// SecretUpdated fires after a secret value is updated.
	SecretUpdated Type = "secretsmanager:SecretUpdated"
	// SecretDeleted fires after a secret is deleted (or scheduled for deletion).
	SecretDeleted Type = "secretsmanager:SecretDeleted"
	// SecretRotated fires after a secret rotation is triggered.
	SecretRotated Type = "secretsmanager:SecretRotated"

	// ---- SES email events ----------------------------------------.

	// SESEmailSent fires after an email is sent via SendEmail/SendRawEmail/SendTemplatedEmail.
	SESEmailSent Type = "ses:EmailSent"
	// SESIdentityCreated fires after a new email/domain identity is verified.
	SESIdentityCreated Type = "ses:IdentityCreated"
	// SESIdentityDeleted fires after an email/domain identity is deleted.
	SESIdentityDeleted Type = "ses:IdentityDeleted"
	// SESTemplateCreated fires after a new SES email template is created.
	SESTemplateCreated Type = "ses:TemplateCreated"
	// SESTemplateDeleted fires after an SES email template is deleted.
	SESTemplateDeleted Type = "ses:TemplateDeleted"

	// ---- Kinesis stream lifecycle events ----------------------------------------.

	// KinesisStreamCreated fires after a new Kinesis data stream is created.
	KinesisStreamCreated Type = "kinesis:StreamCreated"
	// KinesisStreamDeleted fires after a Kinesis data stream is deleted.
	KinesisStreamDeleted Type = "kinesis:StreamDeleted"

	// ---- EC2 resource lifecycle events ----------------------------------------.

	// EC2VpcCreated fires after a new VPC is created.
	EC2VpcCreated Type = "ec2:VpcCreated"
	// EC2VpcDeleted fires after a VPC is deleted.
	EC2VpcDeleted Type = "ec2:VpcDeleted"
	// EC2SubnetCreated fires after a new subnet is created.
	EC2SubnetCreated Type = "ec2:SubnetCreated"
	// EC2SubnetDeleted fires after a subnet is deleted.
	EC2SubnetDeleted Type = "ec2:SubnetDeleted"
	// EC2SecurityGroupCreated fires after a new security group is created.
	EC2SecurityGroupCreated Type = "ec2:SecurityGroupCreated"
	// EC2SecurityGroupDeleted fires after a security group is deleted.
	EC2SecurityGroupDeleted Type = "ec2:SecurityGroupDeleted"
	// EC2InstanceLaunched fires after RunInstances creates new instance(s).
	EC2InstanceLaunched Type = "ec2:InstanceLaunched"
	// EC2InstanceTerminated fires after an instance reaches the terminated state.
	EC2InstanceTerminated Type = "ec2:InstanceTerminated"
	// EC2InstanceStarted fires after a stopped instance reaches the running state.
	EC2InstanceStarted Type = "ec2:InstanceStarted"
	// EC2InstanceStopped fires after a running instance reaches the stopped state.
	EC2InstanceStopped Type = "ec2:InstanceStopped"

	// ---- ECS resource lifecycle events ----------------------------------------.

	// ECSClusterCreated fires after a new ECS cluster is created.
	ECSClusterCreated Type = "ecs:ClusterCreated"
	// ECSClusterDeleted fires after an ECS cluster is deleted.
	ECSClusterDeleted Type = "ecs:ClusterDeleted"
	// ECSTaskDefinitionRegistered fires after a task definition is registered.
	ECSTaskDefinitionRegistered Type = "ecs:TaskDefinitionRegistered"
	// ECSTaskDefinitionDeregistered fires after a task definition is deregistered.
	ECSTaskDefinitionDeregistered Type = "ecs:TaskDefinitionDeregistered"
	// ECSTaskStarted fires after a task reaches the RUNNING state.
	ECSTaskStarted Type = "ecs:TaskStarted"
	// ECSTaskStopped fires after a task reaches the STOPPED state.
	ECSTaskStopped Type = "ecs:TaskStopped"
	// ECSServiceCreated fires after a new ECS service is created.
	ECSServiceCreated Type = "ecs:ServiceCreated"
	// ECSServiceUpdated fires after an ECS service configuration is updated.
	ECSServiceUpdated Type = "ecs:ServiceUpdated"
	// ECSServiceDeleted fires after an ECS service is deleted.
	ECSServiceDeleted Type = "ecs:ServiceDeleted"

	// ---- RDS resource lifecycle events ----------------------------------------.

	// RDSInstanceCreated fires after a new DB instance reaches the available state.
	RDSInstanceCreated Type = "rds:InstanceCreated"
	// RDSInstanceDeleted fires after a DB instance is deleted.
	RDSInstanceDeleted Type = "rds:InstanceDeleted"
	// RDSInstanceModified fires after a DB instance configuration is modified.
	RDSInstanceModified Type = "rds:InstanceModified"
	// RDSInstanceStarted fires after a stopped DB instance reaches the available state.
	RDSInstanceStarted Type = "rds:InstanceStarted"
	// RDSInstanceStopped fires after a DB instance reaches the stopped state.
	RDSInstanceStopped Type = "rds:InstanceStopped"
	// RDSSubnetGroupCreated fires after a new DB subnet group is created.
	RDSSubnetGroupCreated Type = "rds:SubnetGroupCreated"
	// RDSSubnetGroupDeleted fires after a DB subnet group is deleted.
	RDSSubnetGroupDeleted Type = "rds:SubnetGroupDeleted"

	// ---- EventBridge lifecycle events ----------------------------------------.

	// EventBridgeBusCreated fires after a new EventBridge event bus is created.
	EventBridgeBusCreated Type = "eventbridge:BusCreated"
	// EventBridgeBusDeleted fires after an EventBridge event bus is deleted.
	EventBridgeBusDeleted Type = "eventbridge:BusDeleted"
	// EventBridgeRuleCreated fires after a new EventBridge rule is created.
	EventBridgeRuleCreated Type = "eventbridge:RuleCreated"
	// EventBridgeRuleDeleted fires after an EventBridge rule is deleted.
	EventBridgeRuleDeleted Type = "eventbridge:RuleDeleted"

	// ---- Step Functions lifecycle events ----------------------------------------.

	// SFNStateMachineCreated fires after a new state machine is created.
	SFNStateMachineCreated Type = "stepfunctions:StateMachineCreated"
	// SFNStateMachineDeleted fires after a state machine is deleted.
	SFNStateMachineDeleted Type = "stepfunctions:StateMachineDeleted"
	// SFNExecutionStarted fires after a new execution is started.
	SFNExecutionStarted Type = "stepfunctions:ExecutionStarted"

	// ---- AppSync lifecycle events ----------------------------------------.

	// AppSyncAPICreated fires after a new GraphQL API is created.
	AppSyncAPICreated Type = "appsync:ApiCreated"
	// AppSyncAPIUpdated fires after a GraphQL API is updated.
	AppSyncAPIUpdated Type = "appsync:ApiUpdated"
	// AppSyncAPIDeleted fires after a GraphQL API is deleted.
	AppSyncAPIDeleted Type = "appsync:ApiDeleted"
	// AppSyncSchemaUpdated fires after a schema is uploaded.
	AppSyncSchemaUpdated Type = "appsync:SchemaUpdated"
	// AppSyncDataSourceCreated fires after a data source is created.
	AppSyncDataSourceCreated Type = "appsync:DataSourceCreated"
	// AppSyncDataSourceDeleted fires after a data source is deleted.
	AppSyncDataSourceDeleted Type = "appsync:DataSourceDeleted"
	// AppSyncResolverCreated fires after a resolver is created.
	AppSyncResolverCreated Type = "appsync:ResolverCreated"
	// AppSyncResolverDeleted fires after a resolver is deleted.
	AppSyncResolverDeleted Type = "appsync:ResolverDeleted"

	// ---- Cognito lifecycle events ----------------------------------------.

	// CognitoUserPoolCreated fires after a new user pool is created.
	CognitoUserPoolCreated Type = "cognito:UserPoolCreated"
	// CognitoUserPoolDeleted fires after a user pool is deleted.
	CognitoUserPoolDeleted Type = "cognito:UserPoolDeleted"
	// CognitoUserCreated fires after a user is created (AdminCreateUser or SignUp).
	CognitoUserCreated Type = "cognito:UserCreated"
	// CognitoUserDeleted fires after a user is deleted.
	CognitoUserDeleted Type = "cognito:UserDeleted"
	// CognitoGroupCreated fires after a group is created.
	CognitoGroupCreated Type = "cognito:GroupCreated"
	// CognitoGroupDeleted fires after a group is deleted.
	CognitoGroupDeleted Type = "cognito:GroupDeleted"
	// CognitoClientCreated fires after a user pool client is created.
	CognitoClientCreated Type = "cognito:ClientCreated"
	// CognitoClientDeleted fires after a user pool client is deleted.
	CognitoClientDeleted Type = "cognito:ClientDeleted"

	// CognitoUserConfirmed fires when a user is confirmed (ConfirmSignUp, AdminConfirmSignUp, or new-password challenge).
	CognitoUserConfirmed Type = "cognito:UserConfirmed"
	// CognitoUserUpdated fires when a user's attributes, password, or enabled state changes.
	CognitoUserUpdated Type = "cognito:UserUpdated"
	// CognitoSignIn fires after a successful authentication (InitiateAuth / AdminInitiateAuth).
	CognitoSignIn Type = "cognito:SignIn"
	// CognitoPasswordChanged fires after a password is changed or reset (ChangePassword, ConfirmForgotPassword).
	CognitoPasswordChanged Type = "cognito:PasswordChanged"
	// CognitoSignOut fires after a GlobalSignOut or RevokeToken call.
	CognitoSignOut Type = "cognito:SignOut"
	// CognitoSignInFailed fires when an authentication attempt fails (wrong password, disabled user, etc.).
	CognitoSignInFailed Type = "cognito:SignInFailed"
	// CognitoGroupUpdated fires after a group's attributes are updated.
	CognitoGroupUpdated Type = "cognito:GroupUpdated"
	// CognitoGroupMembershipChanged fires when a user is added to or removed from a group.
	CognitoGroupMembershipChanged Type = "cognito:GroupMembershipChanged"

	// ---- CloudFront lifecycle events ----------------------------------------.

	// CloudFrontDistributionCreated fires after a new CloudFront distribution is created.
	CloudFrontDistributionCreated Type = "cloudfront:DistributionCreated"
	// CloudFrontDistributionUpdated fires after a CloudFront distribution configuration is updated.
	CloudFrontDistributionUpdated Type = "cloudfront:DistributionUpdated"
	// CloudFrontDistributionDeleted fires after a CloudFront distribution is deleted.
	CloudFrontDistributionDeleted Type = "cloudfront:DistributionDeleted"
	// CloudFrontInvalidationCreated fires after a new invalidation is created.
	CloudFrontInvalidationCreated Type = "cloudfront:InvalidationCreated"

	// ---- API Gateway lifecycle events ----------------------------------------.

	// APIGatewayHTTPAPICreated fires after a new HTTP API (v2) is created.
	APIGatewayHTTPAPICreated Type = "apigateway:HttpApiCreated"
	// APIGatewayHTTPAPIDeleted fires after an HTTP API (v2) is deleted.
	APIGatewayHTTPAPIDeleted Type = "apigateway:HttpApiDeleted"
	// APIGatewayRestAPICreated fires after a new REST API (v1) is created.
	APIGatewayRestAPICreated Type = "apigateway:RestApiCreated"
	// APIGatewayRestAPIDeleted fires after a REST API (v1) is deleted.
	APIGatewayRestAPIDeleted Type = "apigateway:RestApiDeleted"
	// APIGatewayDeployed fires after a stage deployment is created.
	APIGatewayDeployed Type = "apigateway:Deployed"

	// ---- CloudFormation lifecycle events ----------------------------------------.

	// CFNStackCreated fires after a CloudFormation stack reaches CREATE_COMPLETE.
	CFNStackCreated Type = "cloudformation:StackCreated"
	// CFNStackUpdated fires after a CloudFormation stack reaches UPDATE_COMPLETE.
	CFNStackUpdated Type = "cloudformation:StackUpdated"
	// CFNStackDeleted fires after a CloudFormation stack reaches DELETE_COMPLETE.
	CFNStackDeleted Type = "cloudformation:StackDeleted"
	// CFNStackFailed fires when a stack operation enters a *_FAILED state.
	CFNStackFailed Type = "cloudformation:StackFailed"
	// CFNChangeSetCreated fires after a new change set is created.
	CFNChangeSetCreated Type = "cloudformation:ChangeSetCreated"
	// CFNChangeSetExecuted fires after a change set is successfully executed.
	CFNChangeSetExecuted Type = "cloudformation:ChangeSetExecuted"
	// CFNResourceProvisioned fires when a single resource within a stack is created.
	CFNResourceProvisioned Type = "cloudformation:ResourceProvisioned"
	// CFNResourceDeleted fires when a single resource within a stack is deleted.
	CFNResourceDeleted Type = "cloudformation:ResourceDeleted"

	// ---- ElastiCache resource lifecycle events ----------------------------------------.

	// ElastiCacheClusterCreated fires after a new cache cluster reaches the available state.
	ElastiCacheClusterCreated Type = "elasticache:ClusterCreated"
	// ElastiCacheClusterDeleted fires after a cache cluster is deleted.
	ElastiCacheClusterDeleted Type = "elasticache:ClusterDeleted"
	// ElastiCacheClusterModified fires after a cache cluster configuration is modified.
	ElastiCacheClusterModified Type = "elasticache:ClusterModified"
	// ElastiCacheReplicationGroupCreated fires after a replication group is created.
	ElastiCacheReplicationGroupCreated Type = "elasticache:ReplicationGroupCreated"
	// ElastiCacheReplicationGroupDeleted fires after a replication group is deleted.
	ElastiCacheReplicationGroupDeleted Type = "elasticache:ReplicationGroupDeleted"

	// ---- ECR resource lifecycle events ----------------------------------------.

	// ECRRepositoryCreated fires after a new ECR repository is created.
	ECRRepositoryCreated Type = "ecr:RepositoryCreated"
	// ECRRepositoryDeleted fires after an ECR repository is deleted.
	ECRRepositoryDeleted Type = "ecr:RepositoryDeleted"
	// ECRImagePushed fires when an image manifest is stored via PutImage.
	ECRImagePushed Type = "ecr:ImagePushed"

	// ---- Request event --------------------------------------------------------.
	// Published by middleware for every incoming HTTP request so SDK callers
	// can see their API calls appear live in the event stream.

	// RequestReceived fires after every incoming HTTP request completes.
	// Source is always "request". Payload is RequestPayload.
	RequestReceived Type = "request:Received"

	// ServiceError fires when an emulated service encounters a recoverable
	// error that would otherwise only appear in server logs. Source is the
	// originating service name (e.g. "lambda"). Payload is ErrorPayload.
	// Use this to surface misconfiguration, transient failures, and
	// invocation errors directly in the event console.
	ServiceError Type = "service:Error"

	// ---- Docker container lifecycle events ----------------------------------------.
	// These are published by the Docker watcher (internal/docker/watcher.go)
	// when Overcast-managed containers change state. Services subscribe to
	// these instead of running per-container WaitContainer goroutines.

	// DockerContainerStarted fires when a managed container starts.
	DockerContainerStarted Type = "docker:ContainerStarted"
	// DockerContainerDied fires when a managed container exits (die event).
	DockerContainerDied Type = "docker:ContainerDied"
	// DockerContainerStopped fires when a managed container is stopped.
	DockerContainerStopped Type = "docker:ContainerStopped"
	// DockerContainerOOM fires when a managed container is killed by OOM.
	DockerContainerOOM Type = "docker:ContainerOOM"
	// DockerContainerHealthStatus fires when a managed container's health check changes.
	DockerContainerHealthStatus Type = "docker:ContainerHealthStatus"

	// ---- Docker network lifecycle events ------------------------------------------
	// Published by the Docker watcher when Overcast-managed networks are
	// created, destroyed, or have containers connected/disconnected.

	// DockerNetworkCreated fires when a managed network is created.
	DockerNetworkCreated Type = "docker:NetworkCreated"
	// DockerNetworkDestroyed fires when a managed network is removed.
	DockerNetworkDestroyed Type = "docker:NetworkDestroyed"
	// DockerNetworkConnect fires when a container is connected to a managed network.
	DockerNetworkConnect Type = "docker:NetworkConnect"
	// DockerNetworkDisconnect fires when a container is disconnected from a managed network.
	DockerNetworkDisconnect Type = "docker:NetworkDisconnect"
)

// S3ObjectPayload carries the details of an S3 object mutation event.
type S3ObjectPayload struct {
	Bucket    string
	Key       string
	Size      int64
	ETag      string
	EventName string // e.g. "ObjectCreated:Put", "ObjectRemoved:Delete"
}

// DynamoDBStreamPayload carries the full details of a DynamoDB Streams change record.
// It is sufficient for delivery to SQS without an additional GetRecords call.
type DynamoDBStreamPayload struct {
	Table          string `json:"table"`
	EventName      string `json:"eventName"` // INSERT, MODIFY, REMOVE
	SequenceNumber int64  `json:"sequenceNumber"`
	Keys           any    `json:"keys"`
	NewImage       any    `json:"newImage,omitempty"`
	OldImage       any    `json:"oldImage,omitempty"`
	CreatedAt      int64  `json:"createdAt"` // UnixMilli
}

// DynamoDBStreamRecordPayload is the payload for DynamoDBStreamRecord events.
// The Dynamodb field mirrors the AWS Streams StreamRecord shape (uppercase Keys,
// NewImage, OldImage) so the event console can display exactly what ESM filter
// patterns are evaluated against.
type DynamoDBStreamRecordPayload struct {
	Table     string         `json:"table"`
	EventName string         `json:"eventName"` // INSERT | MODIFY | REMOVE
	Dynamodb  map[string]any `json:"dynamodb"`  // uppercase keys matching AWS StreamRecord
}

// SNSPublishPayload carries the details of a Publish API call (before fan-out).
// TopicName is a bare name (not an ARN) to match the "sns::TOPIC" node ID.
type SNSPublishPayload struct {
	TopicName string `json:"topicName"`
	MessageID string `json:"messageId"`
}

// SNSNotificationPayload carries the details of a successful SNS→SQS delivery.
// TopicName and QueueName are bare names (not ARNs) to match "sns::TOPIC" and "sqs::QUEUE" node IDs.
type SNSNotificationPayload struct {
	TopicName string `json:"topicName"` // SNS topic name, not full ARN
	QueueName string `json:"queueName"` // SQS queue name, not full ARN
	MessageID string `json:"messageId"`
}

// SNSEmailPayload carries the details of a successful SNS→email delivery.
type SNSEmailPayload struct {
	TopicName string   `json:"topicName"`
	To        []string `json:"to"`
	Subject   string   `json:"subject"`
	MessageID string   `json:"messageId"`
}

// SNSSMSPayload carries the details of a successful SNS→SMS delivery.
type SNSSMSPayload struct {
	TopicName string `json:"topicName"`
	To        string `json:"to"`
	MessageID string `json:"messageId"`
}

// SNSWebhookPayload carries the details of a successful SNS→http/https delivery captured in the inbox.
type SNSWebhookPayload struct {
	TopicName string `json:"topicName"`
	Endpoint  string `json:"endpoint"`
	MessageID string `json:"messageId"`
}

// SNSPushPayload carries the details of a successful SNS→application delivery captured in the inbox.
type SNSPushPayload struct {
	TopicName string `json:"topicName"`
	Endpoint  string `json:"endpoint"`
	MessageID string `json:"messageId"`
}

// PipesDeliveryPayload carries the details of a successful Pipe delivery.
// SourceTable and TargetQueue are bare names (not ARNs) so that map/UI code
// can match them directly against "dynamodb::TABLE" and "sqs::QUEUE" node IDs.
type PipesDeliveryPayload struct {
	PipeName    string `json:"pipeName"`
	SourceTable string `json:"sourceTable"` // DynamoDB table name, not full ARN
	TargetQueue string `json:"targetQueue"` // SQS queue name, not full ARN
	EventName   string `json:"eventName"`   // INSERT, MODIFY, REMOVE
	MessageID   string `json:"messageId"`   // delivery-correlation UUID
}

// PipesStateChangedPayload carries the old and new state of a Pipe lifecycle transition.
type PipesStateChangedPayload struct {
	PipeName string `json:"pipeName"`
	OldState string `json:"oldState"`
	NewState string `json:"newState"`
}

// ResourcePayload is the payload for resource lifecycle events (created, deleted, updated).
// Name is the bare resource name (not an ARN) — e.g. bucket name, queue name, table name.
type ResourcePayload struct {
	Name string `json:"name"`
	ARN  string `json:"arn,omitempty"`
}

// InboxDeliveredPayload carries the key fields of a captured SMTP message.
type InboxDeliveredPayload struct {
	ID      string   `json:"id"`
	From    string   `json:"from"`
	To      []string `json:"to"`
	Subject string   `json:"subject"`
}

// SQSMessagePayload carries the details of an SQS message visibility state change.
// It is emitted on SQSMessageSent, SQSMessageInflight, SQSMessageVisible, and SQSMessageDeleted events.
type SQSMessagePayload struct {
	QueueName               string `json:"queueName"`
	MessageID               string `json:"messageId"`
	VisibleAfter            int64  `json:"visibleAfter,omitempty"`            // UnixMilli; set on Inflight events only
	ApproximateReceiveCount int    `json:"approximateReceiveCount,omitempty"` // set on Inflight events only
}

// SQSDLQPayload carries the details of a message moved to a dead letter queue.
// SourceQueue and DLQQueue are bare queue names (not ARNs) to match topology
// node IDs directly ("sqs::SOURCE" → "sqs::DLQ").
type SQSDLQPayload struct {
	SourceQueue string `json:"sourceQueue"`
	DLQQueue    string `json:"dlqQueue"`
	MessageID   string `json:"messageId"`
}

// SQSRedrivePayload carries the details of a DLQ redrive operation.
type SQSRedrivePayload struct {
	SourceQueue      string `json:"sourceQueue"`
	DestinationQueue string `json:"destinationQueue"`
	MessageCount     int    `json:"messageCount"`
}

// LambdaFunctionPayload carries the key fields of a Lambda function lifecycle event.
type LambdaFunctionPayload struct {
	Name string `json:"name"`
	ARN  string `json:"arn"`
}

// LambdaImagePullPayload carries the details of a Docker image pull for a
// Lambda function or runtime. Published on LambdaImagePulling and
// LambdaImagePullComplete events.
type LambdaImagePullPayload struct {
	// Image is the full Docker image reference being pulled.
	Image string `json:"image"`
	// ElapsedMs is set on LambdaImagePullComplete — the pull duration in ms.
	ElapsedMs int64 `json:"elapsedMs,omitempty"`
	// Error is set on LambdaImagePullComplete when the pull failed.
	Error string `json:"error,omitempty"`
}

// LambdaESMEventPayload carries the details of an EventSourceMapping delivery
// attempt. Published on LambdaESMRecordFiltered and LambdaESMInvoked.
type LambdaESMEventPayload struct {
	// ESMID is the UUID of the EventSourceMapping.
	ESMID string `json:"esmId"`
	// FunctionName is the bare Lambda function name (not an ARN).
	FunctionName string `json:"functionName"`
	// EventSource is the bare source resource name — SQS queue name or
	// DynamoDB table name, not a full ARN — to match topology node IDs.
	EventSource string `json:"eventSource"`
	// SourceType is "sqs" or "dynamodb".
	SourceType string `json:"sourceType"`
	// EventName is the DynamoDB stream event name (INSERT/MODIFY/REMOVE).
	// Empty for SQS events.
	EventName string `json:"eventName,omitempty"`
	// RecordCount is the number of records/messages in the batch.
	RecordCount int `json:"recordCount"`
	// FilterPatterns lists every filter pattern that was evaluated and failed.
	// Non-nil only on LambdaESMRecordFiltered events; nil when no filter
	// criteria is configured. Since filters are OR'd, all patterns must fail
	// for a record to be dropped, so the full list indicates the reason.
	FilterPatterns []string `json:"filterPatterns,omitempty"`
	// Matched reports the per-record FilterCriteria decision when present.
	Matched *bool `json:"matched,omitempty"`
	// Record is the DynamoDB stream record shape that was evaluated by the filter.
	Record map[string]any `json:"record,omitempty"`
}

// LambdaInstancePayload carries a snapshot of a Lambda execution instance.
// Published on LambdaInstanceAcquired, LambdaInstanceReleased, and LambdaInstanceEvicted events.
type LambdaInstancePayload struct {
	// InstanceID is a stable UUID assigned at cold start and preserved across warm reuses.
	InstanceID   string `json:"instanceId"`
	FunctionName string `json:"functionName"`
	// Status is "running" while an invocation is in progress, "idle" between invocations.
	Status    string `json:"status"`
	StartedAt int64  `json:"startedAt"` // UnixMilli — when the instance was first created
	LastUsed  int64  `json:"lastUsed"`  // UnixMilli — when the last invocation completed
	ExpiresAt int64  `json:"expiresAt"` // UnixMilli — lastUsed + 15 min idle TTL
	LogGroup  string `json:"logGroup,omitempty"`
	LogStream string `json:"logStream,omitempty"`
	// LastInvocationStatus reports the last completed invocation outcome:
	// "succeeded" or "failed".
	LastInvocationStatus string `json:"lastInvocationStatus,omitempty"`
	// LastInvocationError carries a brief failure reason when the last
	// invocation failed (e.g. timeout, runtime init/exit error).
	LastInvocationError string `json:"lastInvocationError,omitempty"`
	// TriggerEvent holds the full JSON payload that last triggered this instance.
	TriggerEvent []byte `json:"triggerEvent,omitempty"`
	// MemoryUsedMB and CPUPercent are reserved for future real metrics collection.
	MemoryUsedMB int     `json:"memoryUsedMB"`
	CPUPercent   float64 `json:"cpuPercent"`
}

// LogEventsWrittenPayload carries log events appended via PutLogEvents.
type LogEventsWrittenPayload struct {
	LogGroupName  string         `json:"logGroupName"`
	LogStreamName string         `json:"logStreamName"`
	Events        []LogEventItem `json:"events"`
}

// LogEventItem is a single log line within a LogEventsWrittenPayload.
type LogEventItem struct {
	Timestamp int64  `json:"timestamp"` // UnixMilli
	Message   string `json:"message"`
}

// ErrorPayload is the payload for ServiceError events. It carries enough
// context to identify the problem without requiring log access.
// Service is always set; Operation and Code are optional.
type ErrorPayload struct {
	Service   string `json:"service"`             // originating service name, e.g. "lambda"
	Operation string `json:"operation,omitempty"` // e.g. "Invoke", "CreateFunction"
	Message   string `json:"message"`             // human-readable reason
	Code      string `json:"code,omitempty"`      // AWS error code, if applicable
}

// Event is the envelope published onto the Bus.
// Payload is a typed struct (e.g. S3ObjectPayload); use a type assertion
// in the subscriber after checking the Type.
type Event struct {
	Type    Type
	Time    time.Time
	Source  string // service name: "s3", "sqs", "sns", …
	Payload any
}

// CFNStackPayload carries the details of a CloudFormation stack lifecycle event.
type CFNStackPayload struct {
	StackName   string `json:"stackName"`
	StackID     string `json:"stackId"`
	Status      string `json:"status"`
	Reason      string `json:"reason,omitempty"`
	ChangeSetID string `json:"changeSetId,omitempty"` // for change-set-related events
}

// CFNResourcePayload carries the details of a single CloudFormation resource event.
type CFNResourcePayload struct {
	StackName         string `json:"stackName"`
	LogicalResourceID string `json:"logicalResourceId"`
	ResourceType      string `json:"resourceType"` // e.g. "AWS::S3::Bucket"
	Status            string `json:"status"`
	PhysicalID        string `json:"physicalId,omitempty"`
}

// STSAssumeRolePayload carries the details of a successful STS AssumeRole call.
type STSAssumeRolePayload struct {
	RoleARN     string `json:"roleArn"`
	SessionName string `json:"sessionName"`
}

// DockerContainerPayload carries the details of a Docker container lifecycle
// event received from the Docker daemon event stream. Services subscribe to
// these events to react to container state changes without per-container
// polling goroutines.
type DockerContainerPayload struct {
	ContainerID string `json:"containerId"`
	Action      string `json:"action"`     // "start", "stop", "die", "kill", "oom", "health_status"
	ExitCode    string `json:"exitCode"`   // set on "die" events; from Actor.Attributes["exitCode"]
	Service     string `json:"service"`    // from label overcast.service
	ResourceID  string `json:"resourceId"` // from label overcast.resource-id
	Image       string `json:"image"`
	// Reason is a human-readable explanation of why the container died, populated
	// on "die" events from a post-death ContainerInspect. Empty for non-die events
	// and for clean exits (exit code 0 with no error). Example values: "oom",
	// "exit 137", or a runtime error string from State.Error.
	Reason string `json:"reason,omitempty"`
}

// RequestPayload carries the details of an incoming HTTP request.
// Published on RequestReceived events after the handler returns.
type RequestPayload struct {
	Method        string `json:"method"`
	Path          string `json:"path"`
	Query         string `json:"query,omitempty"`
	Status        int    `json:"status"`
	DurationUs    int64  `json:"durationUs"`
	Service       string `json:"service"`
	Operation     string `json:"operation,omitempty"`
	RequestID     string `json:"requestId,omitempty"`
	RemoteAddr    string `json:"remoteAddr,omitempty"`
	UserAgent     string `json:"userAgent,omitempty"`
	ContentLength int64  `json:"contentLength,omitempty"`
	XAmzTarget    string `json:"xAmzTarget,omitempty"`
}

// DockerNetworkPayload carries the details of a Docker network lifecycle
// event. Services subscribe to these to react to network changes (e.g. EC2
// VPC state tracking).
type DockerNetworkPayload struct {
	NetworkID   string `json:"networkId"`
	Action      string `json:"action"`                // "create", "destroy", "connect", "disconnect"
	Service     string `json:"service"`               // from label overcast.service
	ResourceID  string `json:"resourceId"`            // from label overcast.resource-id
	ContainerID string `json:"containerId,omitempty"` // set on connect/disconnect events
}
