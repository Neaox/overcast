// Health check CRUD tests for the Route 53 emulator at inert level.
package route53_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

type healthCheckXML struct {
	Id                 string `xml:"Id"`
	CallerReference    string `xml:"CallerReference"`
	HealthCheckVersion int64  `xml:"HealthCheckVersion"`
	HealthCheckConfig  struct {
		IPAddress                string `xml:"IPAddress"`
		Port                     int    `xml:"Port"`
		Type                     string `xml:"Type"`
		ResourcePath             string `xml:"ResourcePath"`
		FullyQualifiedDomainName string `xml:"FullyQualifiedDomainName"`
		RequestInterval          int    `xml:"RequestInterval"`
		FailureThreshold         int    `xml:"FailureThreshold"`
	} `xml:"HealthCheckConfig"`
}

func createHealthCheck(t *testing.T, srv *helpers.TestServer, callerRef, config string) healthCheckXML {
	t.Helper()
	body := `<CreateHealthCheckRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<CallerReference>` + callerRef + `</CallerReference>` +
		`<HealthCheckConfig>` + config + `</HealthCheckConfig>` +
		`</CreateHealthCheckRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/healthcheck", body)
	helpers.AssertStatus(t, resp, http.StatusCreated)
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "/2013-04-01/healthcheck/") {
		t.Errorf("expected Location header with /2013-04-01/healthcheck/, got %q", loc)
	}
	var out struct {
		HealthCheck healthCheckXML `xml:"HealthCheck"`
	}
	decodeXML(t, resp, &out)
	if out.HealthCheck.Id == "" {
		t.Fatal("expected HealthCheck.Id to be set")
	}
	return out.HealthCheck
}

func TestCreateHealthCheck_appliesDefaults(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a minimal HTTP health check is created
	hc := createHealthCheck(t, srv, "hc-defaults",
		`<IPAddress>192.0.2.10</IPAddress><Type>HTTP</Type><ResourcePath>/health</ResourcePath>`)

	// Then: AWS defaults are applied and version starts at 1
	if hc.HealthCheckVersion != 1 {
		t.Errorf("expected HealthCheckVersion=1, got %d", hc.HealthCheckVersion)
	}
	if hc.HealthCheckConfig.RequestInterval != 30 {
		t.Errorf("expected default RequestInterval=30, got %d", hc.HealthCheckConfig.RequestInterval)
	}
	if hc.HealthCheckConfig.FailureThreshold != 3 {
		t.Errorf("expected default FailureThreshold=3, got %d", hc.HealthCheckConfig.FailureThreshold)
	}
	if hc.HealthCheckConfig.Port != 80 {
		t.Errorf("expected default Port=80 for HTTP, got %d", hc.HealthCheckConfig.Port)
	}
}

func TestCreateHealthCheck_duplicateCallerReference(t *testing.T) {
	// Given: a health check created with a caller reference
	srv := helpers.NewTestServer(t)
	createHealthCheck(t, srv, "hc-dup", `<IPAddress>192.0.2.10</IPAddress><Type>TCP</Type><Port>443</Port>`)

	// When: the same caller reference is used with a different config
	body := `<CreateHealthCheckRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<CallerReference>hc-dup</CallerReference>` +
		`<HealthCheckConfig><IPAddress>192.0.2.99</IPAddress><Type>TCP</Type><Port>443</Port></HealthCheckConfig>` +
		`</CreateHealthCheckRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/healthcheck", body)

	// Then: HealthCheckAlreadyExists is returned with HTTP 409
	helpers.AssertStatus(t, resp, http.StatusConflict)
	helpers.AssertQueryXMLError(t, resp, "HealthCheckAlreadyExists")
}

func TestCreateHealthCheck_missingType(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: the health check config has no Type
	body := `<CreateHealthCheckRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<CallerReference>hc-notype</CallerReference>` +
		`<HealthCheckConfig><IPAddress>192.0.2.10</IPAddress></HealthCheckConfig>` +
		`</CreateHealthCheckRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/healthcheck", body)

	// Then: InvalidInput is returned
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
	helpers.AssertQueryXMLError(t, resp, "InvalidInput")
}

func TestGetHealthCheck_roundTrip(t *testing.T) {
	// Given: a health check exists
	srv := helpers.NewTestServer(t)
	created := createHealthCheck(t, srv, "hc-get",
		`<FullyQualifiedDomainName>app.example.com</FullyQualifiedDomainName><Type>HTTPS</Type>`)

	// When: GetHealthCheck is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/healthcheck/"+created.Id, "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HealthCheck healthCheckXML `xml:"HealthCheck"`
	}
	decodeXML(t, resp, &out)

	// Then: the stored config round-trips
	if out.HealthCheck.Id != created.Id {
		t.Errorf("expected Id %q, got %q", created.Id, out.HealthCheck.Id)
	}
	if out.HealthCheck.HealthCheckConfig.FullyQualifiedDomainName != "app.example.com" {
		t.Errorf("expected FQDN app.example.com, got %q", out.HealthCheck.HealthCheckConfig.FullyQualifiedDomainName)
	}
	if out.HealthCheck.HealthCheckConfig.Port != 443 {
		t.Errorf("expected default Port=443 for HTTPS, got %d", out.HealthCheck.HealthCheckConfig.Port)
	}
}

func TestGetHealthCheck_notFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: GetHealthCheck is called with an unknown ID
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/healthcheck/00000000-0000-0000-0000-000000000000", "")

	// Then: NoSuchHealthCheck is returned
	helpers.AssertStatus(t, resp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, resp, "NoSuchHealthCheck")
}

func TestUpdateHealthCheck_bumpsVersion(t *testing.T) {
	// Given: a health check exists
	srv := helpers.NewTestServer(t)
	created := createHealthCheck(t, srv, "hc-update", `<IPAddress>192.0.2.10</IPAddress><Type>HTTP</Type>`)

	// When: the failure threshold is updated
	body := `<UpdateHealthCheckRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<FailureThreshold>5</FailureThreshold>` +
		`</UpdateHealthCheckRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/healthcheck/"+created.Id, body)
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HealthCheck healthCheckXML `xml:"HealthCheck"`
	}
	decodeXML(t, resp, &out)

	// Then: the config changes and the version increments
	if out.HealthCheck.HealthCheckConfig.FailureThreshold != 5 {
		t.Errorf("expected FailureThreshold=5, got %d", out.HealthCheck.HealthCheckConfig.FailureThreshold)
	}
	if out.HealthCheck.HealthCheckVersion != 2 {
		t.Errorf("expected HealthCheckVersion=2, got %d", out.HealthCheck.HealthCheckVersion)
	}
}

