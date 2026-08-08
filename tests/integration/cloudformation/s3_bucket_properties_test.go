package cloudformation_test

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

const s3BucketPropertiesCreateTemplate = `{
  "Resources": {
    "Bucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "BucketName": "cfn-s3-properties",
        "LifecycleConfiguration": {
          "Rules": [{
            "Id": "archive-logs",
            "Prefix": "logs/",
            "Status": "Enabled",
            "ExpirationInDays": 30,
            "Transitions": [{"StorageClass": "GLACIER", "TransitionInDays": 7}]
          }]
        },
        "VersioningConfiguration": {"Status": "Enabled"},
        "NotificationConfiguration": {
          "QueueConfigurations": [{
            "Event": "s3:ObjectCreated:*",
            "Queue": "arn:aws:sqs:us-east-1:000000000000:cfn-events",
            "Filter": {"S3Key": {"Rules": [{"Name": "prefix", "Value": "incoming/"}]}}
          }]
        },
        "BucketEncryption": {
          "ServerSideEncryptionConfiguration": [{
            "BucketKeyEnabled": true,
            "ServerSideEncryptionByDefault": {
              "SSEAlgorithm": "aws:kms",
              "KMSMasterKeyID": "alias/cfn-key"
            }
          }]
        },
        "Tags": [
          {"Key": "environment", "Value": "test"},
          {"Key": "owner", "Value": "cloudformation"}
        ],
        "CorsConfiguration": {
          "CorsRules": [{
            "AllowedHeaders": ["*"],
            "AllowedMethods": ["GET", "PUT"],
            "AllowedOrigins": ["https://example.test"],
            "ExposedHeaders": ["ETag"],
            "MaxAge": 3600
          }]
        },
        "WebsiteConfiguration": {
          "IndexDocument": "index.html",
          "ErrorDocument": "error.html"
        }
      }
    }
  }
}`

const s3BucketPropertiesUpdateTemplate = `{
  "Resources": {
    "Bucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {
        "BucketName": "cfn-s3-properties",
        "LifecycleConfiguration": {
          "Rules": [{
            "Id": "expire-archive",
            "Prefix": "archive/",
            "Status": "Disabled",
            "ExpirationInDays": 90
          }]
        },
        "VersioningConfiguration": {"Status": "Suspended"},
        "NotificationConfiguration": {
          "TopicConfigurations": [{
            "Event": "s3:ObjectRemoved:*",
            "Topic": "arn:aws:sns:us-east-1:000000000000:cfn-events"
          }]
        },
        "BucketEncryption": {
          "ServerSideEncryptionConfiguration": [{
            "ServerSideEncryptionByDefault": {"SSEAlgorithm": "AES256"}
          }]
        },
        "Tags": [{"Key": "environment", "Value": "updated"}],
        "CorsConfiguration": {
          "CorsRules": [{
            "AllowedMethods": ["POST"],
            "AllowedOrigins": ["https://updated.example.test"]
          }]
        },
        "WebsiteConfiguration": {
          "IndexDocument": "home.html",
          "ErrorDocument": "404.html"
        }
      }
    }
  }
}`

const s3BucketPropertiesRemovalTemplate = `{
  "Resources": {
    "Bucket": {
      "Type": "AWS::S3::Bucket",
      "Properties": {"BucketName": "cfn-s3-properties"}
    }
  }
}`

