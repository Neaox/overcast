package ec2

// handler_launch_templates.go — CreateLaunchTemplate and the six operations
// around it.
//
// A launch template is a named, versioned bundle of RunInstances parameters.
// It matters here because CDK's autoscaling.AutoScalingGroup emits one by
// default, so without them the modern IaC path has nothing to resolve an
// ImageId and InstanceType from and Auto Scaling has to refuse the group
// (#518).
//
// Two things are worth knowing before reading:
//
//   - The template record holds no launch parameters. Those live in its
//     versions, and the template only records which version is the default and
//     which is the latest — the two numbers `$Default` and `$Latest` resolve
//     to. Every read that needs parameters resolves a version first.
//   - The `lt-` ID is minted with seventeen hex characters rather than the
//     eight every other EC2 resource here uses. Launch templates postdate AWS's
//     move to long resource IDs, so the short form was never valid for one, and
//     AWS's own InvalidLaunchTemplateId.Malformed message spells the shape out
//     as `lt-xxxxxxxxxxxxxxxxx`.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/overcast-sh/overcast/internal/protocol"
)

// Launch template version aliases. AWS accepts either in place of a version
// number wherever a version is named.
const (
	versionAliasLatest  = "$Latest"
	versionAliasDefault = "$Default"
)

// launchTemplateNamePattern is AWS's: 3 to 128 characters drawn from letters,
// digits and `-_./()`. The InvalidLaunchTemplateName.MalformedException
// description in the EC2 API reference is the source.
var launchTemplateNamePattern = regexp.MustCompile(`^[a-zA-Z0-9()./_-]{3,128}$`)

// ── XML response types ───────────────────────────────────────────────────────

type xmlLaunchTemplate struct {
	LaunchTemplateID     string   `xml:"launchTemplateId"`
	LaunchTemplateName   string   `xml:"launchTemplateName"`
	CreateTime           string   `xml:"createTime"`
	CreatedBy            string   `xml:"createdBy"`
	DefaultVersionNumber int64    `xml:"defaultVersionNumber"`
	LatestVersionNumber  int64    `xml:"latestVersionNumber"`
	TagSet               []xmlTag `xml:"tagSet>item,omitempty"`
}

type xmlLaunchTemplateIamProfile struct {
	ARN  string `xml:"arn,omitempty"`
	Name string `xml:"name,omitempty"`
}

type xmlLaunchTemplateNetworkInterface struct {
	AssociatePublicIPAddress *bool    `xml:"associatePublicIpAddress,omitempty"`
	DeviceIndex              *int     `xml:"deviceIndex,omitempty"`
	Groups                   []string `xml:"groupSet>item,omitempty"`
	SubnetID                 string   `xml:"subnetId,omitempty"`
}

type xmlLaunchTemplateTagSpecification struct {
	ResourceType string   `xml:"resourceType"`
	TagSet       []xmlTag `xml:"tagSet>item,omitempty"`
}

type xmlLaunchTemplateData struct {
	IamInstanceProfile *xmlLaunchTemplateIamProfile        `xml:"iamInstanceProfile,omitempty"`
	ImageID            string                              `xml:"imageId,omitempty"`
	InstanceType       string                              `xml:"instanceType,omitempty"`
	KeyName            string                              `xml:"keyName,omitempty"`
	NetworkInterfaces  []xmlLaunchTemplateNetworkInterface `xml:"networkInterfaceSet>item,omitempty"`
	SecurityGroupIDs   []string                            `xml:"securityGroupIdSet>item,omitempty"`
	SecurityGroups     []string                            `xml:"securityGroupSet>item,omitempty"`
	TagSpecifications  []xmlLaunchTemplateTagSpecification `xml:"tagSpecificationSet>item,omitempty"`
	UserData           string                              `xml:"userData,omitempty"`
}

type xmlLaunchTemplateVersion struct {
	CreateTime         string                `xml:"createTime"`
	CreatedBy          string                `xml:"createdBy"`
	DefaultVersion     bool                  `xml:"defaultVersion"`
	LaunchTemplateData xmlLaunchTemplateData `xml:"launchTemplateData"`
	LaunchTemplateID   string                `xml:"launchTemplateId"`
	LaunchTemplateName string                `xml:"launchTemplateName"`
	VersionDescription string                `xml:"versionDescription,omitempty"`
	VersionNumber      int64                 `xml:"versionNumber"`
}

type xmlCreateLaunchTemplateResponse struct {
	XMLName        xml.Name          `xml:"CreateLaunchTemplateResponse"`
	Xmlns          string            `xml:"xmlns,attr"`
	RequestID      string            `xml:"requestId"`
	LaunchTemplate xmlLaunchTemplate `xml:"launchTemplate"`
}

type xmlCreateLaunchTemplateVersionResponse struct {
	XMLName   xml.Name                 `xml:"CreateLaunchTemplateVersionResponse"`
	Xmlns     string                   `xml:"xmlns,attr"`
	RequestID string                   `xml:"requestId"`
	Version   xmlLaunchTemplateVersion `xml:"launchTemplateVersion"`
}

