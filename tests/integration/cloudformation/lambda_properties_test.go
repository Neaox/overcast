package cloudformation_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const lambdaPriorityOnePropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ConfiguredFunction": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "cfn-lambda-configured",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": {"ZipFile": "def handler(event, context): return {}"},
        "Architectures": ["arm64"],
        "VpcConfig": {
          "SubnetIds": ["subnet-cfn0001", "subnet-cfn0002"],
          "SecurityGroupIds": ["sg-cfn0001"],
          "Ipv6AllowedForDualStack": true
        },
        "FileSystemConfigs": [{
          "Arn": "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-0123456789abcdef0",
          "LocalMountPath": "/mnt/shared"
        }],
        "ReservedConcurrentExecutions": 0
      }
    }
  }
}`

func TestCreateStack_LambdaFunctionForwardsPriorityOneProperties(t *testing.T) {
	// Given: a Lambda function with the runtime properties CloudFormation used
	// to discard before calling CreateFunction.
	srv := helpers.NewTestServer(t)

	// When: CloudFormation creates the function.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"lambda-priority-one-stack"},
		"TemplateBody": []string{lambdaPriorityOnePropertiesTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-priority-one-stack", "CREATE_COMPLETE")

	// Then: Lambda's own configuration read agrees with the template.
	configResp := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-lambda-configured/configuration", nil)
	defer configResp.Body.Close()
	helpers.AssertStatus(t, configResp, http.StatusOK)

	var config struct {
		Architectures []string `json:"Architectures"`
		VpcConfig     struct {
			SubnetIds               []string `json:"SubnetIds"`
			SecurityGroupIds        []string `json:"SecurityGroupIds"`
			Ipv6AllowedForDualStack bool     `json:"Ipv6AllowedForDualStack"`
		} `json:"VpcConfig"`
		FileSystemConfigs []struct {
			Arn            string `json:"Arn"`
			LocalMountPath string `json:"LocalMountPath"`
		} `json:"FileSystemConfigs"`
	}
	helpers.DecodeJSON(t, configResp, &config)

	if len(config.Architectures) != 1 || config.Architectures[0] != "arm64" {
		t.Errorf("Architectures = %v, want [arm64]", config.Architectures)
	}
	if len(config.VpcConfig.SubnetIds) != 2 || config.VpcConfig.SubnetIds[0] != "subnet-cfn0001" || config.VpcConfig.SubnetIds[1] != "subnet-cfn0002" {
		t.Errorf("VpcConfig.SubnetIds = %v", config.VpcConfig.SubnetIds)
	}
	if len(config.VpcConfig.SecurityGroupIds) != 1 || config.VpcConfig.SecurityGroupIds[0] != "sg-cfn0001" {
		t.Errorf("VpcConfig.SecurityGroupIds = %v", config.VpcConfig.SecurityGroupIds)
	}
	if !config.VpcConfig.Ipv6AllowedForDualStack {
		t.Error("VpcConfig.Ipv6AllowedForDualStack = false, want true")
	}
	if len(config.FileSystemConfigs) != 1 || config.FileSystemConfigs[0].Arn != "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-0123456789abcdef0" || config.FileSystemConfigs[0].LocalMountPath != "/mnt/shared" {
		t.Errorf("FileSystemConfigs = %+v", config.FileSystemConfigs)
	}

	// And: reserved concurrency is configured through Lambda's separate API.
	concurrencyResp := lambdaRequest(t, srv, http.MethodGet,
		"/2019-09-30/functions/cfn-lambda-configured/concurrency", nil)
	defer concurrencyResp.Body.Close()
	helpers.AssertStatus(t, concurrencyResp, http.StatusOK)
	var concurrency struct {
		ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
	}
	helpers.DecodeJSON(t, concurrencyResp, &concurrency)
	if concurrency.ReservedConcurrentExecutions != 0 {
		t.Errorf("ReservedConcurrentExecutions = %d, want 0", concurrency.ReservedConcurrentExecutions)
	}
}

const lambdaImagePropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ImageFunction": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "cfn-lambda-image",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "PackageType": "Image",
        "Code": {"ImageUri": "000000000000.dkr.ecr.us-east-1.amazonaws.com/function:latest"},
        "ImageConfig": {
          "EntryPoint": ["/entry.sh"],
          "Command": ["app.handler"],
          "WorkingDirectory": "/var/task"
        }
      }
    }
  }
}`

const lambdaUpdatedPriorityOnePropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "ConfiguredFunction": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "cfn-lambda-configured",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": {"ZipFile": "def handler(event, context): return {'updated': True}"},
        "Architectures": ["x86_64"],
        "VpcConfig": {
          "SubnetIds": ["subnet-cfn-updated"],
          "SecurityGroupIds": ["sg-cfn-updated"],
          "Ipv6AllowedForDualStack": false
        },
        "FileSystemConfigs": [{
          "Arn": "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-123456789abcdef01",
          "LocalMountPath": "/mnt/updated"
        }],
        "ReservedConcurrentExecutions": 2
      }
    }
  }
}`

func TestUpdateStack_LambdaFunctionForwardsPriorityOneProperties(t *testing.T) {
	// Given: a stack containing a Lambda function with all priority-one
	// configuration fields.
	srv := helpers.NewTestServer(t)
	createLambdaStack(t, srv, "lambda-priority-one-update-stack", lambdaPriorityOnePropertiesTemplate)

	// When: CloudFormation updates those fields in place.
	resp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"lambda-priority-one-update-stack"},
		"TemplateBody": []string{lambdaUpdatedPriorityOnePropertiesTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-priority-one-update-stack", "UPDATE_COMPLETE")

	// Then: Lambda's source-of-truth configuration reflects the update.
	configResp := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-lambda-configured/configuration", nil)
	defer configResp.Body.Close()
	helpers.AssertStatus(t, configResp, http.StatusOK)
	var config struct {
		Architectures []string `json:"Architectures"`
		VpcConfig     struct {
			SubnetIds               []string `json:"SubnetIds"`
			SecurityGroupIds        []string `json:"SecurityGroupIds"`
			Ipv6AllowedForDualStack bool     `json:"Ipv6AllowedForDualStack"`
		} `json:"VpcConfig"`
		FileSystemConfigs []struct {
			Arn            string `json:"Arn"`
			LocalMountPath string `json:"LocalMountPath"`
		} `json:"FileSystemConfigs"`
	}
	helpers.DecodeJSON(t, configResp, &config)
	if len(config.Architectures) != 1 || config.Architectures[0] != "x86_64" {
		t.Errorf("Architectures = %v, want [x86_64]", config.Architectures)
	}
	if len(config.VpcConfig.SubnetIds) != 1 || config.VpcConfig.SubnetIds[0] != "subnet-cfn-updated" {
		t.Errorf("VpcConfig.SubnetIds = %v", config.VpcConfig.SubnetIds)
	}
	if len(config.VpcConfig.SecurityGroupIds) != 1 || config.VpcConfig.SecurityGroupIds[0] != "sg-cfn-updated" {
		t.Errorf("VpcConfig.SecurityGroupIds = %v", config.VpcConfig.SecurityGroupIds)
	}
	if config.VpcConfig.Ipv6AllowedForDualStack {
		t.Error("VpcConfig.Ipv6AllowedForDualStack = true, want false")
	}
	if len(config.FileSystemConfigs) != 1 || config.FileSystemConfigs[0].Arn != "arn:aws:elasticfilesystem:us-east-1:000000000000:access-point/fsap-123456789abcdef01" || config.FileSystemConfigs[0].LocalMountPath != "/mnt/updated" {
		t.Errorf("FileSystemConfigs = %+v", config.FileSystemConfigs)
	}

	concurrencyResp := lambdaRequest(t, srv, http.MethodGet,
		"/2019-09-30/functions/cfn-lambda-configured/concurrency", nil)
	defer concurrencyResp.Body.Close()
	helpers.AssertStatus(t, concurrencyResp, http.StatusOK)
	var concurrency struct {
		ReservedConcurrentExecutions int `json:"ReservedConcurrentExecutions"`
	}
	helpers.DecodeJSON(t, concurrencyResp, &concurrency)
	if concurrency.ReservedConcurrentExecutions != 2 {
		t.Errorf("ReservedConcurrentExecutions = %d, want 2", concurrency.ReservedConcurrentExecutions)
	}

	// When: the reserved-concurrency property is removed from the template.
	withoutReservedConcurrency := strings.Replace(
		lambdaUpdatedPriorityOnePropertiesTemplate,
		",\n        \"ReservedConcurrentExecutions\": 2", "", 1,
	)
	removeResp := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    []string{"lambda-priority-one-update-stack"},
		"TemplateBody": []string{withoutReservedConcurrency},
	})
	defer removeResp.Body.Close()
	helpers.AssertStatus(t, removeResp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-priority-one-update-stack", "UPDATE_COMPLETE")

	// Then: CloudFormation delegates the clear to DeleteFunctionConcurrency.
	removedConcurrencyResp := lambdaRequest(t, srv, http.MethodGet,
		"/2019-09-30/functions/cfn-lambda-configured/concurrency", nil)
	defer removedConcurrencyResp.Body.Close()
	helpers.AssertStatus(t, removedConcurrencyResp, http.StatusNotFound)
	helpers.AssertJSONError(t, removedConcurrencyResp, "ResourceNotFoundException")
}

const lambdaNegativeConcurrencyTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Function": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "cfn-lambda-invalid-concurrency",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": {"ZipFile": "def handler(event, context): return {}"},
        "ReservedConcurrentExecutions": -1
      }
    }
  }
}`

