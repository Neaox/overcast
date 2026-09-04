package iam

// UpdateRole and CreatePolicyVersion (issue #521): the in-place mutations
// CloudFormation's AWS::IAM::Role and AWS::IAM::ManagedPolicy updates dispatch.
// Both exist only as typed operations; the legacy Query table reaches them
// through typedHandler.

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/overcast-sh/overcast/internal/protocol"
)

const (
	defaultMaxSessionDuration = 3600
	maxMaxSessionDuration     = 43200
)

func checkMaxSessionDuration(duration int) *protocol.AWSError {
	if duration < defaultMaxSessionDuration || duration > maxMaxSessionDuration {
		return errInvalidMaxSessionDuration(strconv.Itoa(duration))
	}
	return nil
}

func errInvalidMaxSessionDuration(value string) *protocol.AWSError {
	return &protocol.AWSError{
		Code: "ValidationError",
		Message: fmt.Sprintf("1 validation error detected: Value '%s' at 'maxSessionDuration' failed to satisfy constraint: "+
			"Member must have value less than or equal to %d and greater than or equal to %d", value, maxMaxSessionDuration, defaultMaxSessionDuration),
		HTTPStatus: http.StatusBadRequest,
	}
}

// policyDefaultVersionID renders a policy's operative version as AWS's vN id.
func policyDefaultVersionID(p *Policy) string {
	version := p.DefaultVersion
	if version == 0 {
		version = 1
	}
	return "v" + strconv.Itoa(version)
}

// ─── UpdateRole ─────────────────────────────────────────────────────────────

type updateRoleReq struct {
	RoleName string `json:"RoleName"`
	// Description distinguishes clear (present and empty) from omit (absent):
	// AWS's UpdateRole leaves an omitted description alone and clears an empty
	// one, which is what lets CloudFormation reset a removed template property.
	Description        *string `json:"Description" queryEmpty:"set"`
	MaxSessionDuration int     `json:"MaxSessionDuration"`
}

type updateRoleResp struct {
	XMLName struct{} `xml:"UpdateRoleResponse"`
	Xmlns   string   `xml:"xmlns,attr"`
	Result  struct{} `xml:"UpdateRoleResult"`
	Meta    respMeta `xml:"ResponseMetadata"`
}

func (h *Handler) updateRoleTyped(ctx context.Context, req *updateRoleReq) (*updateRoleResp, *protocol.AWSError) {
	if strings.TrimSpace(req.RoleName) == "" {
		return nil, protocol.ErrMissingParameter("RoleName")
	}
	role, aerr := h.store.getRole(ctx, req.RoleName)
	if aerr != nil {
		return nil, aerr
	}
	if req.MaxSessionDuration != 0 {
		if aerr := checkMaxSessionDuration(req.MaxSessionDuration); aerr != nil {
			return nil, aerr
		}
		role.MaxSessionDuration = req.MaxSessionDuration
	}
	if req.Description != nil {
		role.Description = *req.Description
	}
	if aerr := h.store.putRole(ctx, role); aerr != nil {
		return nil, aerr
	}
	return &updateRoleResp{Xmlns: iamXMLNS, Meta: metaFromCtx(ctx)}, nil
}

// ─── CreatePolicyVersion ────────────────────────────────────────────────────

type createPolicyVersionReq struct {
	PolicyArn      string `json:"PolicyArn"`
	PolicyDocument string `json:"PolicyDocument"`
	SetAsDefault   bool   `json:"SetAsDefault"`
}

type createPolicyVersionResp struct {
	XMLName struct{}                  `xml:"CreatePolicyVersionResponse"`
	Xmlns   string                    `xml:"xmlns,attr"`
	Result  createPolicyVersionResult `xml:"CreatePolicyVersionResult"`
	Meta    respMeta                  `xml:"ResponseMetadata"`
}

type createPolicyVersionResult struct {
	PolicyVersion policyVersionXML `xml:"PolicyVersion"`
}

type policyVersionXML struct {
	VersionId        string `xml:"VersionId"`
	IsDefaultVersion bool   `xml:"IsDefaultVersion"`
	CreateDate       string `xml:"CreateDate"`
}

// createPolicyVersionTyped mints a new policy version. Partial on purpose:
// only the operative document is stored, so a version created with
// SetAsDefault=false gets an id but its content is not retained, and there is
// no GetPolicyVersion/ListPolicyVersions to read versions back.
func (h *Handler) createPolicyVersionTyped(ctx context.Context, req *createPolicyVersionReq) (*createPolicyVersionResp, *protocol.AWSError) {
	if strings.TrimSpace(req.PolicyArn) == "" {
		return nil, protocol.ErrMissingParameter("PolicyArn")
	}
	if strings.TrimSpace(req.PolicyDocument) == "" {
		return nil, protocol.ErrMissingParameter("PolicyDocument")
	}
	p, aerr := h.store.getPolicy(ctx, req.PolicyArn)
	if aerr != nil {
		return nil, aerr
	}
	latest := p.LatestVersion
	if latest == 0 {
		latest = 1
	}
	if p.DefaultVersion > latest {
		latest = p.DefaultVersion
	}
	version := latest + 1
	p.LatestVersion = version
	if req.SetAsDefault {
		p.Document = req.PolicyDocument
		p.DefaultVersion = version
	}
	if aerr := h.store.putPolicy(ctx, p); aerr != nil {
		return nil, aerr
	}
	return &createPolicyVersionResp{Xmlns: iamXMLNS, Result: createPolicyVersionResult{
		PolicyVersion: policyVersionXML{
			VersionId:        "v" + strconv.Itoa(version),
			IsDefaultVersion: req.SetAsDefault,
			CreateDate:       h.clk.Now().UTC().Format("2006-01-02T15:04:05Z"),
		},
	}, Meta: metaFromCtx(ctx)}, nil
}
