// Package cloudformation_test contains integration tests for the CloudFormation emulator.
//
// Run: go test ./tests/integration/cloudformation/...
package cloudformation_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

// cfnQuery sends a CloudFormation Query protocol request.
func cfnQuery(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2010-05-15")
	body := strings.NewReader(params.Encode())
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("cfnQuery %s: %v", action, err)
	}
	return resp
}

func readBody(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("readBody: %v", err)
	}
	return b
}

func sqsJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, "AmazonSQS.", action, "application/x-amz-json-1.0", body)
}

func cognitoJSONCall(t *testing.T, srv *helpers.TestServer, action string, body map[string]any) *http.Response {
	t.Helper()
	return awsJSONCall(t, srv, "AWSCognitoIdentityProviderService.", action, "application/x-amz-json-1.1", body)
}

func awsJSONCall(t *testing.T, srv *helpers.TestServer, targetPrefix, action, contentType string, body map[string]any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal %s body: %v", action, err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("build %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("X-Amz-Target", targetPrefix+action)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s: %v", action, err)
	}
	return resp
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

const minimalTemplate = `{"AWSTemplateFormatVersion":"2010-09-09","Description":"compat test","Resources":{}}`

// ─── CreateStack ──────────────────────────────────────────────────────────────

func TestCreateStack_success(t *testing.T) {
	// Given: CloudFormation service
	srv := helpers.NewTestServer(t)

	// When: CreateStack is called
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"test-stack"},
		"TemplateBody": []string{minimalTemplate},
	})
	defer resp.Body.Close()

	// Then: 200 with StackId
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "StackId") {
		t.Errorf("expected StackId in response, got: %s", b)
	}
}

// ─── DescribeStacks ───────────────────────────────────────────────────────────

func TestDescribeStacks_success(t *testing.T) {
	// Given: an existing stack
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"my-stack"},
		"TemplateBody": []string{minimalTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: DescribeStacks is called
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"my-stack"},
	})
	defer resp.Body.Close()

	// Then: 200 with Stack element containing StackName
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "my-stack") {
		t.Errorf("expected my-stack in response, got: %s", b)
	}
}

// ─── ListStacks ───────────────────────────────────────────────────────────────

func TestListStacks_success(t *testing.T) {
	// Given: two stacks
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"stack-a", "stack-b"} {
		r := cfnQuery(t, srv, "CreateStack", url.Values{
			"StackName":    []string{name},
			"TemplateBody": []string{minimalTemplate},
		})
		r.Body.Close()
	}

	// When: ListStacks is called
	resp := cfnQuery(t, srv, "ListStacks", nil)
	defer resp.Body.Close()

	// Then: 200 with stack summaries
	helpers.AssertStatus(t, resp, http.StatusOK)
	b := readBody(t, resp)
	if !strings.Contains(string(b), "StackName") {
		t.Errorf("expected StackName in response, got: %s", b)
	}
}

// ─── DeleteStack ──────────────────────────────────────────────────────────────

func TestDeleteStack_success(t *testing.T) {
	// Given: an existing stack
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"to-delete"},
		"TemplateBody": []string{minimalTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// When: DeleteStack is called
	resp := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"to-delete"},
	})
	defer resp.Body.Close()

	// Then: 200
	helpers.AssertStatus(t, resp, http.StatusOK)
}

// ─── Rollback behaviour ───────────────────────────────────────────────────────

// failTemplate references a resource type that will resolve via the stub handler
// (which succeeds), followed by an SQS queue whose RedrivePolicy references a
// non-existent DLQ — the SNS subscription below forces a real provisioning
// failure because the topic ARN is deliberately invalid.
//
// For rollback tests we need a template where at least one resource is created
// successfully before a second resource fails. We use:
//   - ResourceA: S3 bucket (succeeds — stub handler)
//   - ResourceB: SNS Subscription with a fake TopicArn (fails, no DependsOn so
//     ordering is alphabetical A then B)
const rollbackTestTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "BucketA": {
      "Type": "AWS::S3::Bucket",
      "Properties": { "BucketName": "rollback-test-bucket-a" }
    },
    "SubB": {
      "Type": "AWS::SNS::Subscription",
      "DependsOn": "BucketA",
      "Properties": {
        "TopicArn": "arn:aws:sns:us-east-1:000000000000:nonexistent-topic-xyzzy",
        "Protocol": "sqs",
        "Endpoint": "arn:aws:sqs:us-east-1:000000000000:nonexistent-queue"
      }
    }
  }
}`

func waitForStackStatus(t *testing.T, srv *helpers.TestServer, stackName, wantStatus string) {
	t.Helper()
	helpers.Eventually(t, 5*time.Second, 20*time.Millisecond, func() bool {
		resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
			"StackName": []string{stackName},
		})
		defer resp.Body.Close()
		b := readBody(t, resp)
		return strings.Contains(string(b), wantStatus)
	}, "timed out waiting for stack status "+wantStatus)
}

func TestCreateStack_rollsBackOnResourceFailure(t *testing.T) {
	// Given: a CloudFormation service and a template where one resource will fail
	srv := helpers.NewTestServer(t)

	// When: CreateStack is called (without DisableRollback)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"rollback-stack"},
		"TemplateBody": []string{rollbackTestTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: stack eventually reaches ROLLBACK_COMPLETE (not CREATE_FAILED)
	waitForStackStatus(t, srv, "rollback-stack", "ROLLBACK_COMPLETE")

	// And: the S3 bucket created before the failure should have been deleted
	// (verify the bucket does not exist)
	bucketResp, err := http.Get(srv.URL + "/rollback-test-bucket-a")
	if err != nil {
		t.Fatalf("bucket probe: %v", err)
	}
	defer bucketResp.Body.Close()
	if bucketResp.StatusCode == http.StatusOK {
		t.Error("expected bucket to be deleted during rollback, but it still exists (200)")
	}
}

func TestCreateStack_disableRollback_leavesPartialStack(t *testing.T) {
	// Given: a CloudFormation service and a template where one resource will fail
	srv := helpers.NewTestServer(t)

	// When: CreateStack is called with DisableRollback=true
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":       []string{"disabled-rollback-stack"},
		"TemplateBody":    []string{rollbackTestTemplate},
		"DisableRollback": []string{"true"},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: stack reaches CREATE_FAILED (not ROLLBACK_COMPLETE)
	waitForStackStatus(t, srv, "disabled-rollback-stack", "CREATE_FAILED")

	// And: the S3 bucket should still exist (no rollback performed)
	helpers.Eventually(t, 3*time.Second, 20*time.Millisecond, func() bool {
		bucketResp, err := http.Get(srv.URL + "/rollback-test-bucket-a")
		if err != nil {
			return false
		}
		defer bucketResp.Body.Close()
		return bucketResp.StatusCode != http.StatusNotFound
	}, "timed out waiting for bucket to exist after CREATE_FAILED (DisableRollback)")
}

// ─── VPC stack provisioning ──────────────────────────────────────────────────

const vpcStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "VPC with subnet, SG, IGW, route table, and route",
  "Resources": {
    "MyVPC": {
      "Type": "AWS::EC2::VPC",
      "Properties": { "CidrBlock": "10.0.0.0/16" }
    },
    "MySubnet": {
      "Type": "AWS::EC2::Subnet",
      "DependsOn": "MyVPC",
      "Properties": {
        "VpcId": { "Ref": "MyVPC" },
        "CidrBlock": "10.0.1.0/24"
      }
    },
    "MySG": {
      "Type": "AWS::EC2::SecurityGroup",
      "DependsOn": "MyVPC",
      "Properties": {
        "GroupDescription": "Test SG",
        "VpcId": { "Ref": "MyVPC" }
      }
    },
    "MyIGW": {
      "Type": "AWS::EC2::InternetGateway"
    },
    "MyVPCGWAttach": {
      "Type": "AWS::EC2::VPCGatewayAttachment",
      "DependsOn": ["MyVPC", "MyIGW"],
      "Properties": {
        "VpcId": { "Ref": "MyVPC" },
        "InternetGatewayId": { "Ref": "MyIGW" }
      }
    },
    "MyRT": {
      "Type": "AWS::EC2::RouteTable",
      "DependsOn": "MyVPC",
      "Properties": {
        "VpcId": { "Ref": "MyVPC" }
      }
    },
    "MyRoute": {
      "Type": "AWS::EC2::Route",
      "DependsOn": ["MyRT", "MyVPCGWAttach"],
      "Properties": {
        "RouteTableId": { "Ref": "MyRT" },
        "DestinationCidrBlock": "0.0.0.0/0",
        "GatewayId": { "Ref": "MyIGW" }
      }
    },
    "MyRTAssoc": {
      "Type": "AWS::EC2::SubnetRouteTableAssociation",
      "DependsOn": ["MyRT", "MySubnet"],
      "Properties": {
        "RouteTableId": { "Ref": "MyRT" },
        "SubnetId": { "Ref": "MySubnet" }
      }
    }
  }
}`

func TestCreateStack_VPCStack_provisionesRealResources(t *testing.T) {
	// Given: a CloudFormation service
	srv := helpers.NewTestServer(t)

	// When: CreateStack is called with a VPC template
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"vpc-test-stack"},
		"TemplateBody": []string{vpcStackTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: stack reaches CREATE_COMPLETE
	waitForStackStatus(t, srv, "vpc-test-stack", "CREATE_COMPLETE")

	// And: DescribeStackResources shows real physical IDs
	resp := cfnQuery(t, srv, "DescribeStackResources", url.Values{
		"StackName": []string{"vpc-test-stack"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))

	// Verify the VPC has a real vpc-xxxx ID (not stub-)
	if !strings.Contains(body, "vpc-") {
		t.Error("expected VPC physical ID to contain 'vpc-'")
	}
	// Verify the subnet has a real subnet-xxxx ID
	if !strings.Contains(body, "subnet-") {
		t.Error("expected Subnet physical ID to contain 'subnet-'")
	}
	// Verify the security group has a real sg-xxxx ID
	if !strings.Contains(body, "sg-") {
		t.Error("expected SecurityGroup physical ID to contain 'sg-'")
	}
	// Verify the internet gateway has a real igw-xxxx ID
	if !strings.Contains(body, "igw-") {
		t.Error("expected InternetGateway physical ID to contain 'igw-'")
	}
	// Verify the route table has a real rtb-xxxx ID
	if !strings.Contains(body, "rtb-") {
		t.Error("expected RouteTable physical ID to contain 'rtb-'")
	}
}

// ─── ECS Cluster stack provisioning ──────────────────────────────────────────

const ecsClusterTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyCluster": {
      "Type": "AWS::ECS::Cluster",
      "Properties": { "ClusterName": "cfn-test-cluster" }
    }
  }
}`

func TestCreateStack_ECSCluster(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"ecs-test-stack"},
		"TemplateBody": []string{ecsClusterTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	waitForStackStatus(t, srv, "ecs-test-stack", "CREATE_COMPLETE")

	// Verify the cluster was actually created via ECS DescribeClusters.
	resp := cfnQuery(t, srv, "DescribeStackResources", url.Values{
		"StackName": []string{"ecs-test-stack"},
	})
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	if !strings.Contains(body, "arn:aws:ecs:") {
		t.Error("expected ECS cluster ARN in resources")
	}
}

// ─── KMS Key stack provisioning ──────────────────────────────────────────────

const kmsKeyTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyKey": {
      "Type": "AWS::KMS::Key",
      "Properties": { "Description": "cfn-test-key" }
    },
    "MyAlias": {
      "Type": "AWS::KMS::Alias",
      "DependsOn": "MyKey",
      "Properties": {
        "AliasName": "alias/cfn-test-key",
        "TargetKeyId": { "Ref": "MyKey" }
      }
    }
  }
}`