func TestCreateStack_LambdaInvalidReservedConcurrencyRollsBack(t *testing.T) {
	// Given: a template value that Lambda's PutFunctionConcurrency API rejects.
	srv := helpers.NewTestServer(t)

	// When: CloudFormation creates the stack.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"lambda-invalid-concurrency-stack"},
		"TemplateBody": []string{lambdaNegativeConcurrencyTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-invalid-concurrency-stack", "ROLLBACK_COMPLETE")

	// Then: the API failure was not swallowed and the partially created
	// function was cleaned up.
	configResp := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-lambda-invalid-concurrency/configuration", nil)
	defer configResp.Body.Close()
	helpers.AssertStatus(t, configResp, http.StatusNotFound)
	helpers.AssertJSONError(t, configResp, "ResourceNotFoundException")
}

func TestCreateStack_LambdaImageFunctionForwardsPackageAndImageConfig(t *testing.T) {
	// Given: a CloudFormation image function with Dockerfile overrides.
	srv := helpers.NewTestServer(t)

	// When: the stack is created.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"lambda-image-stack"},
		"TemplateBody": []string{lambdaImagePropertiesTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-image-stack", "CREATE_COMPLETE")

	// Then: Lambda reports the image package and overrides from the template.
	configResp := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-lambda-image/configuration", nil)
	defer configResp.Body.Close()
	helpers.AssertStatus(t, configResp, http.StatusOK)
	var config struct {
		PackageType         string `json:"PackageType"`
		ImageConfigResponse struct {
			ImageConfig struct {
				EntryPoint       []string `json:"EntryPoint"`
				Command          []string `json:"Command"`
				WorkingDirectory string   `json:"WorkingDirectory"`
			} `json:"ImageConfig"`
		} `json:"ImageConfigResponse"`
	}
	helpers.DecodeJSON(t, configResp, &config)
	if config.PackageType != "Image" {
		t.Errorf("PackageType = %q, want Image", config.PackageType)
	}
	imageConfig := config.ImageConfigResponse.ImageConfig
	if len(imageConfig.EntryPoint) != 1 || imageConfig.EntryPoint[0] != "/entry.sh" {
		t.Errorf("ImageConfigResponse.ImageConfig.EntryPoint = %v", imageConfig.EntryPoint)
	}
	if len(imageConfig.Command) != 1 || imageConfig.Command[0] != "app.handler" {
		t.Errorf("ImageConfigResponse.ImageConfig.Command = %v", imageConfig.Command)
	}
	if imageConfig.WorkingDirectory != "/var/task" {
		t.Errorf("ImageConfigResponse.ImageConfig.WorkingDirectory = %q, want /var/task", imageConfig.WorkingDirectory)
	}
}

const lambdaPermissionTargetTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Function": {
      "Type": "AWS::Lambda::Function",
      "Properties": {
        "FunctionName": "cfn-lambda-permission-target",
        "Runtime": "python3.11",
        "Handler": "index.handler",
        "Role": "arn:aws:iam::000000000000:role/lambda-role",
        "Code": {"ZipFile": "def handler(event, context): return {}"}
      }
    }
  }
}`

const lambdaPermissionTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "AllowSNSInvoke": {
      "Type": "AWS::Lambda::Permission",
      "Properties": {
        "FunctionName": "arn:aws:lambda:us-east-1:000000000000:function:cfn-lambda-permission-target",
        "Action": "lambda:InvokeFunction",
        "Principal": "sns.amazonaws.com",
        "SourceArn": "arn:aws:sns:us-east-1:000000000000:cfn-topic",
        "SourceAccount": "000000000000"
      }
    }
  }
}`