func TestS3BucketProperties_createDispatchesSubresourceAPIs(t *testing.T) {
	// Given: a CloudFormation stack containing all supported S3 bucket sub-resources
	srv := helpers.NewTestServer(t)

	// When: the stack is created
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cfn-s3-properties-create"},
		"TemplateBody": {s3BucketPropertiesCreateTemplate},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-properties-create", "CREATE_COMPLETE")

	// Then: each value is visible through the existing S3 read API
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "lifecycle", http.StatusOK,
		"<ID>archive-logs</ID>", "<Prefix>logs/</Prefix>", "<Days>30</Days>", "<StorageClass>GLACIER</StorageClass>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "versioning", http.StatusOK,
		"<Status>Enabled</Status>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "notification", http.StatusOK,
		"<Queue>arn:aws:sqs:us-east-1:000000000000:cfn-events</Queue>", "<Event>s3:ObjectCreated:*</Event>", "<Value>incoming/</Value>")
	assertS3SubresourceExcludes(t, srv, "cfn-s3-properties", "notification", "<Id>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "encryption", http.StatusOK,
		"<SSEAlgorithm>aws:kms</SSEAlgorithm>", "<KMSMasterKeyID>alias/cfn-key</KMSMasterKeyID>", "<BucketKeyEnabled>true</BucketKeyEnabled>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "tagging", http.StatusOK,
		`xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`, "<Key>environment</Key>", "<Value>test</Value>", "<Key>owner</Key>", "<Value>cloudformation</Value>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "cors", http.StatusOK,
		`xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`, "<AllowedMethod>GET</AllowedMethod>", "<AllowedMethod>PUT</AllowedMethod>", "<AllowedOrigin>https://example.test</AllowedOrigin>", "<MaxAgeSeconds>3600</MaxAgeSeconds>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "website", http.StatusOK,
		`xmlns="http://s3.amazonaws.com/doc/2006-03-01/"`, "<Suffix>index.html</Suffix>", "<Key>error.html</Key>")
}

func TestS3BucketProperties_updateAndRemovalDispatchSubresourceAPIs(t *testing.T) {
	// Given: a stack with every supported S3 bucket sub-resource configured
	srv := helpers.NewTestServer(t)
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cfn-s3-properties-update"},
		"TemplateBody": {s3BucketPropertiesCreateTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-properties-update", "CREATE_COMPLETE")

	// When: all seven sub-resources are updated in place
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"cfn-s3-properties-update"},
		"TemplateBody": {s3BucketPropertiesUpdateTemplate},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-properties-update", "UPDATE_COMPLETE")

	// Then: S3 exposes the updated configurations on the same bucket
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "lifecycle", http.StatusOK,
		"<ID>expire-archive</ID>", "<Prefix>archive/</Prefix>", "<Status>Disabled</Status>", "<Days>90</Days>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "versioning", http.StatusOK,
		"<Status>Suspended</Status>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "notification", http.StatusOK,
		"<Topic>arn:aws:sns:us-east-1:000000000000:cfn-events</Topic>", "<Event>s3:ObjectRemoved:*</Event>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "encryption", http.StatusOK,
		"<SSEAlgorithm>AES256</SSEAlgorithm>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "tagging", http.StatusOK,
		"<Key>environment</Key>", "<Value>updated</Value>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "cors", http.StatusOK,
		"<AllowedMethod>POST</AllowedMethod>", "<AllowedOrigin>https://updated.example.test</AllowedOrigin>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "website", http.StatusOK,
		"<Suffix>home.html</Suffix>", "<Key>404.html</Key>")

	// When: the properties are removed from the template
	remove := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName":    {"cfn-s3-properties-update"},
		"TemplateBody": {s3BucketPropertiesRemovalTemplate},
	})
	defer remove.Body.Close()
	helpers.AssertStatus(t, remove, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-properties-update", "UPDATE_COMPLETE")

	// Then: deletable configurations are absent, notifications and tags are empty,
	// and versioning is suspended because S3 cannot return a versioned bucket to
	// its initial unversioned state.
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "lifecycle", http.StatusNotFound,
		"<Code>NoSuchLifecycleConfiguration</Code>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "versioning", http.StatusOK,
		"<Status>Suspended</Status>")
	assertS3SubresourceExcludes(t, srv, "cfn-s3-properties", "notification", "<QueueConfiguration>", "<TopicConfiguration>", "<CloudFunctionConfiguration>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "encryption", http.StatusOK,
		"<SSEAlgorithm>AES256</SSEAlgorithm>")
	assertS3SubresourceExcludes(t, srv, "cfn-s3-properties", "encryption", "<KMSMasterKeyID>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "tagging", http.StatusNotFound,
		"<Code>NoSuchTagSet</Code>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "cors", http.StatusNotFound,
		"<Code>NoSuchCORSConfiguration</Code>")
	assertS3SubresourceContains(t, srv, "cfn-s3-properties", "website", http.StatusNotFound,
		"<Code>NoSuchWebsiteConfiguration</Code>")
}

