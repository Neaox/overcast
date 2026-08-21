package cloudformation_test

// cognito_properties_test.go — Issue #536: Cognito properties dropped between
// CDK and the emulator.
//
// cognitoUserPoolHandler.Create sent only a core subset of AWS::Cognito::
// UserPool properties to CreateUserPool (PoolName, Policies,
// UsernameAttributes, AutoVerifiedAttributes, Schema,
// VerificationMessageTemplate, AdminCreateUserConfig, EmailConfiguration,
// MfaConfiguration) and dropped the rest — a CDK pool configured with
// aliasAttributes, deviceTracking, accountRecovery, SMS/email verification
// templates, or lambdaTriggers provisioned fine and reported none of it back.
// There was also no Update handler at all, so any template change replaced
// the pool instead of reconciling it. cognitoUserPoolClientHandler had the
// same gap for PreventUserExistenceErrors/ReadAttributes/WriteAttributes.
//
// LambdaConfig is threaded and echoed by DescribeUserPool so the drop is
// visible, but the emulator does not invoke any of the configured triggers —
// see capabilities_dev.go and docs/dev/compatibility/services/cognito.yaml.

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

type cognitoDescribedUserPool struct {
	Id                  string   `json:"Id"`
	AliasAttributes     []string `json:"AliasAttributes"`
	DeviceConfiguration struct {
		ChallengeRequiredOnNewDevice     bool `json:"ChallengeRequiredOnNewDevice"`
		DeviceOnlyRememberedOnUserPrompt bool `json:"DeviceOnlyRememberedOnUserPrompt"`
	} `json:"DeviceConfiguration"`
	AccountRecoverySetting struct {
		RecoveryMechanisms []struct {
			Name     string `json:"Name"`
			Priority int    `json:"Priority"`
		} `json:"RecoveryMechanisms"`
	} `json:"AccountRecoverySetting"`
	SmsConfiguration struct {
		SnsCallerArn string `json:"SnsCallerArn"`
		ExternalId   string `json:"ExternalId"`
	} `json:"SmsConfiguration"`
	SmsAuthenticationMessage string `json:"SmsAuthenticationMessage"`
	SmsVerificationMessage   string `json:"SmsVerificationMessage"`
	EmailVerificationMessage string `json:"EmailVerificationMessage"`
	EmailVerificationSubject string `json:"EmailVerificationSubject"`
	LambdaConfig             struct {
		PreSignUp        string `json:"PreSignUp"`
		PostConfirmation string `json:"PostConfirmation"`
	} `json:"LambdaConfig"`
}

