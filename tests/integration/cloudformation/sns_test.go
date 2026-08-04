package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

func snsQuery(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	if params == nil {
		params = url.Values{}
	}
	params.Set("Action", action)
	params.Set("Version", "2010-03-31")
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(params.Encode()))
	if err != nil {
		t.Fatalf("build SNS %s request: %v", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SNS %s request: %v", action, err)
	}
	return resp
}

type snsAttributesResponse struct {
	Result struct {
		Attributes []struct {
			Key   string `xml:"key"`
			Value string `xml:"value"`
		} `xml:"Attributes>entry"`
	} `xml:"GetSubscriptionAttributesResult"`
}

type snsSubscriptionsResponse struct {
	Result struct {
		Subscriptions []struct {
			SubscriptionARN string `xml:"SubscriptionArn"`
		} `xml:"Subscriptions>member"`
	} `xml:"ListSubscriptionsByTopicResult"`
}

type snsTopicAttributesResponse struct {
	Result struct {
		Attributes []struct {
			Key   string `xml:"key"`
			Value string `xml:"value"`
		} `xml:"Attributes>entry"`
	} `xml:"GetTopicAttributesResult"`
}

func snsTopicAttributes(t *testing.T, srv *helpers.TestServer, topicARN string) map[string]string {
	t.Helper()
	resp := snsQuery(t, srv, "GetTopicAttributes", url.Values{"TopicArn": {topicARN}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var decoded snsTopicAttributesResponse
	helpers.DecodeXML(t, resp, &decoded)
	attrs := make(map[string]string, len(decoded.Result.Attributes))
	for _, entry := range decoded.Result.Attributes {
		attrs[entry.Key] = entry.Value
	}
	return attrs
}

func snsAttributes(t *testing.T, srv *helpers.TestServer, subscriptionARN string) map[string]string {
	t.Helper()
	resp := snsQuery(t, srv, "GetSubscriptionAttributes", url.Values{"SubscriptionArn": {subscriptionARN}})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	var decoded snsAttributesResponse
	helpers.DecodeXML(t, resp, &decoded)
	attrs := make(map[string]string, len(decoded.Result.Attributes))
	for _, entry := range decoded.Result.Attributes {
		attrs[entry.Key] = entry.Value
	}
	return attrs
}

const snsSubscriptionAttributesTemplate = `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "FilteredQueue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "cfn-sns-filtered-raw"}
    },
    "NotificationTopic": {
      "Type": "AWS::SNS::Topic",
      "Properties": {
        "TopicName": "cfn-sns-attributes",
        "DisplayName": "CFN notifications",
        "KmsMasterKeyId": "alias/aws/sns"
      }
    },
    "FilteredSubscription": {
      "Type": "AWS::SNS::Subscription",
      "Properties": {
        "TopicArn": {"Ref": "NotificationTopic"},
        "Endpoint": {"Fn::GetAtt": ["FilteredQueue", "Arn"]},
        "Protocol": "sqs",
        "FilterPolicy": {"eventType": ["accepted"]},
        "FilterPolicyScope": "MessageBody",
        "RawMessageDelivery": true,
        "RedrivePolicy": {"deadLetterTargetArn": "arn:aws:sqs:us-east-1:000000000000:cfn-sns-dlq"},
        "DeliveryPolicy": {"healthyRetryPolicy": {"numRetries": 3}},
        "SubscriptionRoleArn": "arn:aws:iam::000000000000:role/ignored-by-sqs"
      }
    }
  }
}`

func TestCreateStack_SNSSubscriptionAttributesFilterAndRawDelivery(t *testing.T) {
	// Given: a CloudFormation stack with an SNS topic and a filtered raw SQS subscription.
	srv := helpers.NewTestServer(t)
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"sns-attributes-stack"},
		"TemplateBody": {snsSubscriptionAttributesTemplate},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "sns-attributes-stack", "CREATE_COMPLETE")

	topicARN := "arn:aws:sns:us-east-1:000000000000:cfn-sns-attributes"
	queueURL := srv.URL + "/000000000000/cfn-sns-filtered-raw"
	list := snsQuery(t, srv, "ListSubscriptionsByTopic", url.Values{"TopicArn": {topicARN}})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var subscriptions snsSubscriptionsResponse
	helpers.DecodeXML(t, list, &subscriptions)
	if len(subscriptions.Result.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %#v, want exactly one", subscriptions.Result.Subscriptions)
	}
	subscriptionARN := subscriptions.Result.Subscriptions[0].SubscriptionARN
	topicAttrs := snsTopicAttributes(t, srv, topicARN)
	if topicAttrs["DisplayName"] != "CFN notifications" || topicAttrs["KmsMasterKeyId"] != "alias/aws/sns" {
		t.Fatalf("topic attributes = %#v, want deployed DisplayName and KmsMasterKeyId", topicAttrs)
	}

	// Then: CloudFormation has applied every subscription attribute through SNS.
	attrs := snsAttributes(t, srv, subscriptionARN)
	if attrs["FilterPolicy"] != `{"eventType":["accepted"]}` {
		t.Fatalf("FilterPolicy = %q, want configured JSON", attrs["FilterPolicy"])
	}
	for name, want := range map[string]string{
		"FilterPolicyScope":   "MessageBody",
		"RawMessageDelivery":  "true",
		"RedrivePolicy":       `{"deadLetterTargetArn":"arn:aws:sqs:us-east-1:000000000000:cfn-sns-dlq"}`,
		"DeliveryPolicy":      `{"healthyRetryPolicy":{"numRetries":3}}`,
		"SubscriptionRoleArn": "arn:aws:iam::000000000000:role/ignored-by-sqs",
	} {
		if got := attrs[name]; got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}

	// When: SNS publishes a non-matching event followed by a matching event.
	for _, message := range []string{
		`{"eventType":"rejected","body":"not-delivered"}`,
		`{"eventType":"accepted","body":"raw-delivered"}`,
	} {
		publish := snsQuery(t, srv, "Publish", url.Values{
			"TopicArn": {topicARN},
			"Message":  {message},
		})
		helpers.AssertStatus(t, publish, http.StatusOK)
		publish.Body.Close()
	}

	// Then: only the matching message reaches SQS and raw delivery omits the SNS envelope.
	var messages []struct {
		Body string `json:"Body"`
	}
	helpers.Eventually(t, 5*time.Second, 20*time.Millisecond, func() bool {
		receive := sqsJSONCall(t, srv, "ReceiveMessage", map[string]any{
			"QueueUrl":            queueURL,
			"MaxNumberOfMessages": 10,
		})
		defer receive.Body.Close()
		helpers.AssertStatus(t, receive, http.StatusOK)
		var result struct {
			Messages []struct {
				Body string `json:"Body"`
			} `json:"Messages"`
		}
		helpers.DecodeJSON(t, receive, &result)
		if len(result.Messages) == 0 {
			return false
		}
		messages = result.Messages
		return true
	}, "timed out waiting for SNS delivery")
	if len(messages) != 1 || messages[0].Body != `{"eventType":"accepted","body":"raw-delivered"}` {
		t.Fatalf("received messages = %#v, want exactly the matching raw message", messages)
	}
}

