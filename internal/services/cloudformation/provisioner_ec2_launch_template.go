package cloudformation

// AWS::EC2::LaunchTemplate. CDK's autoscaling.AutoScalingGroup synthesizes one
// of these plus a group that references it, so this handler and the ASG one
// are the pair a modern CDK stack lands on (#518).
//
// The only work here that is not a property copy is the update rule, and it is
// AWS's: LaunchTemplateName requires replacement, everything else is applied
// by creating a new version of the same template. CloudFormation then makes
// that version the default, which is what lets a group pinned to `$Default`
// pick the change up.

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"

	"github.com/overcast-sh/overcast/internal/config"
)

// maxNameLenLaunchTemplate is AWS's launch-template name limit. Its
// InvalidLaunchTemplateName.MalformedException description states the range as
// 3 to 128 characters.
const maxNameLenLaunchTemplate = 128

// ec2LaunchTemplateResponse is the `launchTemplate` element CreateLaunchTemplate
// and ModifyLaunchTemplate both answer with. It names no root element on
// purpose, so one shape reads either response.
type ec2LaunchTemplateResponse struct {
	LaunchTemplateID     string `xml:"launchTemplate>launchTemplateId"`
	LaunchTemplateName   string `xml:"launchTemplate>launchTemplateName"`
	DefaultVersionNumber int64  `xml:"launchTemplate>defaultVersionNumber"`
	LatestVersionNumber  int64  `xml:"launchTemplate>latestVersionNumber"`
}

type ec2LaunchTemplateHandler struct{}

func (h *ec2LaunchTemplateHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	name, _ := props["LaunchTemplateName"].(string)
	if name == "" {
		name = rCtx.generatedNameWithin(maxNameLenLaunchTemplate)
	}
	params := map[string]string{
		"Action":             "CreateLaunchTemplate",
		"Version":            "2016-11-15",
		"LaunchTemplateName": name,
	}
	if v, _ := props["VersionDescription"].(string); v != "" {
		params["VersionDescription"] = v
	}
	putLaunchTemplateData(params, "LaunchTemplateData.", props["LaunchTemplateData"])
	putLaunchTemplateTagSpecifications(params, "", props["TagSpecifications"])

	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	if err != nil {
		return "", nil, fmt.Errorf("CreateLaunchTemplate: %w", err)
	}
	var resp ec2LaunchTemplateResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateLaunchTemplate: parse response: %w", err)
	}
	return resp.LaunchTemplateID, launchTemplateAttrs(resp), nil
}

// Update applies a data change as a new version and makes it the default.
// A LaunchTemplateName change is the one property AWS replaces on.
func (h *ec2LaunchTemplateHandler) Update(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, props map[string]any, oldProps map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	newName, _ := props["LaunchTemplateName"].(string)
	oldName, _ := oldProps["LaunchTemplateName"].(string)
	if newName != oldName {
		return "", nil, errReplacementRequired
	}

	versionParams := map[string]string{
		"Action":           "CreateLaunchTemplateVersion",
		"Version":          "2016-11-15",
		"LaunchTemplateId": physicalID,
	}
	if v, _ := props["VersionDescription"].(string); v != "" {
		versionParams["VersionDescription"] = v
	}
	putLaunchTemplateData(versionParams, "LaunchTemplateData.", props["LaunchTemplateData"])
	if _, err := internalQuery(ctx, router, rCtx.Region, versionParams); err != nil {
		return "", nil, fmt.Errorf("CreateLaunchTemplateVersion: %w", err)
	}

	// CloudFormation moves the default version to the one it just created, so
	// a consumer pinned to $Default follows the stack.
	modifyParams := map[string]string{
		"Action":            "ModifyLaunchTemplate",
		"Version":           "2016-11-15",
		"LaunchTemplateId":  physicalID,
		"SetDefaultVersion": "$Latest",
	}
	rec, err := internalQuery(ctx, router, rCtx.Region, modifyParams)
	if err != nil {
		return "", nil, fmt.Errorf("ModifyLaunchTemplate: %w", err)
	}
	var resp ec2LaunchTemplateResponse
	if err := xml.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("ModifyLaunchTemplate: parse response: %w", err)
	}
	return physicalID, launchTemplateAttrs(resp), nil
}

func (h *ec2LaunchTemplateHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	params := map[string]string{
		"Action":           "DeleteLaunchTemplate",
		"Version":          "2016-11-15",
		"LaunchTemplateId": physicalID,
	}
	rec, err := internalQuery(ctx, router, rCtx.Region, params)
	return teardownError("DeleteLaunchTemplate", rec, err)
}