func TestS3BucketProperties_unknownAndUntranslatablePropertiesFailBeforeBucketCreation(t *testing.T) {
	tests := []struct {
		name       string
		stackName  string
		bucketName string
		property   string
		wantReason string
	}{
		{
			name:       "unknown root property",
			stackName:  "cfn-s3-unknown-property",
			bucketName: "cfn-s3-unknown-property",
			property:   `"MysteryConfiguration": {}`,
			wantReason: "MysteryConfiguration",
		},
		{
			name:       "non-private access control without S3 ACL support",
			stackName:  "cfn-s3-unsupported-acl",
			bucketName: "cfn-s3-unsupported-acl",
			property:   `"AccessControl": "PublicRead"`,
			wantReason: `AccessControl &#34;PublicRead&#34; is not supported`,
		},
		{
			name:       "non-default public access block without S3 support",
			stackName:  "cfn-s3-unsupported-public-access-block",
			bucketName: "cfn-s3-unsupported-public-access-block",
			property:   `"PublicAccessBlockConfiguration":{"BlockPublicAcls":false,"BlockPublicPolicy":true,"IgnorePublicAcls":true,"RestrictPublicBuckets":true}`,
			wantReason: "PublicAccessBlockConfiguration is not supported unless all settings are true",
		},
		{
			name:       "known shape unsupported by S3 codec",
			stackName:  "cfn-s3-untranslatable-property",
			bucketName: "cfn-s3-untranslatable-property",
			property:   `"WebsiteConfiguration": {"RoutingRules": []}`,
			wantReason: "RoutingRules cannot be represented by the current S3 website handler",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given: a fresh service and a template property that cannot be translated
			srv := helpers.NewTestServer(t)
			template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"` + tt.bucketName + `",` + tt.property + `}}}}`

			// When: CloudFormation creates the stack
			resp := cfnQuery(t, srv, "CreateStack", url.Values{
				"StackName":    {tt.stackName},
				"TemplateBody": {template},
			})
			defer resp.Body.Close()
			helpers.AssertStatus(t, resp, http.StatusOK)
			waitForStackStatus(t, srv, tt.stackName, "ROLLBACK_COMPLETE")
			events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {tt.stackName}})
			defer events.Body.Close()
			eventBody := string(readBody(t, events))
			if !strings.Contains(eventBody, tt.wantReason) {
				t.Fatalf("stack events do not contain loud translation failure %q: %s", tt.wantReason, eventBody)
			}

			// Then: translation fails loudly before CreateBucket is dispatched
			bucket, err := http.Get(srv.URL + "/" + tt.bucketName)
			if err != nil {
				t.Fatalf("probe bucket: %v", err)
			}
			defer bucket.Body.Close()
			helpers.AssertStatus(t, bucket, http.StatusNotFound)
		})
	}
}

func TestS3BucketProperties_explicitPrivateAccessControlUsesCreateBucketDefault(t *testing.T) {
	// Given: CDK and ordinary CloudFormation templates commonly spell out the
	// same private ACL that S3 applies to every newly-created bucket by default.
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-private-acl","AccessControl":"Private"}}}}`

	// When: CloudFormation creates the bucket.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cfn-s3-private-acl"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-private-acl", "CREATE_COMPLETE")

	// Then: the explicit default is accepted without introducing ACL behavior
	// into the CloudFormation handler.
	bucket, err := http.Get(srv.URL + "/cfn-s3-private-acl")
	if err != nil {
		t.Fatalf("probe bucket: %v", err)
	}
	defer bucket.Body.Close()
	helpers.AssertStatus(t, bucket, http.StatusOK)
}

func TestS3BucketProperties_explicitPublicAccessBlockUsesCreateBucketDefault(t *testing.T) {
	// Given: CDK bootstrap explicitly states the public-access protections AWS
	// applies to every newly-created general-purpose bucket by default.
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-default-public-access-block","PublicAccessBlockConfiguration":{"BlockPublicAcls":true,"BlockPublicPolicy":true,"IgnorePublicAcls":true,"RestrictPublicBuckets":true}}}}}`

	// When: CloudFormation creates the bucket.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cfn-s3-default-public-access-block"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-default-public-access-block", "CREATE_COMPLETE")

	// Then: the explicit default is accepted without moving service state or
	// policy enforcement into CloudFormation.
	bucket, err := http.Get(srv.URL + "/cfn-s3-default-public-access-block")
	if err != nil {
		t.Fatalf("probe bucket: %v", err)
	}
	defer bucket.Body.Close()
	helpers.AssertStatus(t, bucket, http.StatusOK)
}

