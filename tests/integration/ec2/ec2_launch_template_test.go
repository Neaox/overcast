package ec2_test

// Launch templates (#518). CDK's autoscaling.AutoScalingGroup emits one by
// default, so an EC2 emulation without them refuses the modern IaC path.
//
// These cover the wire contract: the `lt-` id shape, version numbering from 1,
// `$Latest`/`$Default` resolution, the default-version move ModifyLaunchTemplate
// makes, AWS's launch-template error codes, and RunInstances merging a
// template's data *under* explicitly-passed parameters.

import (
	"encoding/xml"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers"
)

// ─── XML shapes ───────────────────────────────────────────────────────────────

type xmlLaunchTemplate struct {
	LaunchTemplateID     string `xml:"launchTemplateId"`
	LaunchTemplateName   string `xml:"launchTemplateName"`
	CreateTime           string `xml:"createTime"`
	CreatedBy            string `xml:"createdBy"`
	DefaultVersionNumber int64  `xml:"defaultVersionNumber"`
	LatestVersionNumber  int64  `xml:"latestVersionNumber"`
	TagSet               []struct {
		Key   string `xml:"key"`
		Value string `xml:"value"`
	} `xml:"tagSet>item"`
}

type xmlLaunchTemplateData struct {
	ImageID           string   `xml:"imageId"`
	InstanceType      string   `xml:"instanceType"`
	KeyName           string   `xml:"keyName"`
	UserData          string   `xml:"userData"`
	SecurityGroupIDs  []string `xml:"securityGroupIdSet>item"`
	SecurityGroups    []string `xml:"securityGroupSet>item"`
	NetworkInterfaces []struct {
		DeviceIndex int      `xml:"deviceIndex"`
		SubnetID    string   `xml:"subnetId"`
		Groups      []string `xml:"groupSet>item"`
	} `xml:"networkInterfaceSet>item"`
	TagSpecifications []struct {
		ResourceType string `xml:"resourceType"`
		Tags         []struct {
			Key   string `xml:"key"`
			Value string `xml:"value"`
		} `xml:"tagSet>item"`
	} `xml:"tagSpecificationSet>item"`
}

type xmlLaunchTemplateVersion struct {
	LaunchTemplateID   string                `xml:"launchTemplateId"`
	LaunchTemplateName string                `xml:"launchTemplateName"`
	VersionNumber      int64                 `xml:"versionNumber"`
	VersionDescription string                `xml:"versionDescription"`
	CreateTime         string                `xml:"createTime"`
	CreatedBy          string                `xml:"createdBy"`
	DefaultVersion     bool                  `xml:"defaultVersion"`
	Data               xmlLaunchTemplateData `xml:"launchTemplateData"`
}

type createLaunchTemplateResponse struct {
	XMLName        xml.Name          `xml:"CreateLaunchTemplateResponse"`
	RequestID      string            `xml:"requestId"`
	LaunchTemplate xmlLaunchTemplate `xml:"launchTemplate"`
}

type createLaunchTemplateVersionResponse struct {
	XMLName xml.Name                 `xml:"CreateLaunchTemplateVersionResponse"`
	Version xmlLaunchTemplateVersion `xml:"launchTemplateVersion"`
}

type describeLaunchTemplatesResponse struct {
	XMLName   xml.Name            `xml:"DescribeLaunchTemplatesResponse"`
	Templates []xmlLaunchTemplate `xml:"launchTemplates>item"`
}

type describeLaunchTemplateVersionsResponse struct {
	XMLName  xml.Name                   `xml:"DescribeLaunchTemplateVersionsResponse"`
	Versions []xmlLaunchTemplateVersion `xml:"launchTemplateVersionSet>item"`
}

type modifyLaunchTemplateResponse struct {
	XMLName        xml.Name          `xml:"ModifyLaunchTemplateResponse"`
	LaunchTemplate xmlLaunchTemplate `xml:"launchTemplate"`
}

type deleteLaunchTemplateResponse struct {
	XMLName        xml.Name          `xml:"DeleteLaunchTemplateResponse"`
	LaunchTemplate xmlLaunchTemplate `xml:"launchTemplate"`
}