type xmlDescribeLaunchTemplatesResponse struct {
	XMLName   xml.Name            `xml:"DescribeLaunchTemplatesResponse"`
	Xmlns     string              `xml:"xmlns,attr"`
	RequestID string              `xml:"requestId"`
	Templates []xmlLaunchTemplate `xml:"launchTemplates>item"`
}

type xmlDescribeLaunchTemplateVersionsResponse struct {
	XMLName   xml.Name                   `xml:"DescribeLaunchTemplateVersionsResponse"`
	Xmlns     string                     `xml:"xmlns,attr"`
	RequestID string                     `xml:"requestId"`
	Versions  []xmlLaunchTemplateVersion `xml:"launchTemplateVersionSet>item"`
}

type xmlModifyLaunchTemplateResponse struct {
	XMLName        xml.Name          `xml:"ModifyLaunchTemplateResponse"`
	Xmlns          string            `xml:"xmlns,attr"`
	RequestID      string            `xml:"requestId"`
	LaunchTemplate xmlLaunchTemplate `xml:"launchTemplate"`
}

type xmlDeleteLaunchTemplateResponse struct {
	XMLName        xml.Name          `xml:"DeleteLaunchTemplateResponse"`
	Xmlns          string            `xml:"xmlns,attr"`
	RequestID      string            `xml:"requestId"`
	LaunchTemplate xmlLaunchTemplate `xml:"launchTemplate"`
}

type xmlDeletedVersionItem struct {
	LaunchTemplateID   string `xml:"launchTemplateId"`
	LaunchTemplateName string `xml:"launchTemplateName"`
	VersionNumber      int64  `xml:"versionNumber"`
}

type xmlResponseError struct {
	Code    string `xml:"code"`
	Message string `xml:"message"`
}

type xmlUndeletedVersionItem struct {
	LaunchTemplateID   string           `xml:"launchTemplateId"`
	LaunchTemplateName string           `xml:"launchTemplateName"`
	ResponseError      xmlResponseError `xml:"responseError"`
	VersionNumber      int64            `xml:"versionNumber"`
}

type xmlDeleteLaunchTemplateVersionsResponse struct {
	XMLName   xml.Name                  `xml:"DeleteLaunchTemplateVersionsResponse"`
	Xmlns     string                    `xml:"xmlns,attr"`
	RequestID string                    `xml:"requestId"`
	Deleted   []xmlDeletedVersionItem   `xml:"successfullyDeletedLaunchTemplateVersionSet>item"`
	Failed    []xmlUndeletedVersionItem `xml:"unsuccessfullyDeletedLaunchTemplateVersionSet>item"`
}

// ── Errors ───────────────────────────────────────────────────────────────────

// The codes and their wording come from the EC2 API reference's client error
// table (docs.aws.amazon.com/AWSEC2/latest/APIReference/errors-overview.html).

func errLaunchTemplateIDNotFound(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidLaunchTemplateId.NotFound",
		Message:    fmt.Sprintf("The specified launch template, with ID %s, does not exist.", id),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errLaunchTemplateNameNotFound(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidLaunchTemplateName.NotFoundException",
		Message:    fmt.Sprintf("The specified launch template, with template name %s, does not exist.", name),
		HTTPStatus: http.StatusBadRequest,
	}
}

func errLaunchTemplateVersionNotFound(version string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "InvalidLaunchTemplateId.VersionNotFound",
		Message:    fmt.Sprintf("The specified launch template version %s does not exist.", version),
		HTTPStatus: http.StatusBadRequest,
	}
}

// errLaunchTemplateDataRequired is AWS's answer to a create with no launch
// parameters at all: LaunchTemplateData is a required member, and a template
// with nothing in it could launch nothing.
func errLaunchTemplateDataRequired() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "MissingParameter",
		Message:    "The request must contain at least one launch template parameter.",
		HTTPStatus: http.StatusBadRequest,
	}
}

// errLaunchTemplateNotSelected is AWS's answer when neither identifier is
// given to an operation that acts on exactly one template.
func errLaunchTemplateNotSelected() *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "MissingParameter",
		Message:    "The request must contain either the launch template ID or the launch template name.",
		HTTPStatus: http.StatusBadRequest,
	}
}

// ── Resolution ───────────────────────────────────────────────────────────────

// launchTemplateRef names a launch template the way every operation and
// RunInstances name one: by ID or by name, optionally at a version.
type launchTemplateRef struct {
	ID      string
	Name    string
	Version string
}

func (ref launchTemplateRef) named() bool { return ref.ID != "" || ref.Name != "" }

// empty reports whether a parsed LaunchTemplateData carries no parameters at
// all, which AWS refuses on a create.
func (d LaunchTemplateData) empty() bool {
	return d.ImageID == "" && d.InstanceType == "" && d.KeyName == "" && d.UserData == "" &&
		len(d.SecurityGroupIDs) == 0 && len(d.SecurityGroups) == 0 &&
		d.IamInstanceProfile == nil && len(d.NetworkInterfaces) == 0 && len(d.TagSpecifications) == 0
}

