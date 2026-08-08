package sns_test

import (
	"net/url"
	"testing"

	"github.com/Neaox/overcast/tests/helpers"
)

// Real SNS tag operations answer a missing topic with error code
// "ResourceNotFound" — unlike the topic operations, which use "NotFound".

func TestTagResource_missingTopicErrorCode(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "TagResource", url.Values{
		"ResourceArn":      {"arn:aws:sns:us-east-1:000000000000:absent-topic"},
		"Tags.Tag.1.Key":   {"env"},
		"Tags.Tag.1.Value": {"prod"},
	})
	defer resp.Body.Close()
	helpers.AssertQueryXMLError(t, resp, "ResourceNotFound")
}

func TestUntagResource_missingTopicErrorCode(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "UntagResource", url.Values{
		"ResourceArn":      {"arn:aws:sns:us-east-1:000000000000:absent-topic"},
		"TagKeys.member.1": {"env"},
	})
	defer resp.Body.Close()
	helpers.AssertQueryXMLError(t, resp, "ResourceNotFound")
}

func TestListTagsForResource_missingTopicErrorCode(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "ListTagsForResource", url.Values{
		"ResourceArn": {"arn:aws:sns:us-east-1:000000000000:absent-topic"},
	})
	defer resp.Body.Close()
	helpers.AssertQueryXMLError(t, resp, "ResourceNotFound")
}

// TestGetTopicAttributes_missingTopicKeepsNotFound guards the non-tag topic
// operations: their error code stays "NotFound".
func TestGetTopicAttributes_missingTopicKeepsNotFound(t *testing.T) {
	srv := helpers.NewTestServer(t)
	resp := snsCall(t, srv, "GetTopicAttributes", url.Values{
		"TopicArn": {"arn:aws:sns:us-east-1:000000000000:absent-topic"},
	})
	defer resp.Body.Close()
	helpers.AssertQueryXMLError(t, resp, "NotFound")
}