func cognitoDescribeUserPool(t *testing.T, srv *helpers.TestServer, poolID string) cognitoDescribedUserPool {
	t.Helper()
	resp := cognitoJSONCall(t, srv, "DescribeUserPool", map[string]any{"UserPoolId": poolID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		UserPool cognitoDescribedUserPool `json:"UserPool"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.UserPool
}

type cognitoDescribedUserPoolClient struct {
	ClientId                   string   `json:"ClientId"`
	PreventUserExistenceErrors string   `json:"PreventUserExistenceErrors"`
	ReadAttributes             []string `json:"ReadAttributes"`
	WriteAttributes            []string `json:"WriteAttributes"`
}

// cognitoStackOutput extracts a named Output's value from a DescribeStacks
// XML response body.
func cognitoStackOutput(body, key string) string {
	return extractBetween(body, "<OutputKey>"+key+"</OutputKey><OutputValue>", "</OutputValue>")
}

func cognitoDescribeUserPoolClient(t *testing.T, srv *helpers.TestServer, poolID, clientID string) cognitoDescribedUserPoolClient {
	t.Helper()
	resp := cognitoJSONCall(t, srv, "DescribeUserPoolClient", map[string]any{"UserPoolId": poolID, "ClientId": clientID})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var result struct {
		UserPoolClient cognitoDescribedUserPoolClient `json:"UserPoolClient"`
	}
	helpers.DecodeJSON(t, resp, &result)
	return result.UserPoolClient
}

const cognitoPropertiesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Pool": {
      "Type": "AWS::Cognito::UserPool",
      "Properties": {
        "UserPoolName": "cfn-props-pool",
        "AliasAttributes": ["email"],
        "DeviceConfiguration": {"ChallengeRequiredOnNewDevice": true, "DeviceOnlyRememberedOnUserPrompt": true},
        "AccountRecoverySetting": {"RecoveryMechanisms": [{"Name": "verified_email", "Priority": 1}]},
        "SmsConfiguration": {"SnsCallerArn": "arn:aws:iam::123456789012:role/SmsRole", "ExternalId": "ext-id"},
        "SmsAuthenticationMessage": "Your authentication code is {####}",
        "SmsVerificationMessage": "Your verification code is {####}",
        "EmailVerificationMessage": "Your verification code is {####}",
        "EmailVerificationSubject": "Verify your account",
        "LambdaConfig": {
          "PreSignUp": "arn:aws:lambda:us-east-1:123456789012:function:pre-sign-up",
          "PostConfirmation": "arn:aws:lambda:us-east-1:123456789012:function:post-confirmation"
        }
      }
    },
    "Client": {
      "Type": "AWS::Cognito::UserPoolClient",
      "Properties": {
        "UserPoolId": {"Ref": "Pool"},
        "ClientName": "cfn-props-client",
        "PreventUserExistenceErrors": "ENABLED",
        "ReadAttributes": ["email"],
        "WriteAttributes": ["email"]
      }
    }
  },
  "Outputs": {
    "PoolId": {"Value": {"Ref": "Pool"}},
    "ClientId": {"Value": {"Ref": "Client"}}
  }
}`

// A CDK-shaped user pool carrying AliasAttributes, DeviceConfiguration,
// AccountRecoverySetting, SmsConfiguration, the SMS/email verification
// message properties, and LambdaConfig must come out the other side with all
// of them — not just the Policies/UsernameAttributes/Schema/
// VerificationMessageTemplate/AdminCreateUserConfig/EmailConfiguration/
// MfaConfiguration subset that already worked. The pool client's
// PreventUserExistenceErrors/ReadAttributes/WriteAttributes must round-trip
// too.
func TestCreateStack_CognitoUserPoolPropertiesRoundTrip(t *testing.T) {
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cognito-props-stack"},
		"TemplateBody": {cognitoPropertiesTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "cognito-props-stack", "CREATE_COMPLETE")

	describeResp := cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {"cognito-props-stack"}})
	defer describeResp.Body.Close()
	body := string(readBody(t, describeResp))
	poolID := cognitoStackOutput(body, "PoolId")
	clientID := cognitoStackOutput(body, "ClientId")
	if poolID == "" || clientID == "" {
		t.Fatalf("could not read PoolId/ClientId outputs from DescribeStacks: %s", body)
	}

	pool := cognitoDescribeUserPool(t, srv, poolID)
	if len(pool.AliasAttributes) != 1 || pool.AliasAttributes[0] != "email" {
		t.Errorf("AliasAttributes = %v, want [email]", pool.AliasAttributes)
	}
	if !pool.DeviceConfiguration.ChallengeRequiredOnNewDevice || !pool.DeviceConfiguration.DeviceOnlyRememberedOnUserPrompt {
		t.Errorf("DeviceConfiguration = %+v, want both true", pool.DeviceConfiguration)
	}
	if len(pool.AccountRecoverySetting.RecoveryMechanisms) != 1 ||
		pool.AccountRecoverySetting.RecoveryMechanisms[0].Name != "verified_email" ||
		pool.AccountRecoverySetting.RecoveryMechanisms[0].Priority != 1 {
		t.Errorf("AccountRecoverySetting = %+v, want one verified_email/priority-1 mechanism", pool.AccountRecoverySetting)
	}
	if pool.SmsConfiguration.SnsCallerArn != "arn:aws:iam::123456789012:role/SmsRole" {
		t.Errorf("SmsConfiguration.SnsCallerArn = %q, want arn:aws:iam::123456789012:role/SmsRole", pool.SmsConfiguration.SnsCallerArn)
	}
	if pool.SmsConfiguration.ExternalId != "ext-id" {
		t.Errorf("SmsConfiguration.ExternalId = %q, want ext-id", pool.SmsConfiguration.ExternalId)
	}
	if pool.SmsAuthenticationMessage != "Your authentication code is {####}" {
		t.Errorf("SmsAuthenticationMessage = %q", pool.SmsAuthenticationMessage)
	}
	if pool.SmsVerificationMessage != "Your verification code is {####}" {
		t.Errorf("SmsVerificationMessage = %q", pool.SmsVerificationMessage)
	}
	if pool.EmailVerificationMessage != "Your verification code is {####}" {
		t.Errorf("EmailVerificationMessage = %q", pool.EmailVerificationMessage)
	}
	if pool.EmailVerificationSubject != "Verify your account" {
		t.Errorf("EmailVerificationSubject = %q", pool.EmailVerificationSubject)
	}
	if pool.LambdaConfig.PreSignUp != "arn:aws:lambda:us-east-1:123456789012:function:pre-sign-up" {
		t.Errorf("LambdaConfig.PreSignUp = %q", pool.LambdaConfig.PreSignUp)
	}
	if pool.LambdaConfig.PostConfirmation != "arn:aws:lambda:us-east-1:123456789012:function:post-confirmation" {
		t.Errorf("LambdaConfig.PostConfirmation = %q", pool.LambdaConfig.PostConfirmation)
	}

	client := cognitoDescribeUserPoolClient(t, srv, poolID, clientID)
	if client.PreventUserExistenceErrors != "ENABLED" {
		t.Errorf("PreventUserExistenceErrors = %q, want ENABLED", client.PreventUserExistenceErrors)
	}
	if len(client.ReadAttributes) != 1 || client.ReadAttributes[0] != "email" {
		t.Errorf("ReadAttributes = %v, want [email]", client.ReadAttributes)
	}
	if len(client.WriteAttributes) != 1 || client.WriteAttributes[0] != "email" {
		t.Errorf("WriteAttributes = %v, want [email]", client.WriteAttributes)
	}
}

// The Definition of Done in #536 calls for the mutable properties to be
// reconciled on Update, not just forwarded on Create.
func TestUpdateStack_CognitoUserPoolPropertiesReconciled(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := func(smsMessage, preventErrors string) string {
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Pool": {
      "Type": "AWS::Cognito::UserPool",
      "Properties": {
        "UserPoolName": "cfn-reconcile-pool",
        "SmsAuthenticationMessage": "` + smsMessage + `",
        "AccountRecoverySetting": {"RecoveryMechanisms": [{"Name": "verified_phone_number", "Priority": 1}]}
      }
    },
    "Client": {
      "Type": "AWS::Cognito::UserPoolClient",
      "Properties": {
        "UserPoolId": {"Ref": "Pool"},
        "ClientName": "cfn-reconcile-client",
        "PreventUserExistenceErrors": "` + preventErrors + `"
      }
    }
  },
  "Outputs": {
    "PoolId": {"Value": {"Ref": "Pool"}},
    "ClientId": {"Value": {"Ref": "Client"}}
  }
}`
	}

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cognito-reconcile-stack"},
		"TemplateBody": {template("Your code is {####}", "LEGACY")},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "cognito-reconcile-stack", "CREATE_COMPLETE")

	firstBody := string(readBody(t, cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {"cognito-reconcile-stack"}})))
	poolID := cognitoStackOutput(firstBody, "PoolId")
	clientID := cognitoStackOutput(firstBody, "ClientId")
	if poolID == "" || clientID == "" {
		t.Fatalf("could not read PoolId/ClientId outputs after create: %s", firstBody)
	}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"cognito-reconcile-stack"},
		"TemplateBody": {template("Your new code is {####}", "ENABLED")},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatus(t, srv, "cognito-reconcile-stack", "UPDATE_COMPLETE")

	secondBody := string(readBody(t, cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {"cognito-reconcile-stack"}})))
	secondPoolID := cognitoStackOutput(secondBody, "PoolId")
	secondClientID := cognitoStackOutput(secondBody, "ClientId")
	if secondPoolID != poolID {
		t.Fatalf("pool replaced (%q -> %q) on a mutable-property update, want reconciled in place", poolID, secondPoolID)
	}
	if secondClientID != clientID {
		t.Fatalf("client replaced (%q -> %q) on a mutable-property update, want reconciled in place", clientID, secondClientID)
	}

	pool := cognitoDescribeUserPool(t, srv, poolID)
	if pool.SmsAuthenticationMessage != "Your new code is {####}" {
		t.Errorf("SmsAuthenticationMessage after update = %q, want the new message", pool.SmsAuthenticationMessage)
	}
	if len(pool.AccountRecoverySetting.RecoveryMechanisms) != 1 || pool.AccountRecoverySetting.RecoveryMechanisms[0].Name != "verified_phone_number" {
		t.Errorf("AccountRecoverySetting after update = %+v, want verified_phone_number", pool.AccountRecoverySetting)
	}

	client := cognitoDescribeUserPoolClient(t, srv, poolID, clientID)
	if client.PreventUserExistenceErrors != "ENABLED" {
		t.Errorf("PreventUserExistenceErrors after update = %q, want ENABLED", client.PreventUserExistenceErrors)
	}
}

// AliasAttributes has no UpdateUserPool member on real Cognito — it is
// create-only — so a template change to it must replace the pool rather than
// silently keep the old aliases.
func TestUpdateStack_CognitoUserPoolAliasAttributesRequiresReplacement(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := func(alias string) string {
		return `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Pool": {
      "Type": "AWS::Cognito::UserPool",
      "Properties": {
        "UserPoolName": "cfn-alias-replace-pool",
        "AliasAttributes": ["` + alias + `"]
      }
    }
  },
  "Outputs": {
    "PoolId": {"Value": {"Ref": "Pool"}}
  }
}`
	}

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cognito-alias-replace-stack"},
		"TemplateBody": {template("email")},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)
	waitForStackStatus(t, srv, "cognito-alias-replace-stack", "CREATE_COMPLETE")

	firstID := cognitoStackOutput(string(readBody(t, cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {"cognito-alias-replace-stack"}}))), "PoolId")
	if firstID == "" {
		t.Fatalf("could not read PoolId output after create")
	}

	ur := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"cognito-alias-replace-stack"},
		"TemplateBody": {template("preferred_username")},
	})
	defer ur.Body.Close()
	helpers.AssertStatus(t, ur, http.StatusOK)
	waitForStackStatusIn(t, srv, "cognito-alias-replace-stack", "UPDATE_COMPLETE")

	secondID := cognitoStackOutput(string(readBody(t, cfnQuery(t, srv, "DescribeStacks", url.Values{"StackName": {"cognito-alias-replace-stack"}}))), "PoolId")
	if secondID == "" {
		t.Fatalf("could not read PoolId output after update")
	}
	if secondID == firstID {
		t.Fatalf("pool id unchanged (%q) after an AliasAttributes update — replacement did not happen", firstID)
	}

	pool := cognitoDescribeUserPool(t, srv, secondID)
	if len(pool.AliasAttributes) != 1 || pool.AliasAttributes[0] != "preferred_username" {
		t.Errorf("AliasAttributes after replacement = %v, want [preferred_username]", pool.AliasAttributes)
	}
}