type deleteLaunchTemplateVersionsResponse struct {
	XMLName xml.Name `xml:"DeleteLaunchTemplateVersionsResponse"`
	Deleted []struct {
		LaunchTemplateID   string `xml:"launchTemplateId"`
		LaunchTemplateName string `xml:"launchTemplateName"`
		VersionNumber      int64  `xml:"versionNumber"`
	} `xml:"successfullyDeletedLaunchTemplateVersionSet>item"`
	Failed []struct {
		VersionNumber int64 `xml:"versionNumber"`
		ResponseError struct {
			Code    string `xml:"code"`
			Message string `xml:"message"`
		} `xml:"responseError"`
	} `xml:"unsuccessfullyDeletedLaunchTemplateVersionSet>item"`
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// ltDecode issues an EC2 Query call, requires 200, and unmarshals the body.
func ltDecode(t *testing.T, srv *helpers.TestServer, action string, params url.Values, out any) {
	t.Helper()
	resp := ec2Query(t, srv, action, params)
	defer resp.Body.Close()
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%s: status %d: %s", action, resp.StatusCode, body)
	}
	if err := xml.Unmarshal(body, out); err != nil {
		t.Fatalf("%s: unmarshal: %v\n%s", action, err, body)
	}
}

// createTemplate creates a minimal launch template and returns its record.
func createTemplate(t *testing.T, srv *helpers.TestServer, name string, extra url.Values) xmlLaunchTemplate {
	t.Helper()
	params := url.Values{
		"LaunchTemplateName":              {name},
		"LaunchTemplateData.ImageId":      {"ami-0123456789abcdef0"},
		"LaunchTemplateData.InstanceType": {"t3.micro"},
	}
	for k, v := range extra {
		params[k] = v
	}
	var out createLaunchTemplateResponse
	ltDecode(t, srv, "CreateLaunchTemplate", params, &out)
	return out.LaunchTemplate
}

var launchTemplateIDPattern = regexp.MustCompile(`^lt-[0-9a-f]{17}$`)

// ─── CreateLaunchTemplate ─────────────────────────────────────────────────────

// TestCreateLaunchTemplate_success pins AWS's id shape and the version
// numbering a freshly created template starts from.
func TestCreateLaunchTemplate_success(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a launch template is created
	tmpl := createTemplate(t, srv, "web-template", url.Values{
		"VersionDescription":              {"first"},
		"TagSpecification.1.ResourceType": {"launch-template"},
		"TagSpecification.1.Tag.1.Key":    {"Team"},
		"TagSpecification.1.Tag.1.Value":  {"platform"},
	})

	// Then: it carries AWS's lt- id, version 1 as both latest and default
	if !launchTemplateIDPattern.MatchString(tmpl.LaunchTemplateID) {
		t.Errorf("LaunchTemplateId = %q, want lt- followed by 17 hex characters", tmpl.LaunchTemplateID)
	}
	if tmpl.LaunchTemplateName != "web-template" {
		t.Errorf("LaunchTemplateName = %q, want web-template", tmpl.LaunchTemplateName)
	}
	if tmpl.LatestVersionNumber != 1 || tmpl.DefaultVersionNumber != 1 {
		t.Errorf("latest/default = %d/%d, want 1/1", tmpl.LatestVersionNumber, tmpl.DefaultVersionNumber)
	}
	if tmpl.CreateTime == "" {
		t.Error("createTime is empty")
	}
	if !strings.HasPrefix(tmpl.CreatedBy, "arn:aws:iam::") {
		t.Errorf("createdBy = %q, want an IAM ARN", tmpl.CreatedBy)
	}
	if len(tmpl.TagSet) != 1 || tmpl.TagSet[0].Key != "Team" || tmpl.TagSet[0].Value != "platform" {
		t.Errorf("tagSet = %+v, want the launch-template TagSpecification", tmpl.TagSet)
	}
}