func TestCreateAndDeleteStack_LambdaPermissionMutatesFunctionPolicy(t *testing.T) {
	// Given: a Lambda function owned by a separate stack, so deleting the
	// permission stack leaves the function available for policy verification.
	srv := helpers.NewTestServer(t)
	createLambdaStack(t, srv, "lambda-permission-target-stack", lambdaPermissionTargetTemplate)

	// When: CloudFormation creates an AWS::Lambda::Permission resource.
	createLambdaStack(t, srv, "lambda-permission-stack", lambdaPermissionTemplate)

	// Then: Lambda's resource policy contains the modeled grant.
	policyResp := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-lambda-permission-target/policy", nil)
	defer policyResp.Body.Close()
	helpers.AssertStatus(t, policyResp, http.StatusOK)
	var policyEnvelope struct {
		Policy     string `json:"Policy"`
		RevisionId string `json:"RevisionId"`
	}
	helpers.DecodeJSON(t, policyResp, &policyEnvelope)
	if policyEnvelope.RevisionId == "" {
		t.Fatal("GetPolicy returned an empty RevisionId")
	}
	var policy struct {
		Version   string `json:"Version"`
		ID        string `json:"Id"`
		Statement []struct {
			Sid       string                       `json:"Sid"`
			Effect    string                       `json:"Effect"`
			Principal map[string]string            `json:"Principal"`
			Action    string                       `json:"Action"`
			Resource  string                       `json:"Resource"`
			Condition map[string]map[string]string `json:"Condition"`
		} `json:"Statement"`
	}
	if err := json.Unmarshal([]byte(policyEnvelope.Policy), &policy); err != nil {
		t.Fatalf("decode Lambda Policy document: %v", err)
	}
	if policy.Version != "2012-10-17" || policy.ID != "default" {
		t.Errorf("policy header = version %q id %q", policy.Version, policy.ID)
	}
	if len(policy.Statement) != 1 {
		t.Fatalf("policy statements = %d, want 1: %s", len(policy.Statement), policyEnvelope.Policy)
	}
	statement := policy.Statement[0]
	if statement.Sid == "" || statement.Effect != "Allow" || statement.Principal["Service"] != "sns.amazonaws.com" || statement.Action != "lambda:InvokeFunction" {
		t.Errorf("unexpected policy statement: %+v", statement)
	}
	if statement.Resource != "arn:aws:lambda:us-east-1:000000000000:function:cfn-lambda-permission-target" {
		t.Errorf("statement Resource = %q", statement.Resource)
	}
	if statement.Condition["ArnLike"]["AWS:SourceArn"] != "arn:aws:sns:us-east-1:000000000000:cfn-topic" {
		t.Errorf("SourceArn condition = %+v", statement.Condition)
	}
	if statement.Condition["StringEquals"]["AWS:SourceAccount"] != "000000000000" {
		t.Errorf("SourceAccount condition = %+v", statement.Condition)
	}

	// When: the permission stack is deleted.
	deleteResp := cfnQuery(t, srv, "DeleteStack", url.Values{
		"StackName": []string{"lambda-permission-stack"},
	})
	defer deleteResp.Body.Close()
	helpers.AssertStatus(t, deleteResp, http.StatusOK)
	waitForStackStatus(t, srv, "lambda-permission-stack", "DELETE_COMPLETE")

	// Then: CloudFormation removed the statement through RemovePermission.
	removedResp := lambdaRequest(t, srv, http.MethodGet,
		"/2015-03-31/functions/cfn-lambda-permission-target/policy", nil)
	defer removedResp.Body.Close()
	helpers.AssertStatus(t, removedResp, http.StatusNotFound)
	helpers.AssertJSONError(t, removedResp, "ResourceNotFoundException")
}

func createLambdaStack(t *testing.T, srv *helpers.TestServer, stackName, template string) {
	t.Helper()
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{stackName},
		"TemplateBody": []string{template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
}

func lambdaRequest(t *testing.T, srv *helpers.TestServer, method, path string, body any) *http.Response {
	t.Helper()
	var requestBody []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal Lambda request: %v", err)
		}
		requestBody = data
	}
	req, err := http.NewRequest(method, srv.URL+path, bytes.NewReader(requestBody))
	if err != nil {
		t.Fatalf("build Lambda request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Lambda %s %s: %v", method, path, err)
	}
	return resp
}