// resolveLaunchTemplate returns the template a reference names, with AWS's
// per-identifier not-found code when it does not exist.
func (h *Handler) resolveLaunchTemplate(ctx context.Context, ref launchTemplateRef) (*LaunchTemplate, *protocol.AWSError) {
	switch {
	case ref.ID != "":
		lt, aerr := h.store.getLaunchTemplate(ctx, ref.ID)
		if aerr != nil {
			return nil, aerr
		}
		if lt == nil {
			return nil, errLaunchTemplateIDNotFound(ref.ID)
		}
		return lt, nil
	case ref.Name != "":
		lt, aerr := h.store.getLaunchTemplateByName(ctx, ref.Name)
		if aerr != nil {
			return nil, aerr
		}
		if lt == nil {
			return nil, errLaunchTemplateNameNotFound(ref.Name)
		}
		return lt, nil
	default:
		return nil, errLaunchTemplateNotSelected()
	}
}

// resolveVersionNumber turns a version string into a number. An empty string
// means the default version, which is what RunInstances uses when a launch
// template is named without one.
func resolveVersionNumber(lt *LaunchTemplate, version string) (int64, *protocol.AWSError) {
	switch version {
	case "", versionAliasDefault:
		return lt.DefaultVersionNumber, nil
	case versionAliasLatest:
		return lt.LatestVersionNumber, nil
	}
	n, err := strconv.ParseInt(version, 10, 64)
	if err != nil || n < 1 {
		return 0, errLaunchTemplateVersionNotFound(version)
	}
	return n, nil
}

// resolveLaunchTemplateVersion resolves a reference all the way to one stored
// version, applying the $Latest/$Default aliases.
func (h *Handler) resolveLaunchTemplateVersion(ctx context.Context, ref launchTemplateRef) (*LaunchTemplate, *LaunchTemplateVersion, *protocol.AWSError) {
	lt, aerr := h.resolveLaunchTemplate(ctx, ref)
	if aerr != nil {
		return nil, nil, aerr
	}
	number, aerr := resolveVersionNumber(lt, ref.Version)
	if aerr != nil {
		return nil, nil, aerr
	}
	version, aerr := h.store.getLaunchTemplateVersion(ctx, lt.LaunchTemplateID, number)
	if aerr != nil {
		return nil, nil, aerr
	}
	if version == nil {
		return nil, nil, errLaunchTemplateVersionNotFound(strconv.FormatInt(number, 10))
	}
	return lt, version, nil
}

// ── Request parsing ──────────────────────────────────────────────────────────

// launchTemplateRefFromForm reads the LaunchTemplateId/LaunchTemplateName/
// Version trio an operation carries at the top level of its request.
func launchTemplateRefFromForm(r *http.Request, prefix string) launchTemplateRef {
	return launchTemplateRef{
		ID:      r.FormValue(prefix + "LaunchTemplateId"),
		Name:    r.FormValue(prefix + "LaunchTemplateName"),
		Version: r.FormValue(prefix + "Version"),
	}
}

// parseLaunchTemplateData reads a LaunchTemplateData.* sub-structure off the
// form. The parameter names are the ones the AWS SDKs serialise: flattened
// lists are `SecurityGroupId.N`, not `SecurityGroupIds.N`.
func parseLaunchTemplateData(r *http.Request, prefix string) LaunchTemplateData {
	data := LaunchTemplateData{
		ImageID:          r.FormValue(prefix + "ImageId"),
		InstanceType:     r.FormValue(prefix + "InstanceType"),
		KeyName:          r.FormValue(prefix + "KeyName"),
		UserData:         r.FormValue(prefix + "UserData"),
		SecurityGroupIDs: parseIndexedParam(r, prefix+"SecurityGroupId"),
		SecurityGroups:   parseIndexedParam(r, prefix+"SecurityGroup"),
	}
	if arn, name := r.FormValue(prefix+"IamInstanceProfile.Arn"), r.FormValue(prefix+"IamInstanceProfile.Name"); arn != "" || name != "" {
		data.IamInstanceProfile = &LaunchTemplateIamProfile{ARN: arn, Name: name}
	}
	data.NetworkInterfaces = parseLaunchTemplateNetworkInterfaces(r, prefix)
	data.TagSpecifications = parseLaunchTemplateTagSpecifications(r, prefix)
	return data
}

