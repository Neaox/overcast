package logs_test

import (
	"net/http"
	"strconv"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// describeLogGroupsResult is the DescribeLogGroups wire shape these tests
// assert on — the paginated envelope, so `nextToken` is part of it.
type describeLogGroupsResult struct {
	LogGroups []struct {
		LogGroupName string `json:"logGroupName"`
	} `json:"logGroups"`
	NextToken string `json:"nextToken"`
}

type describeLogStreamsResult struct {
	LogStreams []struct {
		LogStreamName string `json:"logStreamName"`
	} `json:"logStreams"`
	NextToken string `json:"nextToken"`
}

func describeLogGroupsPage(t *testing.T, srv *helpers.TestServer, body map[string]any) describeLogGroupsResult {
	t.Helper()
	resp := logsCall(t, srv, "DescribeLogGroups", body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var page describeLogGroupsResult
	helpers.DecodeJSON(t, resp, &page)
	return page
}

func describeLogStreamsPage(t *testing.T, srv *helpers.TestServer, body map[string]any) describeLogStreamsResult {
	t.Helper()
	resp := logsCall(t, srv, "DescribeLogStreams", body)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)
	var page describeLogStreamsResult
	helpers.DecodeJSON(t, resp, &page)
	return page
}

func TestDescribeLogGroups_limitBoundsThePage(t *testing.T) {
	// Given: three log groups exist
	srv := helpers.NewTestServer(t)
	for _, name := range []string{"group-a", "group-b", "group-c"} {
		createLogGroup(t, srv, name)
	}

	// When: DescribeLogGroups asks for one item
	page := describeLogGroupsPage(t, srv, map[string]any{"limit": 1})

	// Then: exactly one comes back, with a token for the remainder
	if len(page.LogGroups) != 1 {
		t.Fatalf("limit=1 returned %d log groups, want 1", len(page.LogGroups))
	}
	if page.NextToken == "" {
		t.Fatal("limit=1 over three groups returned no nextToken")
	}
}

func TestDescribeLogGroups_nextTokenPagesThroughTheRemainder(t *testing.T) {
	// Given: three log groups exist
	srv := helpers.NewTestServer(t)
	want := []string{"group-a", "group-b", "group-c"}
	for _, name := range want {
		createLogGroup(t, srv, name)
	}

	// When: the caller pages with limit=1, following nextToken each time
	var got []string
	token := ""
	for range len(want) {
		body := map[string]any{"limit": 1}
		if token != "" {
			body["nextToken"] = token
		}
		page := describeLogGroupsPage(t, srv, body)
		if len(page.LogGroups) != 1 {
			t.Fatalf("page returned %d groups, want 1", len(page.LogGroups))
		}
		got = append(got, page.LogGroups[0].LogGroupName)
		token = page.NextToken
	}

	// Then: every group is seen exactly once, in ASCII order, and the final
	// page ends the walk rather than looping.
	if token != "" {
		t.Errorf("nextToken after the last page = %q, want empty", token)
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("page %d returned %q, want %q", i, got[i], name)
		}
	}
}

func TestDescribeLogGroups_invalidNextToken(t *testing.T) {
	// Given: a log group exists
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "group-a")

	// When: DescribeLogGroups is called with a token that is not one of ours
	resp := logsCall(t, srv, "DescribeLogGroups", map[string]any{"nextToken": "not-a-token"})
	defer resp.Body.Close()

	// Then: the call is rejected rather than silently restarting at page 1
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

func TestDescribeLogStreams_limitBoundsThePage(t *testing.T) {
	// Given: a group with three streams
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/paged")
	for _, name := range []string{"stream-a", "stream-b", "stream-c"} {
		createLogStream(t, srv, "/aws/lambda/paged", name)
	}

	// When: DescribeLogStreams asks for one item
	page := describeLogStreamsPage(t, srv, map[string]any{
		"logGroupName": "/aws/lambda/paged",
		"limit":        1,
	})

	// Then: exactly one comes back, with a token for the remainder
	if len(page.LogStreams) != 1 {
		t.Fatalf("limit=1 returned %d log streams, want 1", len(page.LogStreams))
	}
	if page.NextToken == "" {
		t.Fatal("limit=1 over three streams returned no nextToken")
	}
}

func TestDescribeLogStreams_nextTokenPagesThroughTheRemainder(t *testing.T) {
	// Given: a group with three streams
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/paged")
	want := []string{"stream-a", "stream-b", "stream-c"}
	for _, name := range want {
		createLogStream(t, srv, "/aws/lambda/paged", name)
	}

	// When: the caller pages with limit=2, following nextToken
	var got []string
	token := ""
	for page := 0; page < 2; page++ {
		body := map[string]any{"logGroupName": "/aws/lambda/paged", "limit": 2}
		if token != "" {
			body["nextToken"] = token
		}
		result := describeLogStreamsPage(t, srv, body)
		for _, s := range result.LogStreams {
			got = append(got, s.LogStreamName)
		}
		token = result.NextToken
	}

	// Then: all three streams are seen exactly once and the walk terminates
	if token != "" {
		t.Errorf("nextToken after the last page = %q, want empty", token)
	}
	if len(got) != len(want) {
		t.Fatalf("paged walk returned %d streams (%v), want %d", len(got), got, len(want))
	}
	for i, name := range want {
		if got[i] != name {
			t.Errorf("stream %d = %q, want %q", i, got[i], name)
		}
	}
}

func TestDescribeLogStreams_invalidNextToken(t *testing.T) {
	// Given: a group with one stream
	srv := helpers.NewTestServer(t)
	createLogGroup(t, srv, "/aws/lambda/paged")
	createLogStream(t, srv, "/aws/lambda/paged", "stream-a")

	// When: DescribeLogStreams is called with a garbled token
	resp := logsCall(t, srv, "DescribeLogStreams", map[string]any{
		"logGroupName": "/aws/lambda/paged",
		"nextToken":    "not-a-token",
	})
	defer resp.Body.Close()

	// Then: the call is rejected
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertJSONError(t, resp, "InvalidParameterException")
}

func TestDescribeLogGroups_limitAboveTheMaximumIsCapped(t *testing.T) {
	// Given: more log groups than DescribeLogGroups' documented maximum page
	// size of 50
	srv := helpers.NewTestServer(t)
	for i := range 51 {
		createLogGroup(t, srv, "group-"+strconv.Itoa(100+i))
	}

	// When: the caller asks for more than AWS allows
	page := describeLogGroupsPage(t, srv, map[string]any{"limit": 500})

	// Then: the page is capped at 50 and the rest is behind a token
	if len(page.LogGroups) != 50 {
		t.Fatalf("limit=500 returned %d log groups, want the documented cap of 50", len(page.LogGroups))
	}
	if page.NextToken == "" {
		t.Fatal("capped page returned no nextToken")
	}
}