func TestCreateStack_SNSTopicInlineSubscription(t *testing.T) {
	// Given: a topic with the CloudFormation inline Subscription property.
	srv := helpers.NewTestServer(t)
	template := `{
  "AWSTemplateFormatVersion": "2010-09-09",
  "Resources": {
    "Queue": {
      "Type": "AWS::SQS::Queue",
      "Properties": {"QueueName": "cfn-sns-inline-subscription"}
    },
    "Topic": {
      "Type": "AWS::SNS::Topic",
      "Properties": {
        "TopicName": "cfn-sns-inline-topic",
        "Subscription": [{
          "Protocol": "sqs",
          "Endpoint": {"Fn::GetAtt": ["Queue", "Arn"]}
        }]
      }
    }
  }
}`
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    {"sns-inline-subscription-stack"},
		"TemplateBody": {template},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, "sns-inline-subscription-stack", "CREATE_COMPLETE")

	// When: the SNS topic is inspected after the stack deploys.
	list := snsQuery(t, srv, "ListSubscriptionsByTopic", url.Values{
		"TopicArn": {"arn:aws:sns:us-east-1:000000000000:cfn-sns-inline-topic"},
	})
	defer list.Body.Close()
	helpers.AssertStatus(t, list, http.StatusOK)
	var subscriptions snsSubscriptionsResponse
	helpers.DecodeXML(t, list, &subscriptions)

	// Then: the endpoint in the nested resource was subscribed through SNS.
	if len(subscriptions.Result.Subscriptions) != 1 {
		t.Fatalf("subscriptions = %#v, want one inline SQS subscription", subscriptions.Result.Subscriptions)
	}
}