func parseLaunchTemplateNetworkInterfaces(r *http.Request, prefix string) []LaunchTemplateNetworkInterface {
	var out []LaunchTemplateNetworkInterface
	for n := 1; ; n++ {
		base := fmt.Sprintf("%sNetworkInterface.%d.", prefix, n)
		subnet := r.FormValue(base + "SubnetId")
		deviceIndex := r.FormValue(base + "DeviceIndex")
		associate := r.FormValue(base + "AssociatePublicIpAddress")
		groups := parseIndexedParam(r, base+"SecurityGroupId")
		if subnet == "" && deviceIndex == "" && associate == "" && len(groups) == 0 {
			break
		}
		nic := LaunchTemplateNetworkInterface{SubnetID: subnet, Groups: groups}
		if deviceIndex != "" {
			if idx, err := strconv.Atoi(deviceIndex); err == nil {
				nic.DeviceIndex = &idx
			}
		}
		if associate != "" {
			value := associate == "true"
			nic.AssociatePublicIPAddress = &value
		}
		out = append(out, nic)
	}
	return out
}

func parseLaunchTemplateTagSpecifications(r *http.Request, prefix string) []LaunchTemplateTagSpecification {
	var out []LaunchTemplateTagSpecification
	for n := 1; ; n++ {
		base := fmt.Sprintf("%sTagSpecification.%d.", prefix, n)
		resourceType := r.FormValue(base + "ResourceType")
		if resourceType == "" {
			break
		}
		spec := LaunchTemplateTagSpecification{ResourceType: resourceType}
		for m := 1; ; m++ {
			key := r.FormValue(fmt.Sprintf("%sTag.%d.Key", base, m))
			if key == "" {
				break
			}
			spec.Tags = append(spec.Tags, Tag{Key: key, Value: r.FormValue(fmt.Sprintf("%sTag.%d.Value", base, m))})
		}
		out = append(out, spec)
	}
	return out
}

// ── Rendering ────────────────────────────────────────────────────────────────

func renderLaunchTemplate(lt *LaunchTemplate, tags []Tag) xmlLaunchTemplate {
	return xmlLaunchTemplate{
		LaunchTemplateID:     lt.LaunchTemplateID,
		LaunchTemplateName:   lt.LaunchTemplateName,
		CreateTime:           lt.CreateTime,
		CreatedBy:            lt.CreatedBy,
		DefaultVersionNumber: lt.DefaultVersionNumber,
		LatestVersionNumber:  lt.LatestVersionNumber,
		TagSet:               xmlTagsOf(tags),
	}
}

func renderLaunchTemplateData(data LaunchTemplateData) xmlLaunchTemplateData {
	out := xmlLaunchTemplateData{
		ImageID:          data.ImageID,
		InstanceType:     data.InstanceType,
		KeyName:          data.KeyName,
		UserData:         data.UserData,
		SecurityGroupIDs: data.SecurityGroupIDs,
		SecurityGroups:   data.SecurityGroups,
	}
	if data.IamInstanceProfile != nil {
		out.IamInstanceProfile = &xmlLaunchTemplateIamProfile{
			ARN:  data.IamInstanceProfile.ARN,
			Name: data.IamInstanceProfile.Name,
		}
	}
	for _, nic := range data.NetworkInterfaces {
		out.NetworkInterfaces = append(out.NetworkInterfaces, xmlLaunchTemplateNetworkInterface{
			AssociatePublicIPAddress: nic.AssociatePublicIPAddress,
			DeviceIndex:              nic.DeviceIndex,
			Groups:                   nic.Groups,
			SubnetID:                 nic.SubnetID,
		})
	}
	for _, spec := range data.TagSpecifications {
		out.TagSpecifications = append(out.TagSpecifications, xmlLaunchTemplateTagSpecification{
			ResourceType: spec.ResourceType,
			TagSet:       xmlTagsOf(spec.Tags),
		})
	}
	return out
}

func renderLaunchTemplateVersion(lt *LaunchTemplate, v *LaunchTemplateVersion) xmlLaunchTemplateVersion {
	return xmlLaunchTemplateVersion{
		CreateTime:         v.CreateTime,
		CreatedBy:          v.CreatedBy,
		DefaultVersion:     v.VersionNumber == lt.DefaultVersionNumber,
		LaunchTemplateData: renderLaunchTemplateData(v.Data),
		LaunchTemplateID:   v.LaunchTemplateID,
		LaunchTemplateName: v.LaunchTemplateName,
		VersionDescription: v.VersionDescription,
		VersionNumber:      v.VersionNumber,
	}
}

// createdBy is the ARN AWS records as the principal that made a launch
// template. Overcast does not authenticate callers, so it names the account
// root, which is what an account-root-credentialled call reports on AWS.
func (h *Handler) createdBy() string {
	return "arn:aws:iam::" + h.cfg.AccountID + ":root"
}

// ── CreateLaunchTemplate ─────────────────────────────────────────────────────