func TestCreateStack_KMSKey(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"kms-test-stack"},
		"TemplateBody": []string{kmsKeyTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	waitForStackStatus(t, srv, "kms-test-stack", "CREATE_COMPLETE")
}

// ─── GetAtt attribute resolution ────────────────────────────────────────────

const getAttTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyBucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": { "BucketName": "getatt-test-bucket" }
    }
  },
  "Outputs": {
    "BucketArn": {
      "Value": { "Fn::GetAtt": ["MyBucket", "Arn"] }
    },
    "BucketDomainName": {
      "Value": { "Fn::GetAtt": ["MyBucket", "DomainName"] }
    }
  }
}`

func TestCreateStack_GetAttResolution(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"getatt-test-stack"},
		"TemplateBody": []string{getAttTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	waitForStackStatus(t, srv, "getatt-test-stack", "CREATE_COMPLETE")

	// Check outputs contain real attribute values.
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"getatt-test-stack"},
	})
	defer resp.Body.Close()
	body := string(readBody(t, resp))

	// BucketArn should be an S3 ARN, not a stub ID.
	if !strings.Contains(body, "arn:aws:s3:::getatt-test-bucket") {
		t.Errorf("expected S3 ARN in output, got: %s", body)
	}
	// DomainName should contain the S3 domain pattern.
	if !strings.Contains(body, "getatt-test-bucket.s3.amazonaws.com") {
		t.Errorf("expected S3 domain name in output, got: %s", body)
	}
}

const sqsQueueOutputTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "EventQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {}
    }
  },
  "Outputs": {
    "EventQueueUrl": {
      "Value": { "Ref": "EventQueue" }
    },
    "EventQueueArn": {
      "Value": { "Fn::GetAtt": ["EventQueue", "Arn"] }
    }
  }
}`

func TestCreateStack_SQSQueueRefOutputIsUsableQueueURL(t *testing.T) {
	// Given: a CloudFormation stack with a generated-name SQS queue.
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"), helpers.WithHostname("localhost.localstack.cloud"))
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"sqs-output-stack"},
		"TemplateBody": []string{sqsQueueOutputTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "sqs-output-stack", "CREATE_COMPLETE")

	// When: stack outputs and SQS APIs are queried from outside CloudFormation.
	desc := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"sqs-output-stack"},
	})
	defer desc.Body.Close()
	body := string(readBody(t, desc))
	queueURL := srv.Config.ExternalBaseURL() + "/000000000000/sqs-output-stack-Queue"

	list := sqsJSONCall(t, srv, "ListQueues", map[string]any{})
	defer list.Body.Close()
	receive := sqsJSONCall(t, srv, "ReceiveMessage", map[string]any{"QueueUrl": queueURL})
	defer receive.Body.Close()

	// Then: Ref resolves to the usable queue URL, GetAtt Arn remains an ARN, and
	// the externally visible SQS APIs operate without internal errors.
	if !strings.Contains(body, queueURL) {
		t.Fatalf("expected SQS Ref output to contain queue URL %q, got: %s", queueURL, body)
	}
	if !strings.Contains(body, "arn:aws:sqs:ap-southeast-2:000000000000:sqs-output-stack-Queue") {
		t.Fatalf("expected SQS Arn output, got: %s", body)
	}
	helpers.AssertStatus(t, list, http.StatusOK)
	var queues struct {
		QueueUrls []string `json:"QueueUrls"`
	}
	helpers.DecodeJSON(t, list, &queues)
	if !contains(queues.QueueUrls, queueURL) {
		t.Fatalf("expected ListQueues to include %q, got %#v", queueURL, queues.QueueUrls)
	}
	helpers.AssertStatus(t, receive, http.StatusOK)
	var received map[string]json.RawMessage
	helpers.DecodeJSON(t, receive, &received)
	if _, ok := received["Messages"]; ok {
		t.Fatalf("expected empty ReceiveMessage response to omit Messages, got %#v", received)
	}
}

const sqsFIFOQueueTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "EventQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"FifoQueue": true}
    }
  },
  "Outputs": {
    "EventQueueUrl": {"Value": {"Ref": "EventQueue"}},
    "EventQueueArn": {"Value": {"Fn::GetAtt": ["EventQueue", "Arn"]}}
  }
}`

func TestCreateStack_SQSFIFOQueueGeneratedName(t *testing.T) {
	// Given: a CloudFormation stack with a generated-name FIFO SQS queue.
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"), helpers.WithHostname("localhost.localstack.cloud"))
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"sqs-fifo-stack"},
		"TemplateBody": []string{sqsFIFOQueueTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "sqs-fifo-stack", "CREATE_COMPLETE")
	queueURL := srv.Config.ExternalBaseURL() + "/000000000000/sqs-fifo-stack-Queue.fifo"

	// When: external SQS APIs inspect the created queue.
	attrs := sqsJSONCall(t, srv, "GetQueueAttributes", map[string]any{
		"QueueUrl":       queueURL,
		"AttributeNames": []string{"All"},
	})
	defer attrs.Body.Close()
	receive := sqsJSONCall(t, srv, "ReceiveMessage", map[string]any{
		"QueueUrl":        queueURL,
		"WaitTimeSeconds": 0,
	})
	defer receive.Body.Close()

	// Then: the generated physical name is FIFO-compatible and the queue behaves
	// like a FIFO queue from outside CloudFormation.
	helpers.AssertStatus(t, attrs, http.StatusOK)
	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	helpers.DecodeJSON(t, attrs, &result)
	if result.Attributes["FifoQueue"] != "true" {
		t.Fatalf("expected FifoQueue=true, got %#v", result.Attributes)
	}
	if !strings.Contains(result.Attributes["QueueArn"], ":sqs-fifo-stack-Queue.fifo") {
		t.Fatalf("expected FIFO queue ARN, got %#v", result.Attributes)
	}
	helpers.AssertStatus(t, receive, http.StatusOK)
}

const cognitoUserPoolTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "SmsRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Principal": {"Service": "cognito-idp.amazonaws.com"}, "Action": "sts:AssumeRole"}]
        }
      }
    },
    "UserPool": {
      "Type": "AWS::Cognito::UserPool",
      "Properties": {
        "UserPoolName": "identity-user-pool",
        "UsernameAttributes": ["email"],
        "Schema": [{"Name": "email", "Required": true, "Mutable": true}],
        "MfaConfiguration": "OPTIONAL",
        "EnabledMfas": ["SMS_MFA", "SOFTWARE_TOKEN_MFA"],
        "SmsConfiguration": {"SnsCallerArn": {"Fn::GetAtt": ["SmsRole", "Arn"]}}
      }
    },
    "UserPoolClient": {
      "Type": "AWS::Cognito::UserPoolClient",
      "Properties": {
        "UserPoolId": {"Ref": "UserPool"},
        "ClientName": "identity-service-client"
      }
    }
  },
  "Outputs": {
    "UserPoolId": {"Value": {"Ref": "UserPool"}},
    "UserPoolClientId": {"Value": {"Ref": "UserPoolClient"}}
  }
}`

func TestCreateStack_CognitoUserPoolApisAfterCDKLikeDeploy(t *testing.T) {
	// Given: a CDK-like Cognito stack with username attributes and MFA settings.
	srv := helpers.NewTestServer(t, helpers.WithRegion("ap-southeast-2"))
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"identity-data-stack"},
		"TemplateBody": []string{cognitoUserPoolTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "identity-data-stack", "CREATE_COMPLETE")

	// When: external Cognito APIs use the CloudFormation-created resources.
	listPools := cognitoJSONCall(t, srv, "ListUserPools", map[string]any{"MaxResults": 10})
	defer listPools.Body.Close()

	// Then: ListUserPools exposes the deployed pool instead of returning InternalError.
	helpers.AssertStatus(t, listPools, http.StatusOK)
	var pools struct {
		UserPools []struct {
			Id string `json:"Id"`
		} `json:"UserPools"`
	}
	helpers.DecodeJSON(t, listPools, &pools)
	if len(pools.UserPools) != 1 || pools.UserPools[0].Id == "" {
		t.Fatalf("expected one Cognito user pool, got %#v", pools.UserPools)
	}
	poolID := pools.UserPools[0].Id

	describe := cognitoJSONCall(t, srv, "DescribeUserPool", map[string]any{"UserPoolId": poolID})
	defer describe.Body.Close()
	clients := cognitoJSONCall(t, srv, "ListUserPoolClients", map[string]any{"UserPoolId": poolID, "MaxResults": 10})
	defer clients.Body.Close()
	createUser := cognitoJSONCall(t, srv, "AdminCreateUser", map[string]any{
		"UserPoolId":    poolID,
		"Username":      "overcast-repro@example.test",
		"MessageAction": "SUPPRESS",
		"UserAttributes": []map[string]string{
			{"Name": "email", "Value": "overcast-repro@example.test"},
		},
	})
	defer createUser.Body.Close()

	helpers.AssertStatus(t, describe, http.StatusOK)
	helpers.AssertStatus(t, clients, http.StatusOK)
	helpers.AssertStatus(t, createUser, http.StatusOK)
}

// ─── DeleteStack cleans up EC2 resources ────────────────────────────────────

const simpleVpcTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "TestVPC": {
      "Type": "AWS::EC2::VPC",
      "Properties": { "CidrBlock": "10.99.0.0/16" }
    }
  }
}`

func TestDeleteStack_cleansUpEC2Resources(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// Create a stack with a VPC.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"delete-vpc-stack"},
		"TemplateBody": []string{simpleVpcTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "delete-vpc-stack", "CREATE_COMPLETE")

	// Delete the stack.
	dr := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"delete-vpc-stack"},
	})
	defer dr.Body.Close()
	helpers.AssertStatus(t, dr, http.StatusOK)
	waitForStackStatus(t, srv, "delete-vpc-stack", "DELETE_COMPLETE")
}

// ─── DescribeStackEvents — event history ────────────────────────────────────

func TestDescribeStackEvents_successfulCreate_hasFullHistory(t *testing.T) {
	// Given: a stack that reaches CREATE_COMPLETE
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"events-test-stack"},
		"TemplateBody": []string{minimalTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "events-test-stack", "CREATE_COMPLETE")

	// When: DescribeStackEvents is called
	resp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"events-test-stack"},
	})
	defer resp.Body.Close()

	// Then: events are present, newest-first
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if !strings.Contains(body, "CREATE_COMPLETE") {
		t.Errorf("expected CREATE_COMPLETE event, got: %s", body)
	}
	if !strings.Contains(body, "CREATE_IN_PROGRESS") {
		t.Errorf("expected CREATE_IN_PROGRESS event, got: %s", body)
	}
	// Newest-first: CREATE_COMPLETE must appear before CREATE_IN_PROGRESS.
	completeIdx := strings.Index(body, "CREATE_COMPLETE")
	inProgressIdx := strings.LastIndex(body, "CREATE_IN_PROGRESS")
	if completeIdx > inProgressIdx {
		t.Error("expected events to be ordered newest-first (CREATE_COMPLETE before CREATE_IN_PROGRESS)")
	}
	// EventId must be stable UUIDs (not regenerated each call). Strip the
	// per-request RequestId from both bodies before comparing.
	resp2 := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"events-test-stack"},
	})
	defer resp2.Body.Close()
	body2 := string(readBody(t, resp2))
	stripRequestID := func(s string) string {
		// Remove the RequestId element which is legitimately unique per call.
		start := strings.Index(s, "<RequestId>")
		end := strings.Index(s, "</RequestId>")
		if start < 0 || end < 0 {
			return s
		}
		return s[:start] + s[end+len("</RequestId>"):]
	}
	if stripRequestID(body) != stripRequestID(body2) {
		t.Errorf("expected DescribeStackEvents to return stable EventIds across calls.\nFirst:  %s\nSecond: %s", body, body2)
	}
}

func TestDescribeStackEvents_rollback_hasReasonAndDeleteEvents(t *testing.T) {
	// Given: a stack that rolls back due to a resource failure
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"rollback-events-stack"},
		"TemplateBody": []string{rollbackTestTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "rollback-events-stack", "ROLLBACK_COMPLETE")

	// When: DescribeStackEvents is called
	resp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"rollback-events-stack"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))

	// Then: the failure and rollback events are all present
	for _, want := range []string{
		"CREATE_IN_PROGRESS",
		"CREATE_FAILED",
		"ROLLBACK_IN_PROGRESS",
		"DELETE_IN_PROGRESS",
		"DELETE_COMPLETE",
		"ROLLBACK_COMPLETE",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("expected %q in events, got: %s", want, body)
		}
	}
	// The CREATE_FAILED event must include a ResourceStatusReason.
	if !strings.Contains(body, "ResourceStatusReason") {
		t.Errorf("expected ResourceStatusReason in failed event, got: %s", body)
	}
}

func TestDescribeStackEvents_pagination_nextTokenWorks(t *testing.T) {
	// Given: a stack with 11 S3 buckets.
	// Each resource emits CREATE_IN_PROGRESS + CREATE_COMPLETE (22 resource events)
	// plus CREATE_IN_PROGRESS + CREATE_COMPLETE for the stack itself = 24 events total,
	// which exceeds the page size of 20 and forces NextToken.
	const bigTemplate = `{
	  "Resources": {
	    "B01":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-01"}},
	    "B02":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-02"}},
	    "B03":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-03"}},
	    "B04":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-04"}},
	    "B05":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-05"}},
	    "B06":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-06"}},
	    "B07":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-07"}},
	    "B08":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-08"}},
	    "B09":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-09"}},
	    "B10":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-10"}},
	    "B11":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"pg-bucket-11"}}
	  }
	}`
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"pagination-test-stack"},
		"TemplateBody": []string{bigTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "pagination-test-stack", "CREATE_COMPLETE")

	// When: first page is fetched (no NextToken)
	resp1 := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"pagination-test-stack"},
	})
	defer resp1.Body.Close()
	helpers.AssertStatus(t, resp1, http.StatusOK)
	body1 := string(readBody(t, resp1))

	// Then: NextToken is present (there are more events beyond the first page)
	if !strings.Contains(body1, "<NextToken>") {
		t.Fatalf("expected NextToken in first page response (>20 events), got:\n%s", body1)
	}

	// Extract NextToken value
	start := strings.Index(body1, "<NextToken>") + len("<NextToken>")
	end := strings.Index(body1, "</NextToken>")
	nextToken := body1[start:end]

	// When: second page is fetched using the NextToken
	resp2 := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"pagination-test-stack"},
		"NextToken": []string{nextToken},
	})
	defer resp2.Body.Close()
	helpers.AssertStatus(t, resp2, http.StatusOK)
	body2 := string(readBody(t, resp2))

	// Then: no NextToken on the last page
	if strings.Contains(body2, "<NextToken>") {
		t.Errorf("expected no NextToken on last page, got:\n%s", body2)
	}

	// And: the full event lifecycle is represented across both pages
	combined := body1 + body2
	for _, want := range []string{"CREATE_IN_PROGRESS", "CREATE_COMPLETE"} {
		if !strings.Contains(combined, want) {
			t.Errorf("expected %q across all pages, not found", want)
		}
	}
}

// ─── DynamoDB Streams + Lambda ESM ────────────────────────────────────────────

const dynamoStreamESMTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyTable": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "cfn-stream-esm-test-table",
        "BillingMode": "PAY_PER_REQUEST",
        "AttributeDefinitions": [
          { "AttributeName": "id", "AttributeType": "S" }
        ],
        "KeySchema": [
          { "AttributeName": "id", "KeyType": "HASH" }
        ],
        "StreamSpecification": {
          "StreamViewType": "NEW_AND_OLD_IMAGES"
        }
      }
    },
    "ExecRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-stream-esm-test-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{ "Effect": "Allow", "Principal": { "Service": "lambda.amazonaws.com" }, "Action": "sts:AssumeRole" }]
        }
      }
    },
    "MyFunction": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": "ExecRole",
      "Properties": {
        "FunctionName": "cfn-stream-esm-test-fn",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": { "Fn::GetAtt": ["ExecRole", "Arn"] },
        "Code": { "ZipFile": "def handler(e, c): return {}" }
      }
    },
    "StreamESM": {
      "Type": "AWS::Lambda::EventSourceMapping",
      "Properties": {
        "EventSourceArn": { "Fn::GetAtt": ["MyTable", "StreamArn"] },
        "FunctionName":   { "Fn::GetAtt": ["MyFunction", "Arn"] },
        "StartingPosition": "TRIM_HORIZON",
        "BatchSize": 10
      }
    }
  }
}`

func TestCreateStack_DynamoDBStreamESM(t *testing.T) {
	// Given: a stack with a DynamoDB table that has streams enabled,
	//        a Lambda function, and an EventSourceMapping pointing to the stream.

	srv := helpers.NewTestServer(t)

	// When: the stack is created
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"dynamo-stream-esm-stack"},
		"TemplateBody": []string{dynamoStreamESMTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: stack reaches CREATE_COMPLETE (not CREATE_FAILED)
	waitForStackStatus(t, srv, "dynamo-stream-esm-stack", "CREATE_COMPLETE")

	// Verify stack status is actually CREATE_COMPLETE (not a different terminal state).
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"dynamo-stream-esm-stack"},
	})
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	if !strings.Contains(body, "CREATE_COMPLETE") {
		t.Errorf("expected CREATE_COMPLETE, got: %s", body)
	}

	// And: the ESM should actually exist and be wired correctly.
	esmResp, err := http.Get(srv.URL + "/2015-03-31/event-source-mappings/")
	if err != nil {
		t.Fatalf("ListEventSourceMappings: %v", err)
	}
	defer esmResp.Body.Close()
	helpers.AssertStatus(t, esmResp, http.StatusOK)

	var esmList struct {
		EventSourceMappings []struct {
			UUID             string  `json:"UUID"`
			EventSourceArn   string  `json:"EventSourceArn"`
			FunctionArn      string  `json:"FunctionArn"`
			State            string  `json:"State"`
			BatchSize        float64 `json:"BatchSize"`
			StartingPosition string  `json:"StartingPosition"`
		} `json:"EventSourceMappings"`
	}
	if err := json.NewDecoder(esmResp.Body).Decode(&esmList); err != nil {
		t.Fatalf("decode ESM list: %v", err)
	}

	if len(esmList.EventSourceMappings) != 1 {
		t.Fatalf("expected 1 ESM, got %d", len(esmList.EventSourceMappings))
	}

	esm := esmList.EventSourceMappings[0]
	if esm.UUID == "" {
		t.Error("expected ESM UUID to be set")
	}
	if !strings.Contains(esm.EventSourceArn, "cfn-stream-esm-test-table/stream/") {
		t.Errorf("EventSourceArn should reference the DynamoDB stream, got: %s", esm.EventSourceArn)
	}
	if !strings.Contains(esm.FunctionArn, "cfn-stream-esm-test-fn") {
		t.Errorf("FunctionArn should reference the Lambda function, got: %s", esm.FunctionArn)
	}
	if esm.State != "Enabled" {
		t.Errorf("State: got %s, want Enabled", esm.State)
	}
	if esm.StartingPosition != "TRIM_HORIZON" {
		t.Errorf("StartingPosition: got %s, want TRIM_HORIZON", esm.StartingPosition)
	}
}

// ─── Cross-stack references (ListExports / Fn::ImportValue) ───────────────────

const exporterTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyBucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": { "BucketName": "cross-stack-export-bucket" }
    }
  },
  "Outputs": {
    "BucketNameOut": {
      "Value": { "Ref": "MyBucket" },
      "Export": { "Name": "SharedBucketName" }
    },
    "BucketArnOut": {
      "Value": { "Fn::GetAtt": ["MyBucket", "Arn"] },
      "Export": { "Name": "SharedBucketArn" }
    },
    "NoExportOut": {
      "Value": "no-export-value"
    }
  }
}`

func TestListExports_returnsExportedValues(t *testing.T) {
	// Given: a stack with exported outputs
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"exporter-stack"},
		"TemplateBody": []string{exporterTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "exporter-stack", "CREATE_COMPLETE")

	// When: ListExports is called
	resp := cfnQuery(t, srv, "ListExports", nil)
	defer resp.Body.Close()

	// Then: 200 with both exported values
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if !strings.Contains(body, "SharedBucketName") {
		t.Errorf("expected SharedBucketName export, got: %s", body)
	}
	if !strings.Contains(body, "SharedBucketArn") {
		t.Errorf("expected SharedBucketArn export, got: %s", body)
	}
	if !strings.Contains(body, "cross-stack-export-bucket") {
		t.Errorf("expected bucket name value in export, got: %s", body)
	}
	// Non-exported output should NOT appear
	if strings.Contains(body, "NoExportOut") {
		t.Errorf("non-exported output should not appear in ListExports, got: %s", body)
	}
}

const importerTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {
        "QueueName": { "Fn::ImportValue": "SharedBucketName" }
      }
    }
  },
  "Outputs": {
    "ImportedValue": {
      "Value": { "Fn::ImportValue": "SharedBucketName" }
    }
  }
}`

func TestImportValue_resolvesExportFromAnotherStack(t *testing.T) {
	// Given: an exporter stack with exported outputs
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"exporter-stack-2"},
		"TemplateBody": []string{exporterTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "exporter-stack-2", "CREATE_COMPLETE")

	// When: an importer stack references the export via Fn::ImportValue
	cr2 := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"importer-stack"},
		"TemplateBody": []string{importerTemplate},
	})
	defer cr2.Body.Close()
	helpers.AssertStatus(t, cr2, http.StatusOK)

	// Then: importer stack completes successfully
	waitForStackStatus(t, srv, "importer-stack", "CREATE_COMPLETE")

	// And: the output contains the resolved imported value
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"importer-stack"},
	})
	defer resp.Body.Close()
	body := string(readBody(t, resp))
	if !strings.Contains(body, "cross-stack-export-bucket") {
		t.Errorf("expected imported value 'cross-stack-export-bucket' in importer stack outputs, got: %s", body)
	}
}

func TestListImports_returnsImportingStacks(t *testing.T) {
	// Given: an exporter stack and an importer stack
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"exp-for-imports"},
		"TemplateBody": []string{exporterTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "exp-for-imports", "CREATE_COMPLETE")

	cr2 := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"imp-for-imports"},
		"TemplateBody": []string{importerTemplate},
	})
	defer cr2.Body.Close()
	helpers.AssertStatus(t, cr2, http.StatusOK)
	waitForStackStatus(t, srv, "imp-for-imports", "CREATE_COMPLETE")

	// When: ListImports is called for the SharedBucketName export
	resp := cfnQuery(t, srv, "ListImports", url.Values{
		"ExportName": []string{"SharedBucketName"},
	})
	defer resp.Body.Close()

	// Then: 200 with the importing stack name
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if !strings.Contains(body, "imp-for-imports") {
		t.Errorf("expected importing stack name in ListImports response, got: %s", body)
	}
}

func TestListExports_deletedStackExportsNotReturned(t *testing.T) {
	// Given: a stack with exports that is then deleted
	srv := helpers.NewTestServer(t)
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"del-export-stack"},
		"TemplateBody": []string{exporterTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "del-export-stack", "CREATE_COMPLETE")

	// Delete the stack
	dr := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"del-export-stack"},
	})
	defer dr.Body.Close()
	helpers.AssertStatus(t, dr, http.StatusOK)
	waitForStackStatus(t, srv, "del-export-stack", "DELETE_COMPLETE")

	// When: ListExports is called
	resp := cfnQuery(t, srv, "ListExports", nil)
	defer resp.Body.Close()

	// Then: no exports from deleted stack
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := string(readBody(t, resp))
	if strings.Contains(body, "SharedBucketName") {
		t.Errorf("expected no exports from deleted stack, got: %s", body)
	}
}

// ─── Custom resource invocation ───────────────────────────────────────────────

// customResourceTemplate creates a Lambda function and a Custom::TestResource
// that references the Lambda as its ServiceToken. The Lambda handler returns a
// PhysicalResourceId and Data map so we can verify Fn::GetAtt resolution.
const customResourceTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ExecRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "custom-res-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]
        }
      }
    },
    "Handler": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": "ExecRole",
      "Properties": {
        "FunctionName": "cfn-custom-handler",
        "Runtime": "nodejs20.x",
        "Handler": "index.handler",
        "Role": {"Fn::GetAtt": ["ExecRole", "Arn"]},
        "Code": {
          "ZipFile": "exports.handler = async (event) => { return { Status: 'SUCCESS', PhysicalResourceId: 'custom-phys-' + event.LogicalResourceId, Data: { OutputKey: 'hello-from-custom' } }; };"
        }
      }
    },
    "MyCustom": {
      "Type": "Custom::TestResource",
      "DependsOn": "Handler",
      "Properties": {
        "ServiceToken": {"Fn::GetAtt": ["Handler", "Arn"]},
        "CustomProp": "custom-value"
      }
    }
  },
  "Outputs": {
    "CustomOutput": {
      "Value": {"Fn::GetAtt": ["MyCustom", "OutputKey"]}
    },
    "CustomPhysId": {
      "Value": {"Ref": "MyCustom"}
    }
  }
}`

func TestCreateStack_customResource_invokesLambda(t *testing.T) {
	// Given: a stack template with a Lambda-backed custom resource
	srv := helpers.NewTestServer(t)

	// When: CreateStack is called
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"custom-res-stack"},
		"TemplateBody": []string{customResourceTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: stack reaches CREATE_COMPLETE — the custom resource handler is
	// exercised regardless of whether Docker is available. Without Docker,
	// the handler degrades gracefully (stub physical ID); with Docker, the
	// Lambda returns real Data and PhysicalResourceId.
	waitForStackStatus(t, srv, "custom-res-stack", "CREATE_COMPLETE")

	// And: stack resources include the custom resource (not an "unknown type" stub)
	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"custom-res-stack"},
	})
	defer resp.Body.Close()
	body := string(readBody(t, resp))

	// The custom resource's Ref (physical ID) should appear in Outputs.
	// With Docker: "custom-phys-MyCustom" (from Lambda response).
	// Without Docker: "custom-resource-custom-res-stack-<N>" (stub fallback).
	if !strings.Contains(body, "custom-phys-MyCustom") && !strings.Contains(body, "custom-resource-custom-res-stack") {
		t.Errorf("expected custom resource physical ID in output (real or stub), got: %s", body)
	}
}

// ─── Nested stacks ────────────────────────────────────────────────────────────

const nestedChildTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Parameters": {
    "BucketSuffix": {
      "Type": "String",
      "Default": "default-suffix"
    }
  },
  "Resources": {
    "ChildBucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "BucketName": {"Fn::Sub": "nested-child-${BucketSuffix}"}
      }
    }
  },
  "Outputs": {
    "ChildBucketName": {
      "Value": {"Ref": "ChildBucket"}
    }
  }
}`

func TestCreateStack_nestedStack_provisionesChildResources(t *testing.T) {
	// Given: a child template uploaded to S3
	srv := helpers.NewTestServer(t)

	// Create the S3 bucket for templates
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/nested-template-bucket", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	resp.Body.Close()

	// Upload the child template
	req, _ = http.NewRequest(http.MethodPut,
		srv.URL+"/nested-template-bucket/child.json",
		strings.NewReader(nestedChildTemplate))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload template: %v", err)
	}
	resp.Body.Close()

	// When: CreateStack is called with a parent template referencing the nested stack
	parentTemplate := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ChildStack": {
      "Type": "AWS::CloudFormation::Stack",
      "Properties": {
        "TemplateURL": "` + srv.URL + `/nested-template-bucket/child.json",
        "Parameters": {
          "BucketSuffix": "from-parent"
        }
      }
    }
  },
  "Outputs": {
    "NestedBucketName": {
      "Value": {"Fn::GetAtt": ["ChildStack", "Outputs.ChildBucketName"]}
    }
  }
}`

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"parent-stack"},
		"TemplateBody": []string{parentTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: parent stack reaches CREATE_COMPLETE
	waitForStackStatus(t, srv, "parent-stack", "CREATE_COMPLETE")

	// And: the child bucket was actually created in S3
	bucketResp, err := http.Get(srv.URL + "/nested-child-from-parent")
	if err != nil {
		t.Fatalf("child bucket probe: %v", err)
	}
	defer bucketResp.Body.Close()
	if bucketResp.StatusCode == http.StatusNotFound {
		t.Error("expected child bucket to exist after nested stack provisioning")
	}

	// And: the parent stack output resolves the nested stack output
	descResp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"parent-stack"},
	})
	defer descResp.Body.Close()
	body := string(readBody(t, descResp))
	if !strings.Contains(body, "nested-child-from-parent") {
		t.Errorf("expected nested stack output 'nested-child-from-parent' in parent outputs, got: %s", body)
	}
}

func TestDeleteStack_nestedStack_deletesChildResources(t *testing.T) {
	// Given: a parent stack with a nested child stack
	srv := helpers.NewTestServer(t)

	// Create bucket and upload child template
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/del-nested-tmpl-bucket", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	resp.Body.Close()

	req, _ = http.NewRequest(http.MethodPut,
		srv.URL+"/del-nested-tmpl-bucket/child.json",
		strings.NewReader(nestedChildTemplate))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload template: %v", err)
	}
	resp.Body.Close()

	parentTemplate := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ChildStack": {
      "Type": "AWS::CloudFormation::Stack",
      "Properties": {
        "TemplateURL": "` + srv.URL + `/del-nested-tmpl-bucket/child.json",
        "Parameters": {
          "BucketSuffix": "del-test"
        }
      }
    }
  }
}`

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"del-parent-stack"},
		"TemplateBody": []string{parentTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "del-parent-stack", "CREATE_COMPLETE")

	// The child bucket should exist
	bucketResp, err := http.Get(srv.URL + "/nested-child-del-test")
	if err != nil {
		t.Fatalf("bucket probe: %v", err)
	}
	bucketResp.Body.Close()
	if bucketResp.StatusCode == http.StatusNotFound {
		t.Fatal("expected child bucket to exist before deletion")
	}

	// When: DeleteStack is called on the parent
	dr := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"del-parent-stack"},
	})
	defer dr.Body.Close()
	helpers.AssertStatus(t, dr, http.StatusOK)
	waitForStackStatus(t, srv, "del-parent-stack", "DELETE_COMPLETE")

	// Then: the child bucket should be deleted
	helpers.Eventually(t, 3*time.Second, 20*time.Millisecond, func() bool {
		br, err := http.Get(srv.URL + "/nested-child-del-test")
		if err != nil {
			return false
		}
		defer br.Body.Close()
		return br.StatusCode == http.StatusNotFound
	}, "timed out waiting for child bucket deletion after nested stack delete")
}

