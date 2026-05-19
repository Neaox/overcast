package cloudformation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
)

// extractAwsApplicationTag returns the value of the `awsApplication` tag
// inside a CFN resource's Properties, or "" if no such tag is present. CDK's
// `Application` L2 construct propagates this tag to every resource in the
// stack — its value is the owning application's ARN (which also equals the
// `applicationTag.awsApplication` field returned by CreateApplication). The
// provisioner uses this to auto-associate each resource with its application
// immediately after provisioning, so the web UI can resolve ownership without
// joining stack→app × stack→resources on the client.
//
// Tags come through in CFN's standard array-of-objects shape:
//
//	"Tags": [ { "Key": "awsApplication", "Value": "arn:aws:..." }, ... ]
func extractAwsApplicationTag(props map[string]any) string {
	raw, ok := props["Tags"]
	if !ok {
		return ""
	}
	arr, ok := raw.([]any)
	if !ok {
		return ""
	}
	for _, entry := range arr {
		tag, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		key, _ := tag["Key"].(string)
		if key != "awsApplication" {
			continue
		}
		val, _ := tag["Value"].(string)
		return val
	}
	return ""
}

// autoAssociateResource records an association between the given application
// (identified by name/ID/ARN — typically the ARN from the `awsApplication`
// tag) and a concrete CloudFormation-provisioned resource. Failures are
// logged but never fail the stack: the association is a UI convenience, not
// a correctness requirement. The resource key is the resource's physical ID
// which lets the frontend's reverse-map hit regardless of whether a detail
// page knows the ARN or the bare name.
func (p *provisioner) autoAssociateResource(ctx context.Context, rCtx *resolveContext, appRef, physID string) {
	// The tag value may be an application ID, name, or ARN. Chi's URL-param
	// matcher captures a single path segment, so ARNs (which contain `/` in
	// the `/applications/<id>` tail) can't be passed through directly — take
	// the last path segment, which for an AppRegistry ARN is the app ID and
	// for a bare ID/name is a no-op.
	appKey := appRef
	if i := strings.LastIndex(appKey, "/"); i != -1 {
		appKey = appKey[i+1:]
	}
	// Tag-propagated associations use AWS's RESOURCE_TAG_VALUE resource type,
	// not the CFN type — matching how real AppRegistry classifies tag-based
	// associations from the `awsApplication` tag.
	path := fmt.Sprintf("/applications/%s/resources/RESOURCE_TAG_VALUE/%s", appKey, physID)
	if _, err := internalRequest(ctx, p.router, rCtx.Region, http.MethodPut, path, "application/json", []byte(`{}`)); err != nil {
		p.log.Warn("appregistry auto-association failed",
			zap.String("application", appRef),
			zap.String("resource", physID),
			zap.Error(err))
	}
}

// ── AWS::ServiceCatalogAppRegistry::Application ────────────────────────────
//
// CDK's `Application` L2 construct synthesizes this resource. The physical ID
// is the application ID returned by the emulator; GetAtt Name/ApplicationName
// expose the friendly name, and GetAtt ApplicationTagValue/ApplicationTagKey
// surface the `awsApplication` tag that CDK propagates to every child resource.

type appregistryApplicationHandler struct{}

func (h *appregistryApplicationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	} else {
		body["name"] = fmt.Sprintf("%s-app", rCtx.StackName)
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}
	if tags, ok := props["Tags"].(map[string]any); ok {
		body["tags"] = tags
	}

	data, _ := json.Marshal(body)
	rec, err := internalRequest(ctx, router, rCtx.Region, http.MethodPost, "/applications", "application/json", data)
	if err != nil {
		return "", nil, fmt.Errorf("CreateApplication: %w", err)
	}

	var resp struct {
		Application struct {
			ID             string            `json:"id"`
			Name           string            `json:"name"`
			Arn            string            `json:"arn"`
			ApplicationTag map[string]string `json:"applicationTag"`
		} `json:"application"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return "", nil, fmt.Errorf("CreateApplication: parse response: %w", err)
	}

	attrs := map[string]string{
		"Id":                  resp.Application.ID,
		"Arn":                 resp.Application.Arn,
		"Name":                resp.Application.Name,
		"ApplicationName":     resp.Application.Name,
		"ApplicationTagKey":   "awsApplication",
		"ApplicationTagValue": resp.Application.ApplicationTag["awsApplication"],
	}
	return resp.Application.ID, attrs, nil
}

func (h *appregistryApplicationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, "/applications/"+physicalID, "", nil)
	return err
}

func (h *appregistryApplicationHandler) Update(ctx context.Context, router http.Handler, _ *config.Config, physicalID string, props map[string]any, _ map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	body := map[string]any{}
	if v, _ := props["Name"].(string); v != "" {
		body["name"] = v
	}
	if v, _ := props["Description"].(string); v != "" {
		body["description"] = v
	}

	data, _ := json.Marshal(body)
	path := "/applications/" + physicalID
	if _, err := internalRequest(ctx, router, rCtx.Region, http.MethodPatch, path, "application/json", data); err != nil {
		return "", nil, fmt.Errorf("UpdateApplication: %w", err)
	}
	return physicalID, nil, nil
}

// ── AWS::ServiceCatalogAppRegistry::ResourceAssociation ────────────────────
//
// CDK uses this to associate a CloudFormation stack with an application. The
// physical ID we return is opaque (<appID>/CFN_STACK/<stackARN>) — it is only
// used so Delete can reconstruct the association path.

type appregistryResourceAssociationHandler struct{}

func (h *appregistryResourceAssociationHandler) Create(ctx context.Context, router http.Handler, cfg *config.Config, props map[string]any, rCtx *resolveContext) (string, map[string]string, error) {
	appID, _ := props["Application"].(string)
	resource, _ := props["Resource"].(string)
	resourceType, _ := props["ResourceType"].(string)
	if resourceType == "" {
		resourceType = "CFN_STACK"
	}
	if appID == "" || resource == "" {
		return "", nil, fmt.Errorf("ResourceAssociation: Application and Resource are required")
	}

	// AppRegistry's PUT path treats {resource} as the stack identifier. For
	// CFN_STACK associations CDK passes the stack ARN — we forward it verbatim.
	path := fmt.Sprintf("/applications/%s/resources/%s/%s", appID, resourceType, resource)
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodPut, path, "application/json", []byte(`{}`))
	if err != nil {
		return "", nil, fmt.Errorf("AssociateResource: %w", err)
	}

	physID := fmt.Sprintf("%s/%s/%s", appID, resourceType, resource)
	return physID, nil, nil
}

func (h *appregistryResourceAssociationHandler) Delete(ctx context.Context, router http.Handler, cfg *config.Config, physicalID string, rCtx *resolveContext) error {
	// physicalID is "<appID>/<resourceType>/<resource>" — split on the first
	// two '/' so the resource ARN (which may contain more slashes) stays intact.
	first := -1
	second := -1
	for i, c := range physicalID {
		if c != '/' {
			continue
		}
		if first == -1 {
			first = i
		} else if second == -1 {
			second = i
			break
		}
	}
	if first == -1 || second == -1 {
		return fmt.Errorf("DisassociateResource: malformed physical ID %q", physicalID)
	}
	path := "/applications/" + physicalID[:first] + "/resources/" + physicalID[first+1:second] + "/" + physicalID[second+1:]
	_, err := internalRequest(ctx, router, rCtx.Region, http.MethodDelete, path, "", nil)
	return err
}
