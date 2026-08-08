package rds_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Neaox/overcast/tests/helpers"
)

// createTagTestInstance creates a DB instance and returns its ARN.
func createTagTestInstance(t *testing.T, srv *helpers.TestServer, id string) string {
	t.Helper()
	resp := rdsQuery(t, srv, "CreateDBInstance", url.Values{
		"DBInstanceIdentifier": []string{id},
		"DBInstanceClass":      []string{"db.t3.micro"},
		"Engine":               []string{"mysql"},
		"MasterUsername":       []string{"admin"},
		"MasterUserPassword":   []string{"Password1!"},
		"AllocatedStorage":     []string{"20"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	return fmt.Sprintf("arn:aws:rds:us-east-1:000000000000:db:%s", id)
}

type rdsListTagsResult struct {
	Result struct {
		TagList struct {
			Tags []struct {
				Key   string `xml:"Key"`
				Value string `xml:"Value"`
			} `xml:"Tag"`
		} `xml:"TagList"`
	} `xml:"ListTagsForResourceResult"`
}

func listRDSTags(t *testing.T, srv *helpers.TestServer, arn string) map[string]string {
	t.Helper()
	resp := rdsQuery(t, srv, "ListTagsForResource", url.Values{
		"ResourceName": []string{arn},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out rdsListTagsResult
	decodeXML(t, resp, &out)
	tags := map[string]string{}
	for _, tag := range out.Result.TagList.Tags {
		tags[tag.Key] = tag.Value
	}
	return tags
}

// TestAddTagsToResource_sdkWireForm sends the exact query keys the AWS
// CLI/SDKs produce: the RDS model gives TagList's member the locationName
// "Tag", so tags arrive as Tags.Tag.N.Key / Tags.Tag.N.Value — never
// Tags.member.N. A handler that only parses the member form silently drops
// every tag a real client sends.
func TestAddTagsToResource_sdkWireForm(t *testing.T) {
	// Given: an existing DB instance
	srv := helpers.NewTestServer(t)
	arn := createTagTestInstance(t, srv, "tags-wire-db")

	// When: AddTagsToResource is called with the CLI wire form
	resp := rdsQuery(t, srv, "AddTagsToResource", url.Values{
		"ResourceName":     []string{arn},
		"Tags.Tag.1.Key":   []string{"env"},
		"Tags.Tag.1.Value": []string{"prod"},
		"Tags.Tag.2.Key":   []string{"team"},
		"Tags.Tag.2.Value": []string{"data"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: ListTagsForResource returns both tags
	tags := listRDSTags(t, srv, arn)
	assert.Equal(t, map[string]string{"env": "prod", "team": "data"}, tags)
}

// TestAddTagsToResource_memberFormFallback keeps the legacy member-indexed
// form working for hand-rolled clients.
func TestAddTagsToResource_memberFormFallback(t *testing.T) {
	// Given: an existing DB instance
	srv := helpers.NewTestServer(t)
	arn := createTagTestInstance(t, srv, "tags-member-db")

	// When: AddTagsToResource is called with Tags.member.N keys
	resp := rdsQuery(t, srv, "AddTagsToResource", url.Values{
		"ResourceName":        []string{arn},
		"Tags.member.1.Key":   []string{"env"},
		"Tags.member.1.Value": []string{"staging"},
	})
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the tag is stored
	tags := listRDSTags(t, srv, arn)
	assert.Equal(t, "staging", tags["env"])
}

// TestRemoveTagsFromResource_memberWireForm sends the exact CLI wire form:
// RemoveTagsFromResource's TagKeys is a plain KeyList with no locationName
// override, so keys arrive as TagKeys.member.N.
func TestRemoveTagsFromResource_memberWireForm(t *testing.T) {
	// Given: a DB instance with two tags
	srv := helpers.NewTestServer(t)
	arn := createTagTestInstance(t, srv, "tags-remove-db")
	resp := rdsQuery(t, srv, "AddTagsToResource", url.Values{
		"ResourceName":     []string{arn},
		"Tags.Tag.1.Key":   []string{"env"},
		"Tags.Tag.1.Value": []string{"prod"},
		"Tags.Tag.2.Key":   []string{"team"},
		"Tags.Tag.2.Value": []string{"data"},
	})
	resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// When: one key is removed with the member-indexed wire form
	rm := rdsQuery(t, srv, "RemoveTagsFromResource", url.Values{
		"ResourceName":     []string{arn},
		"TagKeys.member.1": []string{"env"},
	})
	defer rm.Body.Close()
	helpers.AssertStatus(t, rm, http.StatusOK)

	// Then: only the removed key is gone
	tags := listRDSTags(t, srv, arn)
	assert.Equal(t, map[string]string{"team": "data"}, tags)
}

// TestRDSTagOps_missingResources: real RDS rejects tag operations on
// resources that do not exist rather than minting a tag store for any string.
func TestRDSTagOps_missingResources(t *testing.T) {
	srv := helpers.NewTestServer(t)

	// AddTagsToResource on a missing DB instance → DBInstanceNotFound
	resp := rdsQuery(t, srv, "AddTagsToResource", url.Values{
		"ResourceName":     []string{"arn:aws:rds:us-east-1:000000000000:db:no-such-db"},
		"Tags.Tag.1.Key":   []string{"env"},
		"Tags.Tag.1.Value": []string{"prod"},
	})
	helpers.AssertQueryXMLError(t, resp, "DBInstanceNotFound")
	resp.Body.Close()

	// ListTagsForResource on a missing DB cluster → DBClusterNotFoundFault
	resp = rdsQuery(t, srv, "ListTagsForResource", url.Values{
		"ResourceName": []string{"arn:aws:rds:us-east-1:000000000000:cluster:no-such-cluster"},
	})
	helpers.AssertQueryXMLError(t, resp, "DBClusterNotFoundFault")
	resp.Body.Close()

	// RemoveTagsFromResource on a missing DB instance → DBInstanceNotFound
	resp = rdsQuery(t, srv, "RemoveTagsFromResource", url.Values{
		"ResourceName":     []string{"arn:aws:rds:us-east-1:000000000000:db:no-such-db"},
		"TagKeys.member.1": []string{"env"},
	})
	helpers.AssertQueryXMLError(t, resp, "DBInstanceNotFound")
	resp.Body.Close()

	// A malformed resource name → InvalidParameterValue
	resp = rdsQuery(t, srv, "AddTagsToResource", url.Values{
		"ResourceName":     []string{"not-an-arn"},
		"Tags.Tag.1.Key":   []string{"env"},
		"Tags.Tag.1.Value": []string{"prod"},
	})
	helpers.AssertQueryXMLError(t, resp, "InvalidParameterValue")
	resp.Body.Close()
}