func TestNestedStack_parentIdAndRootId(t *testing.T) {
	// Given: a parent stack with a nested child stack
	srv := helpers.NewTestServer(t)

	// Create bucket and upload child template
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/parentid-tmpl-bucket", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	resp.Body.Close()

	req, _ = http.NewRequest(http.MethodPut,
		srv.URL+"/parentid-tmpl-bucket/child.json",
		strings.NewReader(nestedChildTemplate))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload template: %v", err)
	}
	resp.Body.Close()

	parentTemplate := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ChildStack": {
      "Type": "AWS::CloudFormation::Stack",
      "Properties": {
        "TemplateURL": "` + srv.URL + `/parentid-tmpl-bucket/child.json",
        "Parameters": {
          "BucketSuffix": "parentid-test"
        }
      }
    }
  }
}`

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"root-parent"},
		"TemplateBody": []string{parentTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "root-parent", "CREATE_COMPLETE")

	// When: DescribeStacks is called on the parent
	descParent := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"root-parent"},
	})
	defer descParent.Body.Close()
	parentBody := string(readBody(t, descParent))

	// Then: parent should NOT have ParentId or RootId
	if strings.Contains(parentBody, "<ParentId>") {
		t.Error("parent stack should not have a ParentId")
	}
	if strings.Contains(parentBody, "<RootId>") {
		t.Error("parent stack should not have a RootId")
	}

	// Extract the parent's StackId for comparison
	parentStackID := extractXML(parentBody, "StackId")
	if parentStackID == "" {
		t.Fatal("could not extract parent StackId from response")
	}

	// When: ListStacks is called and we find the child stack
	listResp := cfnQuery(t, srv, "ListStacks", url.Values{})
	defer listResp.Body.Close()
	listBody := string(readBody(t, listResp))

	// The child stack name follows the pattern: root-parent-NestedStack-<8chars>
	// Find it in the ListStacks response.
	childName := ""
	for _, line := range strings.Split(listBody, "<StackName>") {
		if idx := strings.Index(line, "</StackName>"); idx > 0 {
			name := line[:idx]
			if strings.HasPrefix(name, "root-parent-NestedStack-") {
				childName = name
				break
			}
		}
	}
	if childName == "" {
		t.Fatalf("could not find child stack in ListStacks response:\n%s", listBody)
	}

	// Then: the child stack in ListStacks should have ParentId
	if !strings.Contains(listBody, "<ParentId>") {
		t.Error("child stack in ListStacks should have ParentId")
	}

	// When: DescribeStacks is called on the child
	descChild := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{childName},
	})
	defer descChild.Body.Close()
	childBody := string(readBody(t, descChild))

	// Then: child should have ParentId matching parent's StackId
	childParentID := extractXML(childBody, "ParentId")
	if childParentID != parentStackID {
		t.Errorf("child ParentId = %q, want parent StackId %q", childParentID, parentStackID)
	}

	// And: child should have RootId matching parent's StackId (parent is the root)
	childRootID := extractXML(childBody, "RootId")
	if childRootID != parentStackID {
		t.Errorf("child RootId = %q, want parent StackId %q", childRootID, parentStackID)
	}
}

// extractXML extracts the text content of an XML element by tag name (simple, non-nested).
func extractXML(body, tag string) string {
	open := "<" + tag + ">"
	close := "</" + tag + ">"
	i := strings.Index(body, open)
	if i < 0 {
		return ""
	}
	rest := body[i+len(open):]
	j := strings.Index(rest, close)
	if j < 0 {
		return ""
	}
	return rest[:j]
}

// ─── 5-level nested stacks (web-UI / topology stress test) ───────────────────

func TestNestedStack_fiveLevelDeep(t *testing.T) {
	// Given: a 5-level deep nesting chain — L1 → L2 → L3 → L4 → L5
	// Each level creates a rich set of resources so the topology view
	// is visually interesting when displayed in the web UI.
	srv := helpers.NewTestServer(t)

	// Create template bucket in S3.
	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/deep-nest-templates", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create template bucket: %v", err)
	}
	resp.Body.Close()

	// L5 (leaf) — "Application Layer": S3 bucket, SQS queue, DynamoDB table, SNS topic, KMS key.
	l5Template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "Level 5 - Application Layer",
  "Parameters": {
    "LeafSuffix": { "Type": "String", "Default": "deep" }
  },
  "Resources": {
    "AppBucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "BucketName": {"Fn::Sub": "deep-leaf-${LeafSuffix}"}
      }
    },
    "AppEventQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {
        "QueueName": {"Fn::Sub": "app-events-${LeafSuffix}"}
      }
    },
    "AppDeadLetterQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {
        "QueueName": {"Fn::Sub": "app-dlq-${LeafSuffix}"}
      }
    },
    "AppTable": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": {"Fn::Sub": "app-sessions-${LeafSuffix}"},
        "AttributeDefinitions": [{"AttributeName": "sessionId", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "sessionId", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST"
      }
    },
    "AppNotifications": {
      "Type": "AWS::SNS::Topic",
      "Properties": { "TopicName": {"Fn::Sub": "app-notifications-${LeafSuffix}"} }
    },
    "AppEncryptionKey": {
      "Type": "AWS::KMS::Key",
      "Properties": {
        "Description": {"Fn::Sub": "App encryption key ${LeafSuffix}"},
        "KeyPolicy": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Principal": {"AWS": "*"}, "Action": "kms:*", "Resource": "*"}]
        }
      }
    },
    "AppLogs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": { "LogGroupName": {"Fn::Sub": "/app/${LeafSuffix}/logs"} }
    }
  },
  "Outputs": {
    "AppBucketName": { "Value": {"Ref": "AppBucket"} },
    "AppTableName":  { "Value": {"Ref": "AppTable"} }
  }
}`

	// L4 — "Compute Layer": IAM role, Lambda, Step Functions, SQS, Log group.
	l4Template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "Level 4 - Compute Layer",
  "Parameters": {
    "LeafSuffix": { "Type": "String", "Default": "deep" }
  },
  "Resources": {
    "ComputeRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "deep-compute-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{
            "Effect": "Allow",
            "Principal": {"Service": "lambda.amazonaws.com"},
            "Action": "sts:AssumeRole"
          }]
        }
      }
    },
    "ProcessorFunction": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "deep-processor",
        "Runtime": "python3.12",
        "Handler": "index.handler",
        "Role": {"Fn::GetAtt": ["ComputeRole", "Arn"]},
        "Code": { "ZipFile": "def handler(event, context): return {'statusCode': 200}" }
      }
    },
    "TransformFunction": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "deep-transformer",
        "Runtime": "python3.12",
        "Handler": "index.handler",
        "Role": {"Fn::GetAtt": ["ComputeRole", "Arn"]},
        "Code": { "ZipFile": "def handler(event, context): return event" }
      }
    },
    "Workflow": {
      "Type": "AWS::StepFunctions::StateMachine",
      "Properties": {
        "StateMachineName": "deep-data-pipeline",
        "RoleArn": {"Fn::GetAtt": ["ComputeRole", "Arn"]},
        "DefinitionString": "{\"StartAt\":\"Process\",\"States\":{\"Process\":{\"Type\":\"Pass\",\"End\":true}}}"
      }
    },
    "TaskQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "deep-task-queue" }
    },
    "RetryQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "deep-retry-queue" }
    },
    "ComputeLogs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": { "LogGroupName": "/compute/deep/processor" }
    },
    "Level5": {
      "Type": "AWS::CloudFormation::Stack",
      "Properties": {
        "TemplateURL": "` + srv.URL + `/deep-nest-templates/l5.json",
        "Parameters": { "LeafSuffix": {"Ref": "LeafSuffix"} }
      }
    }
  },
  "Outputs": {
    "AppBucketName": { "Value": {"Fn::GetAtt": ["Level5", "Outputs.AppBucketName"]} }
  }
}`

	// L3 — "Data Layer": DynamoDB tables, S3, SecretsManager, SSM parameter, Log group.
	l3Template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "Level 3 - Data Layer",
  "Parameters": {
    "LeafSuffix": { "Type": "String", "Default": "deep" }
  },
  "Resources": {
    "UsersTable": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "deep-users",
        "AttributeDefinitions": [{"AttributeName": "userId", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "userId", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST"
      }
    },
    "OrdersTable": {
      "Type": "AWS::DynamoDB::Table",
      "Properties": {
        "TableName": "deep-orders",
        "AttributeDefinitions": [{"AttributeName": "orderId", "AttributeType": "S"}],
        "KeySchema": [{"AttributeName": "orderId", "KeyType": "HASH"}],
        "BillingMode": "PAY_PER_REQUEST"
      }
    },
    "DataLakeBucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": { "BucketName": "deep-data-lake" }
    },
    "DatabaseSecret": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "deep/database/credentials",
        "SecretString": "{\"username\":\"admin\",\"password\":\"changeme\"}"
      }
    },
    "ApiKeySecret": {
      "Type": "AWS::SecretsManager::Secret",
      "Properties": {
        "Name": "deep/api/key",
        "SecretString": "sk-test-not-real-key"
      }
    },
    "DbConnectionParam": {
      "Type": "AWS::SSM::Parameter",
      "Properties": {
        "Name": "/deep/database/connection-string",
        "Type": "String",
        "Value": "postgresql://localhost:5432/deepdb"
      }
    },
    "DataLogs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": { "LogGroupName": "/data/deep/access" }
    },
    "Level4": {
      "Type": "AWS::CloudFormation::Stack",
      "Properties": {
        "TemplateURL": "` + srv.URL + `/deep-nest-templates/l4.json",
        "Parameters": { "LeafSuffix": {"Ref": "LeafSuffix"} }
      }
    }
  },
  "Outputs": {
    "AppBucketName": { "Value": {"Fn::GetAtt": ["Level4", "Outputs.AppBucketName"]} }
  }
}`

	// L2 — "Messaging & Events Layer": SNS topics, SQS queues, subscription, EventBridge.
	l2Template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "Level 2 - Messaging and Events Layer",
  "Parameters": {
    "LeafSuffix": { "Type": "String", "Default": "deep" }
  },
  "Resources": {
    "OrderEventsTopic": {
      "Type": "AWS::SNS::Topic",
      "Properties": { "TopicName": "deep-order-events" }
    },
    "AlertsTopic": {
      "Type": "AWS::SNS::Topic",
      "Properties": { "TopicName": "deep-alerts" }
    },
    "OrderProcessingQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "deep-order-processing" }
    },
    "AuditQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "deep-audit-log" }
    },
    "OrderSubscription": {
      "Type": "AWS::SNS::Subscription",
      "Properties": {
        "TopicArn": {"Ref": "OrderEventsTopic"},
        "Protocol": "sqs",
        "Endpoint": {"Fn::GetAtt": ["OrderProcessingQueue", "Arn"]}
      }
    },
    "AppEventBus": {
      "Type": "AWS::Events::EventBus",
      "Properties": { "Name": "deep-app-events" }
    },
    "OrderCreatedRule": {
      "Type": "AWS::Events::Rule",
      "Properties": {
        "Name": "deep-order-created",
        "EventBusName": {"Ref": "AppEventBus"},
        "EventPattern": "{\"source\":[\"com.deep.orders\"],\"detail-type\":[\"OrderCreated\"]}",
        "State": "ENABLED",
        "Targets": [{"Id": "audit-queue", "Arn": {"Fn::GetAtt": ["AuditQueue", "Arn"]}}]
      }
    },
    "MessagingLogs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": { "LogGroupName": "/messaging/deep/events" }
    },
    "Level3": {
      "Type": "AWS::CloudFormation::Stack",
      "Properties": {
        "TemplateURL": "` + srv.URL + `/deep-nest-templates/l3.json",
        "Parameters": { "LeafSuffix": {"Ref": "LeafSuffix"} }
      }
    }
  },
  "Outputs": {
    "AppBucketName": { "Value": {"Fn::GetAtt": ["Level3", "Outputs.AppBucketName"]} }
  }
}`

	// Upload all child templates to S3.
	for name, tmpl := range map[string]string{
		"l5.json": l5Template,
		"l4.json": l4Template,
		"l3.json": l3Template,
		"l2.json": l2Template,
	} {
		req, _ = http.NewRequest(http.MethodPut,
			srv.URL+"/deep-nest-templates/"+name,
			strings.NewReader(tmpl))
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("upload %s: %v", name, err)
		}
		resp.Body.Close()
	}

	// L1 (root) — "Platform Core": S3, KMS, SSM, Log group, nests L2.
	rootTemplate := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Description": "Level 1 - Platform Core (root)",
  "Resources": {
    "PlatformBucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": { "BucketName": "deep-platform-artifacts" }
    },
    "ConfigBucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": { "BucketName": "deep-platform-config" }
    },
    "PlatformKey": {
      "Type": "AWS::KMS::Key",
      "Properties": {
        "Description": "Platform master encryption key",
        "KeyPolicy": {
          "Version": "2012-10-17",
          "Statement": [{"Effect": "Allow", "Principal": {"AWS": "*"}, "Action": "kms:*", "Resource": "*"}]
        }
      }
    },
    "PlatformKeyAlias": {
      "Type": "AWS::KMS::Alias",
      "Properties": {
        "AliasName": "alias/deep-platform",
        "TargetKeyId": {"Ref": "PlatformKey"}
      }
    },
    "EnvironmentParam": {
      "Type": "AWS::SSM::Parameter",
      "Properties": {
        "Name": "/deep/platform/environment",
        "Type": "String",
        "Value": "development"
      }
    },
    "VersionParam": {
      "Type": "AWS::SSM::Parameter",
      "Properties": {
        "Name": "/deep/platform/version",
        "Type": "String",
        "Value": "1.0.0"
      }
    },
    "PlatformLogs": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": { "LogGroupName": "/platform/deep/audit" }
    },
    "Level2": {
      "Type": "AWS::CloudFormation::Stack",
      "Properties": {
        "TemplateURL": "` + srv.URL + `/deep-nest-templates/l2.json",
        "Parameters": { "LeafSuffix": "five" }
      }
    }
  },
  "Outputs": {
    "AppBucketName": { "Value": {"Fn::GetAtt": ["Level2", "Outputs.AppBucketName"]} }
  }
}`

	// When: CreateStack with the root template.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"deep-root"},
		"TemplateBody": []string{rootTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: root stack reaches CREATE_COMPLETE (implies all 5 levels succeeded).
	waitForStackStatus(t, srv, "deep-root", "CREATE_COMPLETE")

	// And: the leaf S3 bucket actually exists (full chain provisioned).
	bucketResp, err := http.Get(srv.URL + "/deep-leaf-five")
	if err != nil {
		t.Fatalf("leaf bucket probe: %v", err)
	}
	defer bucketResp.Body.Close()
	if bucketResp.StatusCode == http.StatusNotFound {
		t.Error("expected leaf bucket 'deep-leaf-five' to exist after 5-level nested provisioning")
	}

	// And: root output resolves through all 5 levels.
	descResp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"deep-root"},
	})
	defer descResp.Body.Close()
	rootBody := string(readBody(t, descResp))
	if !strings.Contains(rootBody, "deep-leaf-five") {
		t.Errorf("root output should resolve leaf bucket name, got: %s", rootBody)
	}

	// And: ListStacks shows 5 stacks in the nesting chain.
	listResp := cfnQuery(t, srv, "ListStacks", url.Values{})
	defer listResp.Body.Close()
	listBody := string(readBody(t, listResp))

	rootStackID := extractXML(rootBody, "StackId")
	if rootStackID == "" {
		t.Fatal("could not extract root StackId")
	}

	// Collect all child stack names from ListStacks.
	var childNames []string
	for _, line := range strings.Split(listBody, "<StackName>") {
		if idx := strings.Index(line, "</StackName>"); idx > 0 {
			name := line[:idx]
			if strings.HasPrefix(name, "deep-root-") {
				childNames = append(childNames, name)
			}
		}
	}
	// 4 nested children expected (L2, L3, L4, L5).
	if len(childNames) < 4 {
		t.Errorf("expected at least 4 nested child stacks, found %d: %v", len(childNames), childNames)
	}

	// Verify ParentId / RootId chain: every child should have a ParentId,
	// and all should share the same RootId (the root stack's ID).
	for _, cn := range childNames {
		desc := cfnQuery(t, srv, "DescribeStacks", url.Values{
			"StackName": []string{cn},
		})
		defer desc.Body.Close()
		childBody := string(readBody(t, desc))

		pid := extractXML(childBody, "ParentId")
		if pid == "" {
			t.Errorf("child %s should have ParentId", cn)
		}

		rid := extractXML(childBody, "RootId")
		if rid != rootStackID {
			t.Errorf("child %s RootId = %q, want root stack ID %q", cn, rid, rootStackID)
		}
	}

	// And: the topology endpoint shows nested-stack edges forming the chain.
	topoResp, err := http.Get(srv.URL + "/_topology")
	if err != nil {
		t.Fatalf("topology request: %v", err)
	}
	defer topoResp.Body.Close()
	topoBody := string(readBody(t, topoResp))

	// Count nested-stack edges — should be 4 (L1→L2, L2→L3, L3→L4, L4→L5).
	nestEdgeCount := strings.Count(topoBody, `"type":"nested-stack"`)
	if nestEdgeCount != 4 {
		t.Errorf("expected 4 nested-stack topology edges, got %d", nestEdgeCount)
	}

	// Verify stack-name ownership: topology nodes should carry stackName tags.
	if !strings.Contains(topoBody, `"stackName"`) {
		t.Error("topology should have stackName ownership tags on nested resources")
	}

	// And: each level created its resources (verifies side-effects across all 5 levels).

	// L1 (root): S3 buckets, KMS, SSM parameters, Log group
	for _, bucket := range []string{"deep-platform-artifacts", "deep-platform-config"} {
		br, err := http.Get(srv.URL + "/" + bucket)
		if err != nil {
			t.Fatalf("probe bucket %s: %v", bucket, err)
		}
		br.Body.Close()
		if br.StatusCode == http.StatusNotFound {
			t.Errorf("L1: expected bucket %q to exist", bucket)
		}
	}

	// L2: SNS topics (query protocol)
	snsResp, err := http.Get(srv.URL + "/?Action=ListTopics&Version=2010-03-31")
	if err != nil {
		t.Fatalf("list topics: %v", err)
	}
	defer snsResp.Body.Close()
	snsBody := string(readBody(t, snsResp))
	for _, topic := range []string{"deep-order-events", "deep-alerts"} {
		if !strings.Contains(snsBody, topic) {
			t.Errorf("L2: expected SNS topic %q", topic)
		}
	}

	// L2: EventBridge (not in topology; verify via stack completion — the stack
	// would have failed if the EventBridge bus + rule creation errored).

	// L3: DynamoDB tables, data bucket, secrets (verify via topology)
	for _, res := range []string{"deep-users", "deep-orders", "deep-data-lake"} {
		if !strings.Contains(topoBody, `"`+res+`"`) {
			t.Errorf("L3: expected resource %q in topology", res)
		}
	}

	// L4: Lambda functions, SQS queues (verify via topology)
	for _, res := range []string{"deep-processor", "deep-transformer", "deep-task-queue", "deep-retry-queue"} {
		if !strings.Contains(topoBody, `"`+res+`"`) {
			t.Errorf("L4: expected resource %q in topology", res)
		}
	}
	// L4: Step Functions (not in topology; verified by stack completion)

	// L5 (leaf): Application resources (verify via topology)
	for _, res := range []string{"app-events-five", "app-dlq-five", "app-sessions-five", "app-notifications-five"} {
		if !strings.Contains(topoBody, `"`+res+`"`) {
			t.Errorf("L5: expected resource %q in topology", res)
		}
	}
}