func TestS3BucketProperties_validationErrorsComeFromS3(t *testing.T) {
	// Given: a stack whose translated VersioningConfiguration violates the S3 API enum
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-invalid-versioning","VersioningConfiguration":{"Status":"Invalid"}}}}}`

	// When: CloudFormation creates the stack without automatic rollback
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":       {"cfn-s3-validation-owner"},
		"TemplateBody":    {template},
		"DisableRollback": {"true"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-validation-owner", "CREATE_FAILED")

	// Then: the stack event contains the REST-XML error emitted by S3 itself
	events := cfnQuery(t, srv, "DescribeStackEvents", url.Values{"StackName": {"cfn-s3-validation-owner"}})
	defer events.Body.Close()
	body := string(readBody(t, events))
	for _, fragment := range []string{"s3 PutBucketVersioning", "HTTP 400", "MalformedXML", "did not validate against our published schema"} {
		if !strings.Contains(body, fragment) {
			t.Errorf("stack events %q do not contain S3 validation fragment %q", body, fragment)
		}
	}
	resources := cfnQuery(t, srv, "DescribeStackResources", url.Values{"StackName": {"cfn-s3-validation-owner"}})
	defer resources.Body.Close()
	if got := string(readBody(t, resources)); !strings.Contains(got, "cfn-s3-invalid-versioning") {
		t.Fatalf("failed partial create lost its physical ID: %s", got)
	}
}

func TestS3BucketProperties_failedPartialCreateIsDeletedByRollback(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-partial-create","VersioningConfiguration":{"Status":"Invalid"}}}}}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {"cfn-s3-partial-create"}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-partial-create", "ROLLBACK_COMPLETE")
	probe, err := http.Get(srv.URL + "/cfn-s3-partial-create")
	if err != nil {
		t.Fatalf("probe rolled-back bucket: %v", err)
	}
	defer probe.Body.Close()
	helpers.AssertStatus(t, probe, http.StatusNotFound)
}

func TestS3BucketProperties_numberParametersTranslateAtTheCFNBoundary(t *testing.T) {
	srv := helpers.NewTestServer(t)
	template := `{"Parameters":{"Days":{"Type":"Number","Default":14},"MaxAge":{"Type":"Number","Default":900}},"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-number-params","LifecycleConfiguration":{"Rules":[{"Id":"expiry","Status":"Enabled","ExpirationInDays":{"Ref":"Days"}}]},"CorsConfiguration":{"CorsRules":[{"AllowedMethods":["GET"],"AllowedOrigins":["*"],"MaxAge":{"Ref":"MaxAge"}}]}}}}}`
	resp := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {"cfn-s3-number-params"}, "TemplateBody": {template}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-number-params", "CREATE_COMPLETE")
	assertS3SubresourceContains(t, srv, "cfn-s3-number-params", "lifecycle", http.StatusOK, "<Days>14</Days>")
	assertS3SubresourceContains(t, srv, "cfn-s3-number-params", "cors", http.StatusOK, "<MaxAgeSeconds>900</MaxAgeSeconds>")
}

func TestS3BucketProperties_noncurrentExpirationDispatchesThroughS3(t *testing.T) {
	// Given: the same noncurrent expiration shape CDK bootstrap emits.
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-noncurrent-expiration","LifecycleConfiguration":{"Rules":[{"Id":"retain-assets","Prefix":"","Status":"Enabled","NoncurrentVersionExpiration":{"NoncurrentDays":30,"NewerNoncurrentVersions":5}}]}}}}}`

	// When: CloudFormation creates the stack.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cfn-s3-noncurrent-expiration"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-noncurrent-expiration", "CREATE_COMPLETE")

	// Then: the underlying S3 API owns and exposes the translated action.
	assertS3SubresourceContains(t, srv, "cfn-s3-noncurrent-expiration", "lifecycle", http.StatusOK,
		"<NoncurrentVersionExpiration>",
		"<NoncurrentDays>30</NoncurrentDays>",
		"<NewerNoncurrentVersions>5</NewerNoncurrentVersions>")
}