// launchTemplateAttrs are the three Fn::GetAtt attributes AWS documents for
// AWS::EC2::LaunchTemplate.
func launchTemplateAttrs(resp ec2LaunchTemplateResponse) map[string]string {
	return map[string]string{
		"LaunchTemplateId":     resp.LaunchTemplateID,
		"LatestVersionNumber":  strconv.FormatInt(resp.LatestVersionNumber, 10),
		"DefaultVersionNumber": strconv.FormatInt(resp.DefaultVersionNumber, 10),
	}
}

// putLaunchTemplateData translates the CloudFormation LaunchTemplateData
// property into the Query parameters EC2 reads.
//
// The two shapes differ in more than nesting: CloudFormation names the lists
// in the plural (SecurityGroupIds, NetworkInterfaces, TagSpecifications) while
// the Query protocol flattens them to a singular indexed key. The mapping is
// written out rather than derived so it stays reviewable against the AWS
// property reference, and it covers the members Overcast's launch templates
// carry — the rest of AWS's thirty-odd are accepted by the template and
// ignored here, exactly as they are by the service.
func putLaunchTemplateData(params map[string]string, prefix string, raw any) {
	data, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for _, field := range []string{"ImageId", "InstanceType", "KeyName", "UserData"} {
		if v, ok := data[field]; ok && v != nil {
			if text := fmt.Sprint(v); text != "" {
				params[prefix+field] = text
			}
		}
	}
	putIndexedStrings(params, prefix+"SecurityGroupId", data["SecurityGroupIds"])
	putIndexedStrings(params, prefix+"SecurityGroup", data["SecurityGroups"])

	if profile, ok := data["IamInstanceProfile"].(map[string]any); ok {
		for _, field := range []string{"Arn", "Name"} {
			if v, ok := profile[field]; ok && v != nil {
				if text := fmt.Sprint(v); text != "" {
					params[prefix+"IamInstanceProfile."+field] = text
				}
			}
		}
	}

	nics, _ := data["NetworkInterfaces"].([]any)
	for i, item := range nics {
		nic, ok := item.(map[string]any)
		if !ok {
			continue
		}
		base := prefix + "NetworkInterface." + strconv.Itoa(i+1) + "."
		for _, field := range []string{"DeviceIndex", "SubnetId", "AssociatePublicIpAddress"} {
			if v, ok := nic[field]; ok && v != nil {
				params[base+field] = fmt.Sprint(v)
			}
		}
		putIndexedStrings(params, base+"SecurityGroupId", nic["Groups"])
	}

	putLaunchTemplateTagSpecifications(params, prefix, data["TagSpecifications"])
}

// putLaunchTemplateTagSpecifications writes a TagSpecifications list, which
// appears both at the top level of the resource (tagging the template) and
// inside LaunchTemplateData (tagging what a launch creates).
func putLaunchTemplateTagSpecifications(params map[string]string, prefix string, raw any) {
	specs, _ := raw.([]any)
	for i, item := range specs {
		spec, ok := item.(map[string]any)
		if !ok {
			continue
		}
		base := prefix + "TagSpecification." + strconv.Itoa(i+1) + "."
		resourceType, _ := spec["ResourceType"].(string)
		if resourceType == "" {
			continue
		}
		params[base+"ResourceType"] = resourceType
		tags, _ := spec["Tags"].([]any)
		idx := 1
		for _, tagItem := range tags {
			tag, ok := tagItem.(map[string]any)
			if !ok {
				continue
			}
			key, _ := tag["Key"].(string)
			if key == "" {
				continue
			}
			params[base+"Tag."+strconv.Itoa(idx)+".Key"] = key
			if v, ok := tag["Value"]; ok && v != nil {
				params[base+"Tag."+strconv.Itoa(idx)+".Value"] = fmt.Sprint(v)
			}
			idx++
		}
	}
}

// putIndexedStrings writes a CloudFormation list property as the Query
// protocol's flattened `<key>.N` form.
func putIndexedStrings(params map[string]string, key string, raw any) {
	items, _ := raw.([]any)
	idx := 1
	for _, item := range items {
		if item == nil {
			continue
		}
		text := fmt.Sprint(item)
		if text == "" {
			continue
		}
		params[key+"."+strconv.Itoa(idx)] = text
		idx++
	}
}
