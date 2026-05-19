package cloudformation_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Neaox/overcast/tests/helpers"
)

func TestDebugCustomResource(t *testing.T) {
	// This is a diagnostic helper superseded by TestCreateStack_customResource_invokesLambda,
	// which has proper assertions. Skip in automated runs.
	t.Skip("diagnostic helper: use TestCreateStack_customResource_invokesLambda for assertion-based coverage")
	srv := helpers.NewTestServer(t)

	cr := cfnQuery(t, srv, "CreateStack", url.Values{
		"StackName":    []string{"custom-res-stack"},
		"TemplateBody": []string{customResourceTemplate},
	})
	defer cr.Body.Close()
	helpers.AssertStatus(t, cr, http.StatusOK)

	time.Sleep(2 * time.Second)

	resp := cfnQuery(t, srv, "DescribeStacks", url.Values{
		"StackName": []string{"custom-res-stack"},
	})
	defer resp.Body.Close()
	body := string(readBody(t, resp))

	for _, s := range []string{"CREATE_COMPLETE", "CREATE_FAILED", "ROLLBACK_COMPLETE", "CREATE_IN_PROGRESS"} {
		if strings.Contains(body, s) {
			t.Logf("Stack status: %s", s)
		}
	}

	evResp := cfnQuery(t, srv, "DescribeStackEvents", url.Values{
		"StackName": []string{"custom-res-stack"},
	})
	defer evResp.Body.Close()
	evBody := string(readBody(t, evResp))

	for _, keyword := range []string{"FAILED", "custom resource"} {
		if idx := strings.Index(evBody, keyword); idx >= 0 {
			start := idx - 300
			if start < 0 {
				start = 0
			}
			end := idx + 300
			if end > len(evBody) {
				end = len(evBody)
			}
			t.Logf("Context around %q:\n%s", keyword, evBody[start:end])
		}
	}

	_ = http.StatusOK
}