func TestS3BucketProperties_legacyNoncurrentExpirationDaysDispatchesThroughS3(t *testing.T) {
	// Given: the deprecated scalar property that CloudFormation still accepts.
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-legacy-noncurrent-expiration","LifecycleConfiguration":{"Rules":[{"Id":"expire-old-versions","Prefix":"","Status":"Enabled","NoncurrentVersionExpirationInDays":30}]}}}}}`

	// When: CloudFormation creates the stack.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cfn-s3-legacy-noncurrent-expiration"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-legacy-noncurrent-expiration", "CREATE_COMPLETE")

	// Then: the adapter maps the legacy scalar to S3's current action shape.
	assertS3SubresourceContains(t, srv, "cfn-s3-legacy-noncurrent-expiration", "lifecycle", http.StatusOK,
		"<NoncurrentVersionExpiration>",
		"<NoncurrentDays>30</NoncurrentDays>")
}

func TestS3BucketProperties_transitionDefaultMinimumObjectSizeDispatchesThroughS3(t *testing.T) {
	// Given: LifecycleConfiguration.TransitionDefaultMinimumObjectSize, which
	// maps to S3's x-amz-transition-default-minimum-object-size request header
	// rather than to anything in the body.
	srv := helpers.NewTestServer(t)
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-transition-minimum","LifecycleConfiguration":{"TransitionDefaultMinimumObjectSize":"varies_by_storage_class","Rules":[{"Id":"chill","Prefix":"","Status":"Enabled","Transitions":[{"StorageClass":"GLACIER","TransitionInDays":7}]}]}}}}}`

	// When: CloudFormation creates the stack.
	resp := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"cfn-s3-transition-minimum"},
		"TemplateBody": {template},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	waitForStackStatus(t, srv, "cfn-s3-transition-minimum", "CREATE_COMPLETE")

	// Then: S3 owns the setting and reports it on GetBucketLifecycleConfiguration.
	get, err := http.Get(srv.URL + "/cfn-s3-transition-minimum?lifecycle")
	if err != nil {
		t.Fatalf("GetBucketLifecycleConfiguration: %v", err)
	}
	defer get.Body.Close()
	helpers.AssertStatus(t, get, http.StatusOK)
	if got := get.Header.Get("x-amz-transition-default-minimum-object-size"); got != "varies_by_storage_class" {
		t.Fatalf("x-amz-transition-default-minimum-object-size = %q, want varies_by_storage_class", got)
	}
}

func TestS3BucketProperties_removingExplicitNameReplacesBucket(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "cfn-s3-name-replacement"
	createTemplate := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-explicit-name"}}}}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {createTemplate}})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	updateTemplate := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{}}}}`
	update := cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {updateTemplate}})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")
	if got := stackResourcePhysicalID(t, srv, stackName, "Bucket"); got == "cfn-s3-explicit-name" || got == "" {
		t.Fatalf("BucketName removal did not replace the bucket: %q", got)
	}
	probe, err := http.Get(srv.URL + "/cfn-s3-explicit-name")
	if err != nil {
		t.Fatalf("probe replaced bucket: %v", err)
	}
	defer probe.Body.Close()
	helpers.AssertStatus(t, probe, http.StatusNotFound)
}