// TestCreateLaunchTemplate_duplicateName pins AWS's already-exists code.
func TestCreateLaunchTemplate_duplicateName(t *testing.T) {
	// Given: a template already exists
	srv := helpers.NewTestServer(t)
	createTemplate(t, srv, "dup-template", nil)

	// When: the same name is used again
	resp := ec2Query(t, srv, "CreateLaunchTemplate", url.Values{
		"LaunchTemplateName":              {"dup-template"},
		"LaunchTemplateData.InstanceType": {"t3.micro"},
	})
	defer resp.Body.Close()

	// Then: AWS's InvalidLaunchTemplateName.AlreadyExistsException comes back
	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidLaunchTemplateName.AlreadyExistsException")
}

// TestCreateLaunchTemplate_malformedName pins AWS's name constraint.
func TestCreateLaunchTemplate_malformedName(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a name shorter than AWS's three-character minimum is used
	resp := ec2Query(t, srv, "CreateLaunchTemplate", url.Values{
		"LaunchTemplateName":              {"ab"},
		"LaunchTemplateData.InstanceType": {"t3.micro"},
	})
	defer resp.Body.Close()

	// Then: AWS's malformed-name code comes back
	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidLaunchTemplateName.MalformedException")
}

// TestCreateLaunchTemplate_noLaunchTemplateData pins that a template with no
// launch parameters at all is refused, as AWS refuses it: LaunchTemplateData
// is a required member.
func TestCreateLaunchTemplate_noLaunchTemplateData(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: a template is created with a name and nothing else
	resp := ec2Query(t, srv, "CreateLaunchTemplate", url.Values{
		"LaunchTemplateName": {"empty-template"},
	})
	defer resp.Body.Close()

	// Then: the call is refused
	assertEC2QueryError(t, resp, http.StatusBadRequest, "MissingParameter")
}

// TestDescribeLaunchTemplates_bothSelectors pins AWS's rule that a describe
// selects on IDs or on names, never on both.
func TestDescribeLaunchTemplates_bothSelectors(t *testing.T) {
	// Given: a template exists
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "both-selectors", nil)

	// When: the call names it by ID and by name at once
	resp := ec2Query(t, srv, "DescribeLaunchTemplates", url.Values{
		"LaunchTemplateId.1":   {tmpl.LaunchTemplateID},
		"LaunchTemplateName.1": {"both-selectors"},
	})
	defer resp.Body.Close()

	// Then: the combination is refused rather than intersected
	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidParameterCombination")
}

// ─── Versions ─────────────────────────────────────────────────────────────────

// TestCreateLaunchTemplateVersion_numbersFromOne pins that versions increment
// and that the default version does not follow the latest one.
func TestCreateLaunchTemplateVersion_numbersFromOne(t *testing.T) {
	// Given: a template with one version
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "ver-template", nil)

	// When: a second version is created
	var created createLaunchTemplateVersionResponse
	ltDecode(t, srv, "CreateLaunchTemplateVersion", url.Values{
		"LaunchTemplateId":                {tmpl.LaunchTemplateID},
		"LaunchTemplateData.InstanceType": {"m5.large"},
		"LaunchTemplateData.ImageId":      {"ami-1111111111111111f"},
		"VersionDescription":              {"bigger"},
	}, &created)

	// Then: it is version 2, and not the default
	if created.Version.VersionNumber != 2 {
		t.Errorf("versionNumber = %d, want 2", created.Version.VersionNumber)
	}
	if created.Version.DefaultVersion {
		t.Error("a newly created version reported itself as the default")
	}
	if created.Version.Data.InstanceType != "m5.large" {
		t.Errorf("instanceType = %q, want m5.large", created.Version.Data.InstanceType)
	}

	// And: the template's latest moved but its default did not
	var described describeLaunchTemplatesResponse
	ltDecode(t, srv, "DescribeLaunchTemplates", url.Values{
		"LaunchTemplateId.1": {tmpl.LaunchTemplateID},
	}, &described)
	if len(described.Templates) != 1 {
		t.Fatalf("DescribeLaunchTemplates returned %d templates, want 1", len(described.Templates))
	}
	if got := described.Templates[0]; got.LatestVersionNumber != 2 || got.DefaultVersionNumber != 1 {
		t.Errorf("latest/default = %d/%d, want 2/1", got.LatestVersionNumber, got.DefaultVersionNumber)
	}
}