func TestUpdateStack_SNSTopicAttributes(t *testing.T) {
	// Given: a stack with SNS topic attributes applied during creation.
	srv := helpers.NewTestServer(t)
	stackName := "sns-topic-attribute-update-stack"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Topic": {
      "Type": "AWS::SNS::Topic",
      "Properties": {
        "TopicName": "cfn-sns-update-attributes",
        "DisplayName": "before",
        "KmsMasterKeyId": "alias/first"
      }
    }
  }
}`},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: CloudFormation updates both mutable topic attributes.
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Topic": {
      "Type": "AWS::SNS::Topic",
      "Properties": {
        "TopicName": "cfn-sns-update-attributes",
        "DisplayName": "after",
        "KmsMasterKeyId": "alias/second"
      }
    }
  }
}`},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "UPDATE_COMPLETE")

	// Then: SNS is the source of truth for the updated values.
	attrs := snsTopicAttributes(t, srv, "arn:aws:sns:us-east-1:000000000000:cfn-sns-update-attributes")
	if attrs["DisplayName"] != "after" || attrs["KmsMasterKeyId"] != "alias/second" {
		t.Fatalf("topic attributes = %#v, want updated values", attrs)
	}
}

func TestUpdateStack_SNSTopicInlineSubscriptionsFailLoudly(t *testing.T) {
	// Given: a topic whose initial inline subscription list is empty.
	srv := helpers.NewTestServer(t)
	stackName := "sns-inline-subscription-update-stack"
	create := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Topic": {
      "Type": "AWS::SNS::Topic",
      "Properties": {
        "TopicName": "cfn-sns-inline-update",
        "Subscription": []
      }
    }
  }
}`},
	})
	defer create.Body.Close()
	helpers.AssertStatus(t, create, http.StatusOK)
	waitForStackStatus(t, srv, stackName, "CREATE_COMPLETE")

	// When: the template changes the inline subscriptions.
	update := cfnQuery(t, srv, "UpdateStack", url.Values{
		"StackName": {stackName},
		"TemplateBody": {`{
  "Resources": {
    "Topic": {
      "Type": "AWS::SNS::Topic",
      "Properties": {
        "TopicName": "cfn-sns-inline-update",
        "Subscription": [{
          "Protocol": "http",
          "Endpoint": "http://example.test/subscription"
        }]
      }
    }
  }
}`},
	})
	defer update.Body.Close()
	helpers.AssertStatus(t, update, http.StatusOK)

	// Then: the update fails rather than reporting success with the old list.
	status := waitForStackStatusIn(t, srv, stackName,
		"UPDATE_FAILED", "UPDATE_ROLLBACK_COMPLETE", "UPDATE_ROLLBACK_FAILED", "UPDATE_COMPLETE")
	if status == "UPDATE_COMPLETE" {
		t.Fatal("stack reached UPDATE_COMPLETE after an unsupported inline subscription update")
	}
}