// CreateLaunchTemplate handles Action=CreateLaunchTemplate.
func (h *Handler) CreateLaunchTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	name := r.FormValue("LaunchTemplateName")
	if name == "" {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "The request must contain the parameter launchTemplateName.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	if !launchTemplateNamePattern.MatchString(name) {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code: "InvalidLaunchTemplateName.MalformedException",
			Message: "The specified launch template name is invalid. A launch template name must be " +
				"between 3 and 128 characters, and may contain letters, numbers, and the following " +
				"characters: '-', '_', '.', '/', '(', and ')'.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}
	existing, aerr := h.store.getLaunchTemplateByName(ctx, name)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if existing != nil {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidLaunchTemplateName.AlreadyExistsException",
			Message:    fmt.Sprintf("Launch template name already in use: %s", name),
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	// Create-time tags are checked before the template is made, as on AWS.
	tags := parseTagSpecifications(r, "launch-template")
	if aerr := validateTagSpecifications(tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	data := parseLaunchTemplateData(r, "LaunchTemplateData.")
	if data.empty() {
		protocol.WriteEC2QueryXMLError(w, r, errLaunchTemplateDataRequired())
		return
	}

	now := h.clk.Now().UTC().Format(time.RFC3339)
	lt := &LaunchTemplate{
		LaunchTemplateID:     "lt-" + longID(),
		LaunchTemplateName:   name,
		CreateTime:           now,
		CreatedBy:            h.createdBy(),
		DefaultVersionNumber: 1,
		LatestVersionNumber:  1,
	}
	version := &LaunchTemplateVersion{
		LaunchTemplateID:   lt.LaunchTemplateID,
		LaunchTemplateName: lt.LaunchTemplateName,
		VersionNumber:      1,
		VersionDescription: r.FormValue("VersionDescription"),
		CreateTime:         now,
		CreatedBy:          lt.CreatedBy,
		Data:               data,
	}
	if aerr := h.store.putLaunchTemplateVersion(ctx, version); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.putLaunchTemplate(ctx, lt); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.putResourceTags(ctx, lt.LaunchTemplateID, tags); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateLaunchTemplateResponse{
		Xmlns:          ec2XMLNS,
		RequestID:      protocol.RequestIDFromContext(ctx),
		LaunchTemplate: renderLaunchTemplate(lt, sortTags(tags)),
	})
}

// ── CreateLaunchTemplateVersion ──────────────────────────────────────────────

// CreateLaunchTemplateVersion handles Action=CreateLaunchTemplateVersion.
//
// AWS's SourceVersion rule is the one thing here that is not a plain write: a
// new version based on a source version inherits that version's parameters
// except the ones the request specifies for itself.
func (h *Handler) CreateLaunchTemplateVersion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ref := launchTemplateRefFromForm(r, "")
	lt, aerr := h.resolveLaunchTemplate(ctx, ref)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	data := parseLaunchTemplateData(r, "LaunchTemplateData.")
	if source := r.FormValue("SourceVersion"); source != "" {
		number, aerr := resolveVersionNumber(lt, source)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		base, aerr := h.store.getLaunchTemplateVersion(ctx, lt.LaunchTemplateID, number)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		if base == nil {
			protocol.WriteEC2QueryXMLError(w, r, errLaunchTemplateVersionNotFound(source))
			return
		}
		data = mergeLaunchTemplateData(data, base.Data)
	}

	now := h.clk.Now().UTC().Format(time.RFC3339)
	version := &LaunchTemplateVersion{
		LaunchTemplateID:   lt.LaunchTemplateID,
		LaunchTemplateName: lt.LaunchTemplateName,
		VersionNumber:      lt.LatestVersionNumber + 1,
		VersionDescription: r.FormValue("VersionDescription"),
		CreateTime:         now,
		CreatedBy:          h.createdBy(),
		Data:               data,
	}
	if aerr := h.store.putLaunchTemplateVersion(ctx, version); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	lt.LatestVersionNumber = version.VersionNumber
	if aerr := h.store.putLaunchTemplate(ctx, lt); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlCreateLaunchTemplateVersionResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Version:   renderLaunchTemplateVersion(lt, version),
	})
}

// mergeLaunchTemplateData fills in over's empty fields from under. It is the
// one merge rule launch templates need, and both callers want it: a new
// version inheriting from its source version, and RunInstances layering a
// template beneath explicitly-passed parameters.
func mergeLaunchTemplateData(over, under LaunchTemplateData) LaunchTemplateData {
	if over.ImageID == "" {
		over.ImageID = under.ImageID
	}
	if over.InstanceType == "" {
		over.InstanceType = under.InstanceType
	}
	if over.KeyName == "" {
		over.KeyName = under.KeyName
	}
	if over.UserData == "" {
		over.UserData = under.UserData
	}
	if len(over.SecurityGroupIDs) == 0 {
		over.SecurityGroupIDs = under.SecurityGroupIDs
	}
	if len(over.SecurityGroups) == 0 {
		over.SecurityGroups = under.SecurityGroups
	}
	if over.IamInstanceProfile == nil {
		over.IamInstanceProfile = under.IamInstanceProfile
	}
	if len(over.NetworkInterfaces) == 0 {
		over.NetworkInterfaces = under.NetworkInterfaces
	}
	if len(over.TagSpecifications) == 0 {
		over.TagSpecifications = under.TagSpecifications
	}
	return over
}