// TestCreateLaunchTemplateVersion_sourceVersionIsInherited pins AWS's rule
// that a new version inherits the source version's parameters except those it
// specifies itself.
func TestCreateLaunchTemplateVersion_sourceVersionIsInherited(t *testing.T) {
	// Given: a template whose first version sets an AMI and a key pair
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "src-template", url.Values{
		"LaunchTemplateData.KeyName": {"deploy-key"},
	})

	// When: a new version specifies only the instance type, based on version 1
	var created createLaunchTemplateVersionResponse
	ltDecode(t, srv, "CreateLaunchTemplateVersion", url.Values{
		"LaunchTemplateId":                {tmpl.LaunchTemplateID},
		"SourceVersion":                   {"1"},
		"LaunchTemplateData.InstanceType": {"c5.xlarge"},
	}, &created)

	// Then: the AMI and key pair carry over, the instance type does not
	if created.Version.Data.ImageID != "ami-0123456789abcdef0" {
		t.Errorf("imageId = %q, want the source version's AMI", created.Version.Data.ImageID)
	}
	if created.Version.Data.KeyName != "deploy-key" {
		t.Errorf("keyName = %q, want deploy-key", created.Version.Data.KeyName)
	}
	if created.Version.Data.InstanceType != "c5.xlarge" {
		t.Errorf("instanceType = %q, want c5.xlarge", created.Version.Data.InstanceType)
	}
}

// TestDescribeLaunchTemplateVersions_resolvesLatestAndDefault pins the two
// version aliases every launch-template client uses.
func TestDescribeLaunchTemplateVersions_resolvesLatestAndDefault(t *testing.T) {
	// Given: a template with two versions, the first still the default
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "alias-template", nil)
	var created createLaunchTemplateVersionResponse
	ltDecode(t, srv, "CreateLaunchTemplateVersion", url.Values{
		"LaunchTemplateName":              {"alias-template"},
		"LaunchTemplateData.InstanceType": {"m5.large"},
	}, &created)

	// When: the aliases are described
	for _, tc := range []struct {
		alias string
		want  int64
	}{{"$Latest", 2}, {"$Default", 1}} {
		var out describeLaunchTemplateVersionsResponse
		ltDecode(t, srv, "DescribeLaunchTemplateVersions", url.Values{
			"LaunchTemplateId":        {tmpl.LaunchTemplateID},
			"LaunchTemplateVersion.1": {tc.alias},
		}, &out)

		// Then: each resolves to the right version number
		if len(out.Versions) != 1 {
			t.Fatalf("%s returned %d versions, want 1", tc.alias, len(out.Versions))
		}
		if out.Versions[0].VersionNumber != tc.want {
			t.Errorf("%s resolved to version %d, want %d", tc.alias, out.Versions[0].VersionNumber, tc.want)
		}
	}
}

// TestModifyLaunchTemplate_setsDefaultVersion pins the default-version move,
// including via the $Latest alias.
func TestModifyLaunchTemplate_setsDefaultVersion(t *testing.T) {
	// Given: a template with two versions
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "mod-template", nil)
	var created createLaunchTemplateVersionResponse
	ltDecode(t, srv, "CreateLaunchTemplateVersion", url.Values{
		"LaunchTemplateId":                {tmpl.LaunchTemplateID},
		"LaunchTemplateData.InstanceType": {"m5.large"},
	}, &created)

	// When: the default is moved to the latest
	var modified modifyLaunchTemplateResponse
	ltDecode(t, srv, "ModifyLaunchTemplate", url.Values{
		"LaunchTemplateId":  {tmpl.LaunchTemplateID},
		"SetDefaultVersion": {"$Latest"},
	}, &modified)

	// Then: the template reports version 2 as its default
	if modified.LaunchTemplate.DefaultVersionNumber != 2 {
		t.Errorf("defaultVersionNumber = %d, want 2", modified.LaunchTemplate.DefaultVersionNumber)
	}

	// And: $Default now resolves to version 2
	var out describeLaunchTemplateVersionsResponse
	ltDecode(t, srv, "DescribeLaunchTemplateVersions", url.Values{
		"LaunchTemplateId":        {tmpl.LaunchTemplateID},
		"LaunchTemplateVersion.1": {"$Default"},
	}, &out)
	if len(out.Versions) != 1 || out.Versions[0].VersionNumber != 2 {
		t.Errorf("$Default resolved to %+v, want version 2", out.Versions)
	}
	if !out.Versions[0].DefaultVersion {
		t.Error("version 2 does not report defaultVersion=true")
	}
}