// ─── AppRegistry Application resource ─────────────────────────────────────────

const appregistryStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "PaymentsApp": {
      "Type": "AWS::ServiceCatalogAppRegistry::Application",
      "Properties": {
        "Name": "payments-service",
        "Description": "payments bounded context"
      }
    }
  }
}`

func TestCreateStack_AppRegistryApplication(t *testing.T) {
	// Given: CloudFormation + AppRegistry services
	srv := helpers.NewTestServer(t)

	// When: a stack is created with an AppRegistry Application resource
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"appreg-stack"},
		"TemplateBody": []string{appregistryStackTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: stack reaches CREATE_COMPLETE
	waitForStackStatus(t, srv, "appreg-stack", "CREATE_COMPLETE")

	// And: the application is queryable via the AppRegistry API
	getResp, err := http.Get(srv.URL + "/applications/payments-service")
	if err != nil {
		t.Fatalf("GetApplication: %v", err)
	}
	defer getResp.Body.Close()
	helpers.AssertStatus(t, getResp, http.StatusOK)
	body := readBody(t, getResp)
	if !strings.Contains(string(body), "payments-service") {
		t.Errorf("expected application in response, got: %s", body)
	}
	if !strings.Contains(string(body), "awsApplication") {
		t.Errorf("expected applicationTag.awsApplication in response, got: %s", body)
	}
}

// ─── awsApplication tag auto-association ─────────────────────────────────────

// A stack that creates an AppRegistry application and an SQS queue tagged
// with the CDK-propagated `awsApplication` tag. The provisioner should
// associate the queue with the application automatically, so it shows up in
// ListAssociatedResources without any explicit ResourceAssociation entry.
const awsApplicationTagStackTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyApp": {
      "Type": "AWS::ServiceCatalogAppRegistry::Application",
      "Properties": { "Name": "tagged-app" }
    },
    "MyQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {
        "QueueName": "tagged-queue",
        "Tags": [
          {
            "Key": "awsApplication",
            "Value": { "Fn::GetAtt": ["MyApp", "ApplicationTagValue"] }
          }
        ]
      }
    }
  }
}`