// ── DescribeLaunchTemplates ──────────────────────────────────────────────────

var launchTemplateFilters = declareFilters(filterSpec[*LaunchTemplate]{
	op:     "DescribeLaunchTemplates",
	tagged: true,
	attrs: map[string]filterAttr[*LaunchTemplate]{
		"create-time":          attr(func(lt *LaunchTemplate) string { return lt.CreateTime }),
		"launch-template-name": attr(func(lt *LaunchTemplate) string { return lt.LaunchTemplateName }),
	},
})

// DescribeLaunchTemplates handles Action=DescribeLaunchTemplates.
//
// Both selectors are lists on AWS, and either may name a template that does
// not exist — which is an error rather than an empty result, the same way
// DescribeInstances errors on an unknown instance ID.
func (h *Handler) DescribeLaunchTemplates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	filters, aerr := launchTemplateFilters.parse(eachFilter(r))
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	requestedIDs := selectedIDs(parseIndexedParam(r, "LaunchTemplateId"))
	requestedNames := selectedIDs(parseIndexedParam(r, "LaunchTemplateName"))
	// AWS takes one selector or the other, never both.
	if len(requestedIDs) > 0 && len(requestedNames) > 0 {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "InvalidParameterCombination",
			Message:    "You may specify launch template IDs or launch template names, but not both.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	all, aerr := h.store.listLaunchTemplates(ctx)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	byID := make(map[string]bool, len(all))
	byName := make(map[string]bool, len(all))
	for _, lt := range all {
		byID[lt.LaunchTemplateID] = true
		byName[lt.LaunchTemplateName] = true
	}
	for id := range requestedIDs {
		if !byID[id] {
			protocol.WriteEC2QueryXMLError(w, r, errLaunchTemplateIDNotFound(id))
			return
		}
	}
	for name := range requestedNames {
		if !byName[name] {
			protocol.WriteEC2QueryXMLError(w, r, errLaunchTemplateNameNotFound(name))
			return
		}
	}

	tagsView, aerr := h.tagViewFor(ctx, eachFilter(r), true)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	var items []xmlLaunchTemplate
	for _, lt := range all {
		if !requestedIDs.has(lt.LaunchTemplateID) || !requestedNames.has(lt.LaunchTemplateName) {
			continue
		}
		if !filters.matches(lt) {
			continue
		}
		tags, keep := tagsView.keep(lt.LaunchTemplateID)
		if !keep {
			continue
		}
		items = append(items, renderLaunchTemplate(lt, tags))
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeLaunchTemplatesResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Templates: items,
	})
}

// ── DescribeLaunchTemplateVersions ───────────────────────────────────────────

// DescribeLaunchTemplateVersions handles Action=DescribeLaunchTemplateVersions.
//
// With no LaunchTemplateVersion.N it returns every version, bounded by
// MinVersion/MaxVersion; with one it returns exactly the versions named,
// resolving $Latest and $Default.
func (h *Handler) DescribeLaunchTemplateVersions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lt, aerr := h.resolveLaunchTemplate(ctx, launchTemplateRefFromForm(r, ""))
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	requested := parseIndexedParam(r, "LaunchTemplateVersion")
	var versions []*LaunchTemplateVersion
	if len(requested) > 0 {
		for _, want := range requested {
			number, aerr := resolveVersionNumber(lt, want)
			if aerr != nil {
				protocol.WriteEC2QueryXMLError(w, r, aerr)
				return
			}
			version, aerr := h.store.getLaunchTemplateVersion(ctx, lt.LaunchTemplateID, number)
			if aerr != nil {
				protocol.WriteEC2QueryXMLError(w, r, aerr)
				return
			}
			if version == nil {
				protocol.WriteEC2QueryXMLError(w, r, errLaunchTemplateVersionNotFound(want))
				return
			}
			versions = append(versions, version)
		}
	} else {
		all, aerr := h.store.listLaunchTemplateVersions(ctx, lt.LaunchTemplateID)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		minVersion, maxVersion := versionBound(r, "MinVersion", 0), versionBound(r, "MaxVersion", 0)
		for _, v := range all {
			if minVersion > 0 && v.VersionNumber < minVersion {
				continue
			}
			if maxVersion > 0 && v.VersionNumber > maxVersion {
				continue
			}
			versions = append(versions, v)
		}
	}

	items := make([]xmlLaunchTemplateVersion, 0, len(versions))
	for _, v := range versions {
		items = append(items, renderLaunchTemplateVersion(lt, v))
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDescribeLaunchTemplateVersionsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
		Versions:  items,
	})
}