// TestModifyLaunchTemplate_unknownVersion pins AWS's version-not-found code.
func TestModifyLaunchTemplate_unknownVersion(t *testing.T) {
	// Given: a template with one version
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "missing-version", nil)

	// When: a version that was never created is made the default
	resp := ec2Query(t, srv, "ModifyLaunchTemplate", url.Values{
		"LaunchTemplateId":  {tmpl.LaunchTemplateID},
		"SetDefaultVersion": {"7"},
	})
	defer resp.Body.Close()

	// Then: AWS's VersionNotFound code comes back
	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidLaunchTemplateId.VersionNotFound")
}

// ─── Deletion ─────────────────────────────────────────────────────────────────

// TestDeleteLaunchTemplateVersions_refusesTheDefault pins that the default
// version cannot be deleted, per-item success reporting, and that a version
// really goes away.
func TestDeleteLaunchTemplateVersions_refusesTheDefault(t *testing.T) {
	// Given: a template with two versions, version 1 the default
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "del-versions", nil)
	var created createLaunchTemplateVersionResponse
	ltDecode(t, srv, "CreateLaunchTemplateVersion", url.Values{
		"LaunchTemplateId":                {tmpl.LaunchTemplateID},
		"LaunchTemplateData.InstanceType": {"m5.large"},
	}, &created)

	// When: the default version is deleted
	resp := ec2Query(t, srv, "DeleteLaunchTemplateVersions", url.Values{
		"LaunchTemplateId":        {tmpl.LaunchTemplateID},
		"LaunchTemplateVersion.1": {"1"},
	})
	defer resp.Body.Close()

	// Then: AWS refuses the whole call
	assertEC2QueryError(t, resp, http.StatusBadRequest, "OperationNotPermitted")

	// When: the non-default version is deleted instead
	var out deleteLaunchTemplateVersionsResponse
	ltDecode(t, srv, "DeleteLaunchTemplateVersions", url.Values{
		"LaunchTemplateId":        {tmpl.LaunchTemplateID},
		"LaunchTemplateVersion.1": {"2"},
		"LaunchTemplateVersion.2": {"9"},
	}, &out)

	// Then: version 2 is reported deleted and version 9 reported missing
	if len(out.Deleted) != 1 || out.Deleted[0].VersionNumber != 2 {
		t.Errorf("successfully deleted = %+v, want version 2", out.Deleted)
	}
	if len(out.Failed) != 1 || out.Failed[0].ResponseError.Code != "launchTemplateVersionDoesNotExist" {
		t.Errorf("unsuccessfully deleted = %+v, want launchTemplateVersionDoesNotExist for version 9", out.Failed)
	}
}