func TestUpdateHealthCheck_versionMismatch(t *testing.T) {
	// Given: a health check at version 1
	srv := helpers.NewTestServer(t)
	created := createHealthCheck(t, srv, "hc-lock", `<IPAddress>192.0.2.10</IPAddress><Type>HTTP</Type>`)

	// When: an update presents a stale HealthCheckVersion
	body := `<UpdateHealthCheckRequest xmlns="https://route53.amazonaws.com/doc/2013-04-01/">` +
		`<HealthCheckVersion>7</HealthCheckVersion>` +
		`<FailureThreshold>5</FailureThreshold>` +
		`</UpdateHealthCheckRequest>`
	resp := r53Call(t, srv, http.MethodPost, "/2013-04-01/healthcheck/"+created.Id, body)

	// Then: HealthCheckVersionMismatch is returned with HTTP 409
	helpers.AssertStatus(t, resp, http.StatusConflict)
	helpers.AssertQueryXMLError(t, resp, "HealthCheckVersionMismatch")
}

func TestListHealthChecksAndCount(t *testing.T) {
	// Given: two health checks
	srv := helpers.NewTestServer(t)
	createHealthCheck(t, srv, "hc-list-1", `<IPAddress>192.0.2.1</IPAddress><Type>HTTP</Type>`)
	createHealthCheck(t, srv, "hc-list-2", `<IPAddress>192.0.2.2</IPAddress><Type>HTTP</Type>`)

	// When: ListHealthChecks is called
	resp := r53Call(t, srv, http.MethodGet, "/2013-04-01/healthcheck", "")
	helpers.AssertStatus(t, resp, http.StatusOK)
	var out struct {
		HealthChecks []healthCheckXML `xml:"HealthChecks>HealthCheck"`
		IsTruncated  bool             `xml:"IsTruncated"`
	}
	decodeXML(t, resp, &out)

	// Then: both are listed
	if len(out.HealthChecks) != 2 || out.IsTruncated {
		t.Errorf("expected 2 health checks (not truncated), got %d (truncated=%v)", len(out.HealthChecks), out.IsTruncated)
	}

	// And: GetHealthCheckCount agrees
	countResp := r53Call(t, srv, http.MethodGet, "/2013-04-01/healthcheckcount", "")
	helpers.AssertStatus(t, countResp, http.StatusOK)
	var count struct {
		HealthCheckCount int `xml:"HealthCheckCount"`
	}
	decodeXML(t, countResp, &count)
	if count.HealthCheckCount != 2 {
		t.Errorf("expected HealthCheckCount=2, got %d", count.HealthCheckCount)
	}
}

func TestDeleteHealthCheck(t *testing.T) {
	// Given: a health check exists
	srv := helpers.NewTestServer(t)
	created := createHealthCheck(t, srv, "hc-del", `<IPAddress>192.0.2.10</IPAddress><Type>HTTP</Type>`)

	// When: it is deleted
	resp := r53Call(t, srv, http.MethodDelete, "/2013-04-01/healthcheck/"+created.Id, "")
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: it is gone, and deleting again reports NoSuchHealthCheck
	getResp := r53Call(t, srv, http.MethodGet, "/2013-04-01/healthcheck/"+created.Id, "")
	helpers.AssertStatus(t, getResp, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, getResp, "NoSuchHealthCheck")

	delAgain := r53Call(t, srv, http.MethodDelete, "/2013-04-01/healthcheck/"+created.Id, "")
	helpers.AssertStatus(t, delAgain, http.StatusNotFound)
	helpers.AssertQueryXMLError(t, delAgain, "NoSuchHealthCheck")
}

func TestHealthCheckTags(t *testing.T) {
	// Given: a health check exists
	srv := helpers.NewTestServer(t)
	created := createHealthCheck(t, srv, "hc-tags", `<IPAddress>192.0.2.10</IPAddress><Type>HTTP</Type>`)

	// When: a tag is added via the shared tags API
	resp := changeTags(t, srv, "healthcheck", created.Id,
		`<AddTags><Tag><Key>service</Key><Value>api</Value></Tag></AddTags>`)
	defer resp.Body.Close()
	helpers.AssertStatus(t, resp, http.StatusOK)

	// Then: the tag is listed for the health check
	tags := listTags(t, srv, "healthcheck", created.Id)
	if len(tags) != 1 || tags[0].Key != "service" || tags[0].Value != "api" {
		t.Errorf("expected service=api tag, got %+v", tags)
	}
}