// versionBound reads MinVersion/MaxVersion, which AWS types as strings.
func versionBound(r *http.Request, key string, def int64) int64 {
	raw := r.FormValue(key)
	if raw == "" {
		return def
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// ── ModifyLaunchTemplate ─────────────────────────────────────────────────────

// ModifyLaunchTemplate handles Action=ModifyLaunchTemplate. The default
// version is the only property AWS lets it change.
func (h *Handler) ModifyLaunchTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lt, aerr := h.resolveLaunchTemplate(ctx, launchTemplateRefFromForm(r, ""))
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}

	if wanted := r.FormValue("SetDefaultVersion"); wanted != "" {
		number, aerr := resolveVersionNumber(lt, wanted)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		version, aerr := h.store.getLaunchTemplateVersion(ctx, lt.LaunchTemplateID, number)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		if version == nil {
			protocol.WriteEC2QueryXMLError(w, r, errLaunchTemplateVersionNotFound(wanted))
			return
		}
		lt.DefaultVersionNumber = number
		if aerr := h.store.putLaunchTemplate(ctx, lt); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
	}

	tags, aerr := h.launchTemplateTags(ctx, lt.LaunchTemplateID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlModifyLaunchTemplateResponse{
		Xmlns:          ec2XMLNS,
		RequestID:      protocol.RequestIDFromContext(ctx),
		LaunchTemplate: renderLaunchTemplate(lt, tags),
	})
}

// launchTemplateTags reads a template's own tags, sorted for a stable
// response.
func (h *Handler) launchTemplateTags(ctx context.Context, id string) ([]Tag, *protocol.AWSError) {
	stored, aerr := h.store.getTags(ctx, id)
	if aerr != nil {
		return nil, aerr
	}
	tags := make([]Tag, 0, len(stored))
	for k, v := range stored {
		tags = append(tags, Tag{Key: k, Value: v})
	}
	return sortTags(tags), nil
}

// ── DeleteLaunchTemplate ─────────────────────────────────────────────────────

// DeleteLaunchTemplate handles Action=DeleteLaunchTemplate, removing the
// template and every version it owns. AWS returns the deleted template.
func (h *Handler) DeleteLaunchTemplate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lt, aerr := h.resolveLaunchTemplate(ctx, launchTemplateRefFromForm(r, ""))
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	tags, aerr := h.launchTemplateTags(ctx, lt.LaunchTemplateID)
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	if aerr := h.store.deleteLaunchTemplate(ctx, lt.LaunchTemplateID); aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	protocol.WriteQueryXML(w, r, http.StatusOK, &xmlDeleteLaunchTemplateResponse{
		Xmlns:          ec2XMLNS,
		RequestID:      protocol.RequestIDFromContext(ctx),
		LaunchTemplate: renderLaunchTemplate(lt, tags),
	})
}

// ── DeleteLaunchTemplateVersions ─────────────────────────────────────────────

// DeleteLaunchTemplateVersions handles Action=DeleteLaunchTemplateVersions.
//
// This is one of the few EC2 operations with per-item outcomes: a version that
// does not exist is reported in the unsuccessful set rather than failing the
// call. Naming the default version is different — AWS refuses the whole
// request, because deleting it would leave `$Default` unresolvable.
func (h *Handler) DeleteLaunchTemplateVersions(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lt, aerr := h.resolveLaunchTemplate(ctx, launchTemplateRefFromForm(r, ""))
	if aerr != nil {
		protocol.WriteEC2QueryXMLError(w, r, aerr)
		return
	}
	requested := parseIndexedParam(r, "LaunchTemplateVersion")
	if len(requested) == 0 {
		protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
			Code:       "MissingParameter",
			Message:    "The request must contain the parameter versions.",
			HTTPStatus: http.StatusBadRequest,
		})
		return
	}

	resolved := make([]int64, 0, len(requested))
	for _, want := range requested {
		number, aerr := resolveVersionNumber(lt, want)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		if number == lt.DefaultVersionNumber {
			protocol.WriteEC2QueryXMLError(w, r, &protocol.AWSError{
				Code: "OperationNotPermitted",
				Message: "Cannot delete the default version of a launch template. Please modify the " +
					"launch template to have a different default version, then try again.",
				HTTPStatus: http.StatusBadRequest,
			})
			return
		}
		resolved = append(resolved, number)
	}

	resp := &xmlDeleteLaunchTemplateVersionsResponse{
		Xmlns:     ec2XMLNS,
		RequestID: protocol.RequestIDFromContext(ctx),
	}
	highest := int64(0)
	for _, number := range resolved {
		version, aerr := h.store.getLaunchTemplateVersion(ctx, lt.LaunchTemplateID, number)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		if version == nil {
			resp.Failed = append(resp.Failed, xmlUndeletedVersionItem{
				LaunchTemplateID:   lt.LaunchTemplateID,
				LaunchTemplateName: lt.LaunchTemplateName,
				VersionNumber:      number,
				ResponseError: xmlResponseError{
					Code:    "launchTemplateVersionDoesNotExist",
					Message: fmt.Sprintf("The specified launch template version %d does not exist.", number),
				},
			})
			continue
		}
		if aerr := h.store.deleteLaunchTemplateVersion(ctx, lt.LaunchTemplateID, number); aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		resp.Deleted = append(resp.Deleted, xmlDeletedVersionItem{
			LaunchTemplateID:   lt.LaunchTemplateID,
			LaunchTemplateName: lt.LaunchTemplateName,
			VersionNumber:      number,
		})
		if number > highest {
			highest = number
		}
	}

	// AWS keeps LatestVersionNumber pointing at a version that exists: it is
	// what `$Latest` resolves to, so deleting the newest version moves it back
	// to the newest one still there.
	if highest == lt.LatestVersionNumber {
		remaining, aerr := h.store.listLaunchTemplateVersions(ctx, lt.LaunchTemplateID)
		if aerr != nil {
			protocol.WriteEC2QueryXMLError(w, r, aerr)
			return
		}
		if len(remaining) > 0 {
			lt.LatestVersionNumber = remaining[len(remaining)-1].VersionNumber
			if aerr := h.store.putLaunchTemplate(ctx, lt); aerr != nil {
				protocol.WriteEC2QueryXMLError(w, r, aerr)
				return
			}
		}
	}

	protocol.WriteQueryXML(w, r, http.StatusOK, resp)
}