func TestS3BucketProperties_laterResourceFailureRollsBackSuccessfulInPlaceUpdate(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "cfn-s3-stack-rollback"
	createTemplate := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-stack-rollback","LifecycleConfiguration":{"Rules":[{"Id":"original","Status":"Enabled","ExpirationInDays":7}]}}},"Later":{"Type":"AWS::S3::Bucket","DependsOn":"Bucket","Properties":{"BucketName":"cfn-s3-stack-rollback-later","VersioningConfiguration":{"Status":"Enabled"}}}}}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {createTemplate}})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	updateTemplate := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-stack-rollback","LifecycleConfiguration":{"Rules":[{"Id":"attempted","Status":"Enabled","ExpirationInDays":30}]}}},"Later":{"Type":"AWS::S3::Bucket","DependsOn":"Bucket","Properties":{"BucketName":"cfn-s3-stack-rollback-later","VersioningConfiguration":{"Status":"Invalid"}}}}}`
	update := cfnQuery(t, srv, "UpdateStack", url.Values{"StackName": {stackName}, "TemplateBody": {updateTemplate}})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")
	assertS3SubresourceContains(t, srv, "cfn-s3-stack-rollback", "lifecycle", http.StatusOK, "<ID>original</ID>", "<Days>7</Days>")
	assertS3SubresourceExcludes(t, srv, "cfn-s3-stack-rollback", "lifecycle", "<ID>attempted</ID>")
}

func TestS3BucketProperties_nonEmptyBucketBlocksStackDeletion(t *testing.T) {
	srv := helpers.NewTestServer(t)
	stackName := "cfn-s3-delete-blocked"
	template := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-delete-blocked"}}}}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{"StackName": {stackName}, "TemplateBody": {template}})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")
	s3PutObject(t, srv, "cfn-s3-delete-blocked", "keep.txt", "not empty")
	remove := cfnQuery(t, srv, "DeleteStack", url.Values{"StackName": {stackName}})
	defer remove.Body.Close()
	helpers.AssertStatus(t, remove, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "DELETE_FAILED")
}

func TestS3BucketProperties_failedUpdateRestoresEarlierSubresourceChanges(t *testing.T) {
	// Given: a stack with lifecycle and versioning configurations
	srv := helpers.NewTestServer(t)
	stackName := "cfn-s3-update-compensation"
	createTemplate := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-update-compensation","LifecycleConfiguration":{"Rules":[{"Id":"original","Prefix":"old/","Status":"Enabled","ExpirationInDays":7}]},"VersioningConfiguration":{"Status":"Enabled"}}}}}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {createTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: lifecycle changes before S3 rejects a later versioning update
	updateTemplate := `{"Resources":{"Bucket":{"Type":"AWS::S3::Bucket","Properties":{"BucketName":"cfn-s3-update-compensation","LifecycleConfiguration":{"Rules":[{"Id":"attempted","Prefix":"new/","Status":"Enabled","ExpirationInDays":30}]},"VersioningConfiguration":{"Status":"Invalid"}}}}}`
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName}, "TemplateBody": {updateTemplate},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_ROLLBACK_COMPLETE")

	// Then: the handler compensates through S3 and restores the previous lifecycle
	assertS3SubresourceContains(t, srv, "cfn-s3-update-compensation", "lifecycle", http.StatusOK,
		"<ID>original</ID>", "<Prefix>old/</Prefix>", "<Days>7</Days>")
	assertS3SubresourceExcludes(t, srv, "cfn-s3-update-compensation", "lifecycle", "<ID>attempted</ID>")
}

func assertS3SubresourceContains(t *testing.T, srv *helpers.TestServer, bucket, subresource string, wantStatus int, fragments ...string) {
	t.Helper()
	body := readS3Subresource(t, srv, bucket, subresource, wantStatus)
	for _, fragment := range fragments {
		if !strings.Contains(body, fragment) {
			t.Errorf("GET /%s?%s body %q does not contain %q", bucket, subresource, body, fragment)
		}
	}
}

func assertS3SubresourceExcludes(t *testing.T, srv *helpers.TestServer, bucket, subresource string, fragments ...string) {
	t.Helper()
	body := readS3Subresource(t, srv, bucket, subresource, http.StatusOK)
	for _, fragment := range fragments {
		if strings.Contains(body, fragment) {
			t.Errorf("GET /%s?%s body %q unexpectedly contains %q", bucket, subresource, body, fragment)
		}
	}
}

func readS3Subresource(t *testing.T, srv *helpers.TestServer, bucket, subresource string, wantStatus int) string {
	t.Helper()
	resp, err := http.Get(srv.URL + "/" + bucket + "?" + subresource)
	if err != nil {
		t.Fatalf("GET /%s?%s: %v", bucket, subresource, err)
	}
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, wantStatus)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read GET /%s?%s: %v", bucket, subresource, err)
	}
	return string(body)
}

func stackResourcePhysicalID(t *testing.T, srv *helpers.TestServer, stackName, logicalID string) string {
	t.Helper()
	resp := cfnQuery(t, srv, "DescribeStackResources", url.Values{"StackName": {stackName}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	body := readBody(t, resp)
	var result struct {
		Resources []struct {
			LogicalID  string `xml:"LogicalResourceId"`
			PhysicalID string `xml:"PhysicalResourceId"`
		} `xml:"DescribeStackResourcesResult>StackResources>member"`
	}
	if err := xml.Unmarshal(body, &result); err != nil {
		t.Fatalf("decode DescribeStackResources: %v\n%s", err, body)
	}
	for _, resource := range result.Resources {
		if resource.LogicalID == logicalID {
			return resource.PhysicalID
		}
	}
	return ""
}