// TestDeleteLaunchTemplate_removesTemplateAndVersions pins that deleting a
// template takes its versions with it and that a later lookup fails with AWS's
// not-found code.
func TestDeleteLaunchTemplate_removesTemplateAndVersions(t *testing.T) {
	// Given: a template with two versions
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "gone-template", nil)
	var created createLaunchTemplateVersionResponse
	ltDecode(t, srv, "CreateLaunchTemplateVersion", url.Values{
		"LaunchTemplateId":                {tmpl.LaunchTemplateID},
		"LaunchTemplateData.InstanceType": {"m5.large"},
	}, &created)

	// When: the template is deleted by name
	var deleted deleteLaunchTemplateResponse
	ltDecode(t, srv, "DeleteLaunchTemplate", url.Values{
		"LaunchTemplateName": {"gone-template"},
	}, &deleted)

	// Then: the deleted template comes back
	if deleted.LaunchTemplate.LaunchTemplateID != tmpl.LaunchTemplateID {
		t.Errorf("deleted id = %q, want %q", deleted.LaunchTemplate.LaunchTemplateID, tmpl.LaunchTemplateID)
	}

	// And: describing it by name now fails the way AWS fails
	resp := ec2Query(t, srv, "DescribeLaunchTemplates", url.Values{
		"LaunchTemplateName.1": {"gone-template"},
	})
	defer resp.Body.Close()
	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidLaunchTemplateName.NotFoundException")

	// And: its versions went with it
	versionsResp := ec2Query(t, srv, "DescribeLaunchTemplateVersions", url.Values{
		"LaunchTemplateId": {tmpl.LaunchTemplateID},
	})
	defer versionsResp.Body.Close()
	assertEC2QueryError(t, versionsResp, http.StatusBadRequest, "InvalidLaunchTemplateId.NotFound")
}

// ─── RunInstances ─────────────────────────────────────────────────────────────

// TestRunInstances_fromLaunchTemplate pins the merge AWS performs: the
// template supplies what the request does not, and an explicit parameter wins.
func TestRunInstances_fromLaunchTemplate(t *testing.T) {
	// Given: a template carrying an AMI, an instance type and instance tags
	srv := helpers.NewTestServer(t)
	tmpl := createTemplate(t, srv, "run-template", url.Values{
		"LaunchTemplateData.KeyName":                         {"deploy-key"},
		"LaunchTemplateData.TagSpecification.1.ResourceType": {"instance"},
		"LaunchTemplateData.TagSpecification.1.Tag.1.Key":    {"Owner"},
		"LaunchTemplateData.TagSpecification.1.Tag.1.Value":  {"platform"},
	})

	// When: instances are run from it, overriding only the instance type
	var out struct {
		XMLName   xml.Name `xml:"RunInstancesResponse"`
		Instances []struct {
			InstanceID   string `xml:"instanceId"`
			ImageID      string `xml:"imageId"`
			InstanceType string `xml:"instanceType"`
			TagSet       []struct {
				Key   string `xml:"key"`
				Value string `xml:"value"`
			} `xml:"tagSet>item"`
		} `xml:"instancesSet>item"`
	}
	ltDecode(t, srv, "RunInstances", url.Values{
		"LaunchTemplate.LaunchTemplateId": {tmpl.LaunchTemplateID},
		"InstanceType":                    {"c5.large"},
		"MinCount":                        {"1"},
		"MaxCount":                        {"1"},
	}, &out)

	// Then: the AMI came from the template and the instance type from the request
	if len(out.Instances) != 1 {
		t.Fatalf("RunInstances returned %d instances, want 1", len(out.Instances))
	}
	inst := out.Instances[0]
	if inst.ImageID != "ami-0123456789abcdef0" {
		t.Errorf("imageId = %q, want the template's AMI", inst.ImageID)
	}
	if inst.InstanceType != "c5.large" {
		t.Errorf("instanceType = %q, want the explicitly-passed c5.large", inst.InstanceType)
	}

	// And: the template's instance tags were applied
	found := false
	for _, tag := range inst.TagSet {
		if tag.Key == "Owner" && tag.Value == "platform" {
			found = true
		}
	}
	if !found {
		t.Errorf("tagSet = %+v, want the template's Owner tag", inst.TagSet)
	}
}

// TestRunInstances_launchTemplateNotFound pins that a template that does not
// exist fails the launch rather than launching a default instance.
func TestRunInstances_launchTemplateNotFound(t *testing.T) {
	// Given: an empty store
	srv := helpers.NewTestServer(t)

	// When: instances are run from a template that was never created
	resp := ec2Query(t, srv, "RunInstances", url.Values{
		"LaunchTemplate.LaunchTemplateName": {"never-created"},
		"MinCount":                          {"1"},
		"MaxCount":                          {"1"},
	})
	defer resp.Body.Close()

	// Then: AWS's not-found code comes back
	assertEC2QueryError(t, resp, http.StatusBadRequest, "InvalidLaunchTemplateName.NotFoundException")
}