func TestCreateStack_AwsApplicationTagAutoAssociation(t *testing.T) {
	// Given: CloudFormation + AppRegistry + SQS services
	srv := helpers.NewTestServer(t)

	// When: a stack is created with a resource carrying the awsApplication tag
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"tagged-stack"},
		"TemplateBody": []string{awsApplicationTagStackTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: stack reaches CREATE_COMPLETE
	waitForStackStatus(t, srv, "tagged-stack", "CREATE_COMPLETE")

	// And: the tagged resource is listed under the application's associations
	listResp, err := http.Get(srv.URL + "/applications/tagged-app/resources")
	if err != nil {
		t.Fatalf("ListAssociatedResources: %v", err)
	}
	defer listResp.Body.Close()
	helpers.AssertStatus(t, listResp, http.StatusOK)
	body := readBody(t, listResp)
	if !strings.Contains(string(body), "tagged-queue") {
		t.Errorf("expected tagged-queue in associated resources, got: %s", body)
	}
}

// ─── Lambda LoggingConfig passthrough ─────────────────────────────────────────

const lambdaLoggingConfigTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ExecRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-logging-config-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]
        }
      }
    },
    "CustomLogGroup": {
      "Type": "AWS::Logs::LogGroup",
      "Properties": {
        "LogGroupName": "/custom/my-fn-logs"
      }
    },
    "MyFunction": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": ["ExecRole", "CustomLogGroup"],
      "Properties": {
        "FunctionName": "cfn-logging-config-fn",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": { "Fn::GetAtt": ["ExecRole", "Arn"] },
        "Code": { "ZipFile": "def handler(e, c): return {}" },
        "LoggingConfig": {
          "LogGroup": "/custom/my-fn-logs",
          "LogFormat": "Text"
        }
      }
    }
  }
}`

func TestCreateStack_LambdaLoggingConfig_noDefaultLogGroup(t *testing.T) {
	// Given: a stack with a Lambda function that has LoggingConfig pointing
	//        to a custom log group (not the default /aws/lambda/<name>).
	srv := helpers.NewTestServer(t)

	// When: the stack is created
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"logging-config-stack"},
		"TemplateBody": []string{lambdaLoggingConfigTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "logging-config-stack", "CREATE_COMPLETE")

	// Then: only the custom log group should exist — the default
	//       /aws/lambda/cfn-logging-config-fn must NOT be created.
	type describeLogGroupsResp struct {
		LogGroups []struct {
			LogGroupName string `json:"logGroupName"`
		} `json:"logGroups"`
	}

	body, _ := json.Marshal(map[string]any{})
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", "Logs_20140328.DescribeLogGroups")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DescribeLogGroups: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var groups describeLogGroupsResp
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		t.Fatalf("decode DescribeLogGroups: %v", err)
	}

	for _, g := range groups.LogGroups {
		if g.LogGroupName == "/aws/lambda/cfn-logging-config-fn" {
			t.Errorf("default log group /aws/lambda/cfn-logging-config-fn should not exist when LoggingConfig specifies a custom log group")
		}
	}

	// And: the custom log group should exist.
	found := false
	for _, g := range groups.LogGroups {
		if g.LogGroupName == "/custom/my-fn-logs" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected custom log group /custom/my-fn-logs to exist, got groups: %+v", groups.LogGroups)
	}
}

// ─── Lambda Tag Forwarding ────────────────────────────────────────────────────

const lambdaTagsTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ExecRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-tags-test-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]
        }
      }
    },
    "MyFunction": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": ["ExecRole"],
      "Properties": {
        "FunctionName": "cfn-tags-test-fn",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": { "Fn::GetAtt": ["ExecRole", "Arn"] },
        "Code": { "ZipFile": "def handler(e, c): return {}" },
        "Tags": [
          { "Key": "overcast:hot-reload-path", "Value": "/tmp/src" },
          { "Key": "env", "Value": "test" }
        ]
      }
    }
  }
}`