// ── RunInstances integration ─────────────────────────────────────────────────

// runLaunchTemplateOverlay is the launch parameters a RunInstances call takes
// from a launch template, already resolved. It is the zero struct for the
// overwhelmingly common call that names no template, so the hot path allocates
// nothing and reads no store.
//
// It carries the members of LaunchTemplateData an instance record can hold.
// KeyName, UserData, IamInstanceProfile and SecurityGroups (by name) are
// stored on the template and returned by DescribeLaunchTemplateVersions, but
// Overcast's instance record has nowhere to put them — RunInstances drops an
// explicitly-passed KeyName for the same reason, so a template changes nothing
// about which parameters have an effect.
type runLaunchTemplateOverlay struct {
	imageID          string
	instanceType     string
	securityGroupIDs []string
	subnetID         string
	tagSpecs         []LaunchTemplateTagSpecification
}

// launchTemplateOverlayFor resolves the template a RunInstances call names.
// A caller that named none gets the zero overlay and no error, which is what
// keeps the ordinary launch path free of both a store read and an allocation.
func (h *Handler) launchTemplateOverlayFor(ctx context.Context, ref launchTemplateRef) (runLaunchTemplateOverlay, *protocol.AWSError) {
	if !ref.named() {
		return runLaunchTemplateOverlay{}, nil
	}
	_, version, aerr := h.resolveLaunchTemplateVersion(ctx, ref)
	if aerr != nil {
		return runLaunchTemplateOverlay{}, aerr
	}
	data := version.Data
	overlay := runLaunchTemplateOverlay{
		imageID:          data.ImageID,
		instanceType:     data.InstanceType,
		securityGroupIDs: data.SecurityGroupIDs,
		tagSpecs:         data.TagSpecifications,
	}
	// A template that configures network interfaces puts the subnet and the
	// security groups there instead of at the top level, and AWS forbids
	// setting both. The primary interface is the one a launch follows.
	if nic, ok := primaryNetworkInterface(data.NetworkInterfaces); ok {
		if overlay.subnetID == "" {
			overlay.subnetID = nic.SubnetID
		}
		if len(overlay.securityGroupIDs) == 0 {
			overlay.securityGroupIDs = nic.Groups
		}
	}
	return overlay, nil
}

// primaryNetworkInterface returns the device-index-0 interface, or the first
// one when none says which it is.
func primaryNetworkInterface(nics []LaunchTemplateNetworkInterface) (LaunchTemplateNetworkInterface, bool) {
	if len(nics) == 0 {
		return LaunchTemplateNetworkInterface{}, false
	}
	for _, nic := range nics {
		if nic.DeviceIndex != nil && *nic.DeviceIndex == 0 {
			return nic, true
		}
	}
	return nics[0], true
}

// instanceTags returns the overlay's instance-scoped TagSpecifications as the
// flat tag list RunInstances writes.
func (o runLaunchTemplateOverlay) instanceTags() []Tag {
	var tags []Tag
	for _, spec := range o.tagSpecs {
		if !strings.EqualFold(spec.ResourceType, "instance") {
			continue
		}
		tags = append(tags, spec.Tags...)
	}
	return tags
}

// applyString returns explicit when it is set and the template's value
// otherwise, which is the direction AWS merges in: a parameter passed to
// RunInstances wins over the template that would have supplied it.
func applyString(explicit, fromTemplate string) string {
	if explicit != "" {
		return explicit
	}
	return fromTemplate
}

// applyList is applyString for a repeated parameter.
func applyList(explicit, fromTemplate []string) []string {
	if len(explicit) > 0 {
		return explicit
	}
	return fromTemplate
}

// applyTags is applyString for a create call's tags. AWS replaces the
// template's instance tags wholesale when the request carries its own rather
// than merging the two sets key by key.
func applyTags(explicit, fromTemplate []Tag) []Tag {
	if len(explicit) > 0 {
		return explicit
	}
	return fromTemplate
}
