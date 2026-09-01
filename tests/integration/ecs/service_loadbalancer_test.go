package ecs_test

// service_loadbalancer_test.go — a service putting its tasks behind a load
// balancer. Shares ecsCall, awsvpcTaskDefCluster and describeService with the
// other files in this package.

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

func elbQuery(t *testing.T, srv *helpers.TestServer, action string, params url.Values) *http.Response {
	t.Helper()
	params.Set("Action", action)
	params.Set("Version", "2015-12-01")
	resp, err := http.PostForm(srv.URL+"/", params)
	if err != nil {
		t.Fatalf("elbQuery %s: %v", action, err)
	}
	return resp
}

func readAllBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func firstXMLValue(t *testing.T, body, tag string) string {
	t.Helper()
	openTag, closeTag := "<"+tag+">", "</"+tag+">"
	i := strings.Index(body, openTag)
	j := strings.Index(body, closeTag)
	if i < 0 || j < 0 {
		t.Fatalf("no <%s> in response:\n%s", tag, body)
	}
	return body[i+len(openTag) : j]
}

func TestCreateService_registersTasksWithTargetGroup(t *testing.T) {
	// Given: an awsvpc task definition and a target group.
	srv := awsvpcTaskDefCluster(t, "lb-cluster", "lb-task")

	tgResp := elbQuery(t, srv, "CreateTargetGroup", url.Values{
		"Name":       {"svc-targets"},
		"Protocol":   {"HTTP"},
		"Port":       {"8080"},
		"VpcId":      {"vpc-12345"},
		"TargetType": {"ip"},
	})
	helpers.AssertStatus(t, tgResp, http.StatusOK)
	tgArn := firstXMLValue(t, readAllBody(t, tgResp), "TargetGroupArn")

	// When: a service is created attached to that target group.
	resp := ecsCall(t, srv, "CreateService", map[string]any{
		"cluster":        "lb-cluster",
		"serviceName":    "lb-service",
		"taskDefinition": "lb-task:1",
		"desiredCount":   1,
		"launchType":     "FARGATE",
		"networkConfiguration": map[string]any{
			"awsvpcConfiguration": map[string]any{"subnets": []string{"subnet-12345678"}},
		},
		"loadBalancers": []map[string]any{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 8080},
		},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	describeService(t, srv, "lb-cluster", "lb-service")

	// Then: the task is registered in the target group at its container port,
	// so the load balancer has somewhere to forward to.
	health := elbQuery(t, srv, "DescribeTargetHealth", url.Values{"TargetGroupArn": {tgArn}})
	helpers.AssertStatus(t, health, http.StatusOK)
	body := readAllBody(t, health)
	if !strings.Contains(body, "<Port>8080</Port>") {
		t.Errorf("expected the container port to be registered, got:\n%s", body)
	}

	// And the registered address is the one DescribeTasks reports for the task,
	// not some other address the load balancer could not reach.
	listResp := ecsCall(t, srv, "ListTasks", map[string]any{"cluster": "lb-cluster"})
	helpers.AssertStatus(t, listResp, http.StatusOK)
	var listed struct {
		TaskArns []string `json:"taskArns"`
	}
	helpers.DecodeJSON(t, listResp, &listed)
	listResp.Body.Close()
	if len(listed.TaskArns) != 1 {
		t.Fatalf("expected 1 task, got %d", len(listed.TaskArns))
	}

	descResp := ecsCall(t, srv, "DescribeTasks", map[string]any{
		"cluster": "lb-cluster", "tasks": listed.TaskArns,
	})
	helpers.AssertStatus(t, descResp, http.StatusOK)
	var described struct {
		Tasks []struct {
			Attachments []struct {
				Details []struct {
					Name  string `json:"name"`
					Value string `json:"value"`
				} `json:"details"`
			} `json:"attachments"`
		} `json:"tasks"`
	}
	helpers.DecodeJSON(t, descResp, &described)
	descResp.Body.Close()
	if len(described.Tasks) != 1 || len(described.Tasks[0].Attachments) == 0 {
		t.Fatal("expected the task to carry an ENI attachment")
	}

	var eniIP string
	for _, d := range described.Tasks[0].Attachments[0].Details {
		if d.Name == "privateIPv4Address" {
			eniIP = d.Value
		}
	}
	if eniIP == "" || !strings.Contains(body, "<Id>"+eniIP+"</Id>") {
		t.Errorf("expected the task's ENI address %q to be the registered target, got:\n%s", eniIP, body)
	}
}

func TestUpdateService_scaleDownDeregistersTargets(t *testing.T) {
	// Given: a service behind a target group running two tasks.
	srv := awsvpcTaskDefCluster(t, "drain-cluster", "drain-task")

	tgResp := elbQuery(t, srv, "CreateTargetGroup", url.Values{
		"Name":       {"drain-targets"},
		"Protocol":   {"HTTP"},
		"Port":       {"8080"},
		"VpcId":      {"vpc-12345"},
		"TargetType": {"ip"},
	})
	helpers.AssertStatus(t, tgResp, http.StatusOK)
	tgArn := firstXMLValue(t, readAllBody(t, tgResp), "TargetGroupArn")

	resp := ecsCall(t, srv, "CreateService", map[string]any{
		"cluster":        "drain-cluster",
		"serviceName":    "drain-service",
		"taskDefinition": "drain-task:1",
		"desiredCount":   2,
		"launchType":     "FARGATE",
		"networkConfiguration": map[string]any{
			"awsvpcConfiguration": map[string]any{"subnets": []string{"subnet-12345678"}},
		},
		"loadBalancers": []map[string]any{
			{"targetGroupArn": tgArn, "containerName": "app", "containerPort": 8080},
		},
	})
	helpers.AssertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	describeService(t, srv, "drain-cluster", "drain-service")

	before := strings.Count(readAllBody(t, elbQuery(t, srv, "DescribeTargetHealth",
		url.Values{"TargetGroupArn": {tgArn}})), "<Id>")
	if before != 2 {
		t.Fatalf("expected 2 registered targets, got %d", before)
	}

	// When: the service is scaled down.
	upd := ecsCall(t, srv, "UpdateService", map[string]any{
		"cluster":      "drain-cluster",
		"service":      "drain-service",
		"desiredCount": 1,
	})
	helpers.AssertStatus(t, upd, http.StatusOK)
	upd.Body.Close()

	// Then: the stopped task is out of the target group, so the load balancer
	// is not still forwarding to a container that is being torn down.
	after := strings.Count(readAllBody(t, elbQuery(t, srv, "DescribeTargetHealth",
		url.Values{"TargetGroupArn": {tgArn}})), "<Id>")
	if after != 1 {
		t.Errorf("expected 1 registered target after scaling down, got %d", after)
	}
}