func TestCreateStack_LambdaFunction_forwardsTags(t *testing.T) {
	// Given: a CloudFormation stack with a Lambda function that has Tags.
	srv := helpers.NewTestServer(t)

	// When: the stack is created.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"lambda-tags-stack"},
		"TemplateBody": []string{lambdaTagsTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-tags-stack", "CREATE_COMPLETE")

	// Then: the Lambda function should have the Tags set.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/2015-03-31/functions/cfn-tags-test-fn", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var fn struct {
		Tags map[string]string `json:"Tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fn); err != nil {
		t.Fatalf("decode GetFunction: %v", err)
	}

	if fn.Tags["overcast:hot-reload-path"] != "/tmp/src" {
		t.Errorf("expected tag overcast:hot-reload-path=/tmp/src, got tags: %v", fn.Tags)
	}
	if fn.Tags["env"] != "test" {
		t.Errorf("expected tag env=test, got tags: %v", fn.Tags)
	}
}

const lambdaStackTagsTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ExecRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-stack-tags-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{"Effect":"Allow","Principal":{"Service":"lambda.amazonaws.com"},"Action":"sts:AssumeRole"}]
        }
      }
    },
    "MyFunction": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": ["ExecRole"],
      "Properties": {
        "FunctionName": "cfn-stack-tags-fn",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": { "Fn::GetAtt": ["ExecRole", "Arn"] },
        "Code": { "ZipFile": "def handler(e, c): return {}" },
        "Tags": [
          { "Key": "env", "Value": "resource" }
        ]
      }
    }
  }
}`

func TestCreateStack_LambdaFunction_appliesStackTags(t *testing.T) {
	// Given: stack-level tags and a Lambda resource with one overlapping resource tag.
	srv := helpers.NewTestServer(t)

	// When: stack is created with stack tags.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":           []string{"lambda-stack-tags-stack"},
		"TemplateBody":        []string{lambdaStackTagsTemplate},
		"Tags.member.1.Key":   []string{"overcast:hot-reload-path"},
		"Tags.member.1.Value": []string{"/tmp/stack-src"},
		"Tags.member.2.Key":   []string{"env"},
		"Tags.member.2.Value": []string{"stack"},
		"Tags.member.3.Key":   []string{"team"},
		"Tags.member.3.Value": []string{"platform"},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-stack-tags-stack", "CREATE_COMPLETE")

	// Then: Lambda function should include stack tags, with resource-level override for env.
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/2015-03-31/functions/cfn-stack-tags-fn", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GetFunction: %v", err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var fn struct {
		Tags map[string]string `json:"Tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&fn); err != nil {
		t.Fatalf("decode GetFunction: %v", err)
	}

	if fn.Tags["overcast:hot-reload-path"] != "/tmp/stack-src" {
		t.Errorf("expected stack tag overcast:hot-reload-path=/tmp/stack-src, got tags: %v", fn.Tags)
	}
	if fn.Tags["team"] != "platform" {
		t.Errorf("expected stack tag team=platform, got tags: %v", fn.Tags)
	}
	if fn.Tags["env"] != "resource" {
		t.Errorf("expected resource tag env=resource to override stack tag, got tags: %v", fn.Tags)
	}
}

// ─── TemplateURL support ──────────────────────────────────────────────────────

func TestCreateStack_withTemplateURL_fetchesFromS3(t *testing.T) {
	// Given: a template uploaded to S3
	srv := helpers.NewTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/tpl-bucket", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	resp.Body.Close()

	tmpl := `{"AWSTemplateFormatVersion":"2010-09-09","Description":"from S3","Resources":{}}`
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/tpl-bucket/stack.json", strings.NewReader(tmpl))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload template: %v", err)
	}
	resp.Body.Close()

	// When: CreateStack is called with TemplateURL instead of TemplateBody
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":   []string{"url-stack"},
		"TemplateURL": []string{srv.URL + "/tpl-bucket/stack.json"},
	})
	defer cr.Body.Close()

	// Then: stack is created successfully
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "url-stack", "CREATE_COMPLETE")
}

func TestCreateChangeSet_withTemplateURL_fetchesFromS3(t *testing.T) {
	// Given: a template uploaded to S3
	srv := helpers.NewTestServer(t)

	req, _ := http.NewRequest(http.MethodPut, srv.URL+"/cs-tpl-bucket", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("create bucket: %v", err)
	}
	resp.Body.Close()

	tmpl := `{"AWSTemplateFormatVersion":"2010-09-09","Description":"changeset from S3","Resources":{}}`
	req, _ = http.NewRequest(http.MethodPut, srv.URL+"/cs-tpl-bucket/cs-stack.json", strings.NewReader(tmpl))
	req.Header.Set("Content-Type", "application/json")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload template: %v", err)
	}
	resp.Body.Close()

	// When: CreateChangeSet is called with TemplateURL and ChangeSetType=CREATE
	cr := cfnQuery(t, srv, "CreateChangeSet", url.Values{
		"StackName":     []string{"cs-url-stack"},
		"ChangeSetName": []string{"cs-init"},
		"ChangeSetType": []string{"CREATE"},
		"TemplateURL":   []string{srv.URL + "/cs-tpl-bucket/cs-stack.json"},
	})
	defer cr.Body.Close()

	// Then: changeset is created successfully
	helpers.AssertStatus(t, cr, http.StatusOK)
	body := readBody(t, cr)
	if !strings.Contains(string(body), "cs-init") {
		t.Errorf("expected changeset ID in response, got: %s", body)
	}
}

// ─── API Gateway nested resources ──────────────────────────────────────────

// Template that creates a REST API with /messages and /messages/{id} resources.
// This mirrors a CDK-generated template where the child resource's ParentId
// uses Ref (which resolves to the physical ID of the parent resource).
const apigwNestedResourceTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "RestApi": {
      "Type": "AWS::ApiGateway::RestApi",
      "Properties": { "Name": "push-api" }
    },
    "MessagesResource": {
      "Type": "AWS::ApiGateway::Resource",
      "Properties": {
        "RestApiId": { "Ref": "RestApi" },
        "ParentId": { "Fn::GetAtt": ["RestApi", "RootResourceId"] },
        "PathPart": "messages"
      }
    },
    "MessageIdResource": {
      "Type": "AWS::ApiGateway::Resource",
      "Properties": {
        "RestApiId": { "Ref": "RestApi" },
        "ParentId": { "Ref": "MessagesResource" },
        "PathPart": "{id}"
      }
    }
  }
}`

func TestCreateStack_APIGatewayNestedResources(t *testing.T) {
	// Given: CloudFormation service
	srv := helpers.NewTestServer(t)

	// When: creating a stack with nested API Gateway resources
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"apigw-nested-stack"},
		"TemplateBody": []string{apigwNestedResourceTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: the stack should reach CREATE_COMPLETE (not ROLLBACK_COMPLETE)
	waitForStackStatus(t, srv, "apigw-nested-stack", "CREATE_COMPLETE")
}

// ─── SQS-based Event Source Mapping ───────────────────────────────────────────

const sqsESMTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "MyQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": { "QueueName": "cfn-sqs-esm-test-queue" }
    },
    "ExecRole": {
      "Type": "AWS::IAM::Role",
      "Properties": {
        "RoleName": "cfn-sqs-esm-test-role",
        "AssumeRolePolicyDocument": {
          "Version": "2012-10-17",
          "Statement": [{ "Effect": "Allow", "Principal": { "Service": "lambda.amazonaws.com" }, "Action": "sts:AssumeRole" }]
        }
      }
    },
    "MyFunction": {
      "Type": "AWS::Lambda::Function",
      "DependsOn": "ExecRole",
      "Properties": {
        "FunctionName": "cfn-sqs-esm-test-fn",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": { "Fn::GetAtt": ["ExecRole", "Arn"] },
        "Code": { "ZipFile": "def handler(e, c): return {}" }
      }
    },
    "SqsESM": {
      "Type": "AWS::Lambda::EventSourceMapping",
      "Properties": {
        "EventSourceArn": { "Fn::GetAtt": ["MyQueue", "Arn"] },
        "FunctionName":   { "Ref": "MyFunction" },
        "BatchSize": 5
      }
    }
  }
}`

func TestCreateStack_SQSEventSourceMapping(t *testing.T) {
	// Given: a stack with an SQS queue, a Lambda function, and an ESM.
	srv := helpers.NewTestServer(t)

	// When: the stack is created.
	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"sqs-esm-stack"},
		"TemplateBody": []string{sqsESMTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	// Then: stack reaches CREATE_COMPLETE.
	waitForStackStatus(t, srv, "sqs-esm-stack", "CREATE_COMPLETE")

	// And: the ESM should exist and be wired to the SQS queue.
	esmResp, err := http.Get(srv.URL + "/2015-03-31/event-source-mappings/")
	if err != nil {
		t.Fatalf("ListEventSourceMappings: %v", err)
	}
	defer esmResp.Body.Close()
	helpers.AssertStatus(t, esmResp, http.StatusOK)

	var esmList struct {
		EventSourceMappings []struct {
			UUID           string `json:"UUID"`
			EventSourceArn string `json:"EventSourceArn"`
			FunctionArn    string `json:"FunctionArn"`
			State          string `json:"State"`
		} `json:"EventSourceMappings"`
	}
	if err := json.NewDecoder(esmResp.Body).Decode(&esmList); err != nil {
		t.Fatalf("decode ESM list: %v", err)
	}

	if len(esmList.EventSourceMappings) != 1 {
		t.Fatalf("expected 1 ESM, got %d", len(esmList.EventSourceMappings))
	}

	esm := esmList.EventSourceMappings[0]
	if !strings.Contains(esm.EventSourceArn, ":sqs:") {
		t.Errorf("EventSourceArn should be an SQS ARN, got: %s", esm.EventSourceArn)
	}
	if !strings.Contains(esm.EventSourceArn, "cfn-sqs-esm-test-queue") {
		t.Errorf("EventSourceArn should reference the queue, got: %s", esm.EventSourceArn)
	}
	if !strings.Contains(esm.FunctionArn, "cfn-sqs-esm-test-fn") {
		t.Errorf("FunctionArn should reference the function, got: %s", esm.FunctionArn)
	}
	if esm.State != "Enabled" {
		t.Errorf("State: got %s, want Enabled", esm.State)
	}

	// And: the topology should include an esm edge.
	topoResp, err := http.Get(srv.URL + "/_topology")
	if err != nil {
		t.Fatalf("topology: %v", err)
	}
	defer topoResp.Body.Close()
	var topo struct {
		Edges []struct {
			Type   string `json:"type"`
			Source string `json:"source"`
			Target string `json:"target"`
		} `json:"edges"`
	}
	if err := json.NewDecoder(topoResp.Body).Decode(&topo); err != nil {
		t.Fatalf("decode topology: %v", err)
	}

	foundESM := false
	for _, e := range topo.Edges {
		if e.Type == "esm" && strings.Contains(e.Source, "sqs") && strings.Contains(e.Target, "lambda") {
			foundESM = true
			break
		}
	}
	if !foundESM {
		t.Errorf("expected esm edge in topology (SQS → Lambda), got edges: %+v", topo.Edges)
	}
}

// ─── API Gateway: ApiKey + UsagePlan + UsagePlanKey ───────────────────────

const apigwApiKeyTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "RestApi": {
      "Type": "AWS::ApiGateway::RestApi",
      "Properties": { "Name": "cfn-key-api" }
    },
    "Resource": {
      "Type": "AWS::ApiGateway::Resource",
      "Properties": {
        "RestApiId": { "Ref": "RestApi" },
        "ParentId":  { "Fn::GetAtt": ["RestApi", "RootResourceId"] },
        "PathPart":  "secret"
      }
    },
    "Method": {
      "Type": "AWS::ApiGateway::Method",
      "Properties": {
        "RestApiId":         { "Ref": "RestApi" },
        "ResourceId":        { "Ref": "Resource" },
        "HttpMethod":        "GET",
        "AuthorizationType": "NONE",
        "ApiKeyRequired":    true
      }
    },
    "Deployment": {
      "Type": "AWS::ApiGateway::Deployment",
      "DependsOn": "Method",
      "Properties": { "RestApiId": { "Ref": "RestApi" } }
    },
    "Stage": {
      "Type": "AWS::ApiGateway::Stage",
      "Properties": {
        "RestApiId":    { "Ref": "RestApi" },
        "StageName":    "prod",
        "DeploymentId": { "Ref": "Deployment" }
      }
    },
    "Key": {
      "Type": "AWS::ApiGateway::ApiKey",
      "Properties": {
        "Name":    "cfn-test-key",
        "Enabled": true,
        "Value":   "cfn0000000000000000000000000000000000000"
      }
    },
    "Plan": {
      "Type": "AWS::ApiGateway::UsagePlan",
      "DependsOn": "Stage",
      "Properties": {
        "UsagePlanName": "cfn-test-plan",
        "ApiStages": [
          { "ApiId": { "Ref": "RestApi" }, "Stage": "prod" }
        ]
      }
    },
    "PlanKey": {
      "Type": "AWS::ApiGateway::UsagePlanKey",
      "Properties": {
        "UsagePlanId": { "Ref": "Plan" },
        "KeyId":       { "Ref": "Key" },
        "KeyType":     "API_KEY"
      }
    }
  }
}`

func TestCreateStack_APIGatewayApiKeyAndUsagePlan(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"apigw-key-stack"},
		"TemplateBody": []string{apigwApiKeyTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "apigw-key-stack", "CREATE_COMPLETE")

	// Verify the ApiKey was created.
	keysResp, err := http.Get(srv.URL + "/apikeys")
	if err != nil {
		t.Fatalf("GetApiKeys: %v", err)
	}
	defer keysResp.Body.Close()
	helpers.AssertStatus(t, keysResp, http.StatusOK)
	var keys struct {
		Item []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Value string `json:"value"`
		} `json:"item"`
	}
	if err := json.NewDecoder(keysResp.Body).Decode(&keys); err != nil {
		t.Fatalf("decode keys: %v", err)
	}
	var keyID string
	for _, k := range keys.Item {
		if k.Name == "cfn-test-key" {
			keyID = k.ID
		}
	}
	if keyID == "" {
		t.Fatalf("expected api key 'cfn-test-key', got %+v", keys.Item)
	}

	// Verify the UsagePlan was created and the key is attached.
	plansResp, err := http.Get(srv.URL + "/usageplans")
	if err != nil {
		t.Fatalf("GetUsagePlans: %v", err)
	}
	defer plansResp.Body.Close()
	helpers.AssertStatus(t, plansResp, http.StatusOK)
	var plans struct {
		Item []struct {
			ID        string   `json:"id"`
			Name      string   `json:"name"`
			KeyIDs    []string `json:"keyIds"`
			APIStages []struct {
				ApiID string `json:"apiId"`
				Stage string `json:"stage"`
			} `json:"apiStages"`
		} `json:"item"`
	}
	if err := json.NewDecoder(plansResp.Body).Decode(&plans); err != nil {
		t.Fatalf("decode plans: %v", err)
	}
	var found bool
	for _, p := range plans.Item {
		if p.Name != "cfn-test-plan" {
			continue
		}
		found = true
		if len(p.APIStages) != 1 || p.APIStages[0].Stage != "prod" {
			t.Errorf("expected 1 apiStage with stage=prod, got %+v", p.APIStages)
		}
		hasKey := false
		for _, k := range p.KeyIDs {
			if k == keyID {
				hasKey = true
			}
		}
		if !hasKey {
			t.Errorf("expected plan to contain key %s, got %+v", keyID, p.KeyIDs)
		}
	}
	if !found {
		t.Fatalf("usage plan 'cfn-test-plan' not found in %+v", plans.Item)
	}
}
