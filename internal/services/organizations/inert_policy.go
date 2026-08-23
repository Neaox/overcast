package organizations

// inert_policy.go hand-wires the Organizations *policy* resource against
// internal/inert, ahead of cmd/awsmodelgen learning to emit exactly this
// (docs/plans/inert-tier-rollout.md phase I2 → I3).
//
// It is written the way the generator will write it, deliberately: typed
// request/response structs mirroring the modeled shapes, one record type that
// is the create input unioned with everything the update can set plus the
// derivations, error values selected from the operation's own declared
// `errors: [...]` list, and no behaviour that is not either in the model or
// in §3 of the plan. Where a value cannot come from the model it is called
// out in a comment, because those are the lines that become seasoning.go
// (§4.4) rather than generated code.
//
// Every shape here was read out of the pruned Smithy snapshot Phase I1
// committed for this service (see docs/plans/inert-tier-rollout.md §4.6), not
// guessed. Member names, required-ness, the PolicyId and PolicyArn patterns,
// the MaxResults range and the per-operation error lists all come from there.
// The snapshot is build-time input only and is deliberately not named by path
// here: internal/awsapi's TestShapeSnapshot_isGeneratorInputOnly scans runtime
// packages for exactly that string, because a runtime reference would make
// model files a startup input.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/inert"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// Namespaces. Organizations is a global service, so its keys carry no region
// (§3.5) — inert.Config leaves Region nil for exactly this reason.
const (
	nsPolicies = "organizations:policies"
	nsTags     = "organizations:tags"
)

// organizationID is the emulator's single organization, shared with
// DescribeOrganization's hand-written response so a policy ARN names the org
// the caller was told it is in.
const organizationID = "o-overcast"

// ---- modeled errors (§3.3) -------------------------------------------------
//
// Selected from each operation's declared errors, with the HTTP status from
// the shape's @httpError trait — never invented. DeletePolicy also declares
// PolicyInUseException, which needs policy *attachments* to be reachable;
// attachments are a Tier 0 verb surface here (AttachPolicy stays 501), so no
// state can put a policy in use and the error is unreachable by construction.

var (
	// PolicyNotFoundException, @httpError 404.
	errPolicyNotFound = &protocol.AWSError{
		Code:       "PolicyNotFoundException",
		Message:    "We can't find a policy with the PolicyId that you specified.",
		HTTPStatus: http.StatusNotFound,
	}
	// DuplicatePolicyException, @httpError 409.
	errDuplicatePolicy = &protocol.AWSError{
		Code:       "DuplicatePolicyException",
		Message:    "A policy with the specified name already exists.",
		HTTPStatus: http.StatusConflict,
	}
	// InvalidInputException, @httpError 400. Organizations declares no
	// InvalidNextToken-shaped error, so §3.3's invalid-token selection falls
	// back to invalid-parameter — which is what the real service returns for
	// a malformed NextToken too.
	errInvalidInput = &protocol.AWSError{
		Code:       "InvalidInputException",
		Message:    "You provided invalid values for one or more of the request parameters.",
		HTTPStatus: http.StatusBadRequest,
	}
	// TargetNotFoundException, @httpError 404 — the tag operations'
	// not-found, since their input names a target rather than a policy.
	errTargetNotFound = &protocol.AWSError{
		Code:       "TargetNotFoundException",
		Message:    "We can't find a root, OU, account, or policy with the ResourceId that you specified.",
		HTTPStatus: http.StatusNotFound,
	}
)

// policyTagRules are the tag constraints Organizations models. The codes are
// the ones its tag operations declare.
var policyTagRules = serviceutil.TagValidationConfig{
	ExceededCode:    "ConstraintViolationException",
	ExceededMessage: "You have exceeded the number of tags allowed on this resource.",
	InvalidCode:     "InvalidInputException",
}

// policyTypes is the PolicyType enum, verbatim from the snapshot. Membership
// is one of the four validations §3.4 permits (enum membership); nothing
// cross-field or referential is checked.
var policyTypes = map[string]bool{
	"AISERVICES_OPT_OUT_POLICY":        true,
	"BACKUP_POLICY":                    true,
	"BEDROCK_POLICY":                   true,
	"CHATBOT_POLICY":                   true,
	"DECLARATIVE_POLICY_EC2":           true,
	"INSPECTOR_POLICY":                 true,
	"NETWORK_SECURITY_DIRECTOR_POLICY": true,
	"RESOURCE_CONTROL_POLICY":          true,
	"S3_POLICY":                        true,
	"SECURITYHUB_POLICY":               true,
	"SERVICE_CONTROL_POLICY":           true,
	"TAG_POLICY":                       true,
	"UPGRADE_ROLLOUT_POLICY":           true,
}

// ---- the persisted record (§3.2) -------------------------------------------

// policyRecord is the create input unioned with every field UpdatePolicy can
// set, plus the derivations. Responses are a projection of this onto the
// modeled output shapes, field by field.
//
// CreatedAt and UpdatedAt are stored but never projected: Organizations
// models no timestamp member on Policy or PolicySummary, and §3.2 is explicit
// that a field the output shape does not carry is stored and not returned
// rather than bolted onto the response. They still come from the injected
// clock, never time.Now() (§3.5) — TestPolicyTimestampsComeFromTheClock
// is what holds that, since the conformance suite's §3.5/timestamps clause
// has no modeled member to look at here.
type policyRecord struct {
	Id          string    `json:"Id"`
	Arn         string    `json:"Arn"`
	Name        string    `json:"Name"`
	Description string    `json:"Description"`
	Content     string    `json:"Content"`
	Type        string    `json:"Type"`
	AwsManaged  bool      `json:"AwsManaged"`
	CreatedAt   time.Time `json:"CreatedAt"`
	UpdatedAt   time.Time `json:"UpdatedAt"`
}

// ---- modeled wire shapes ---------------------------------------------------

type policyTag struct {
	Key   string `json:"Key" cbor:"Key"`
	Value string `json:"Value" cbor:"Value"`
}

// policySummary mirrors the PolicySummary shape. AwsManaged carries
// @default false, so it is emitted rather than omitted, per §3.2: the model's
// default, or omission when the member is optional, and never a fabrication.
type policySummary struct {
	Arn         string `json:"Arn" cbor:"Arn"`
	AwsManaged  bool   `json:"AwsManaged" cbor:"AwsManaged"`
	Description string `json:"Description" cbor:"Description"`
	Id          string `json:"Id" cbor:"Id"`
	Name        string `json:"Name" cbor:"Name"`
	Type        string `json:"Type" cbor:"Type"`
}

type policyView struct {
	Content       string        `json:"Content" cbor:"Content"`
	PolicySummary policySummary `json:"PolicySummary" cbor:"PolicySummary"`
}

// createPolicyRequest mirrors CreatePolicyRequest. Description is a pointer
// because the model makes it @required with @length min 0 — present-and-empty
// is legal input and absent is not, and only a pointer tells those apart.
type createPolicyRequest struct {
	Content     string      `json:"Content" cbor:"Content"`
	Description *string     `json:"Description" cbor:"Description"`
	Name        string      `json:"Name" cbor:"Name"`
	Tags        []policyTag `json:"Tags" cbor:"Tags"`
	Type        string      `json:"Type" cbor:"Type"`
}

type createPolicyResponse struct {
	Policy policyView `json:"Policy" cbor:"Policy"`
}

type describePolicyRequest struct {
	PolicyId string `json:"PolicyId" cbor:"PolicyId"`
}

type describePolicyResponse struct {
	Policy policyView `json:"Policy" cbor:"Policy"`
}

// updatePolicyRequest mirrors UpdatePolicyRequest: every member but PolicyId
// is optional, and a nil pointer means "not sent", which is what makes §3.1's
// merge a merge rather than a replace.
type updatePolicyRequest struct {
	Content     *string `json:"Content" cbor:"Content"`
	Description *string `json:"Description" cbor:"Description"`
	Name        *string `json:"Name" cbor:"Name"`
	PolicyId    string  `json:"PolicyId" cbor:"PolicyId"`
}

type updatePolicyResponse struct {
	Policy policyView `json:"Policy" cbor:"Policy"`
}

type deletePolicyRequest struct {
	PolicyId string `json:"PolicyId" cbor:"PolicyId"`
}

// deletePolicyResponse is DeletePolicy's modeled smithy.api#Unit output: an
// empty body, not an echo of what was deleted.
type deletePolicyResponse struct{}

type listPoliciesRequest struct {
	Filter     string  `json:"Filter" cbor:"Filter"`
	MaxResults *int32  `json:"MaxResults" cbor:"MaxResults"`
	NextToken  *string `json:"NextToken" cbor:"NextToken"`
}

type listPoliciesResponse struct {
	NextToken string          `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
	Policies  []policySummary `json:"Policies" cbor:"Policies"`
}

type tagResourceRequest struct {
	ResourceId string      `json:"ResourceId" cbor:"ResourceId"`
	Tags       []policyTag `json:"Tags" cbor:"Tags"`
}

type tagResourceResponse struct{}

type untagResourceRequest struct {
	ResourceId string   `json:"ResourceId" cbor:"ResourceId"`
	TagKeys    []string `json:"TagKeys" cbor:"TagKeys"`
}

type untagResourceResponse struct{}

type listTagsForResourceRequest struct {
	NextToken  *string `json:"NextToken" cbor:"NextToken"`
	ResourceId string  `json:"ResourceId" cbor:"ResourceId"`
}

type listTagsForResourceResponse struct {
	NextToken string      `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
	Tags      []policyTag `json:"Tags" cbor:"Tags"`
}

// ---- derivations (§3.5) ----------------------------------------------------

// policyID derives a policy's identifier from its name.
//
// AWS mints an opaque `p-…`; the model only constrains the shape
// (`^p-[0-9a-zA-Z_]{8,128}$`, narrowed to `p-[0-9a-z]{10,32}` by the
// PolicyArn pattern). Deriving it from the name rather than from a counter or
// a UUID buys two things a random id would not: the identifier is stable
// across restarts and across a state export/import, and duplicate-name
// detection — DuplicatePolicyException is declared on Name, not on Id — needs
// no second index to maintain and keep consistent.
//
// The one divergence this costs, recorded rather than hidden: renaming a
// policy through UpdatePolicy leaves its identifier derived from the *old*
// name, so the old name stays taken and the new one is not. AWS identifiers
// are opaque, so nothing observable depends on the identifier tracking the
// name; what it means is that a rename-then-recreate of the original name
// returns DuplicatePolicyException where real AWS would allow it. Fixing that
// properly is a name→id index, which is generator work for I3 rather than a
// hand-written second store here.
func policyID(name string) string {
	sum := sha256.Sum256([]byte(name))
	return "p-" + hex.EncodeToString(sum[:8])
}

// policyARN renders the policy ARN template. The `{resource-path}` segment —
// `policy/{org}/{type}/{id}` — is the field §3.5 expects to be hand-corrected
// per resource; it is verbatim from the PolicyArn pattern in the snapshot,
// where the type segment is lowercase.
func (s *Service) policyARN(rec *policyRecord) string {
	return fmt.Sprintf("arn:aws:organizations::%s:policy/%s/%s/%s",
		s.accountID(), organizationID, strings.ToLower(rec.Type), rec.Id)
}

func (rec *policyRecord) summary() policySummary {
	return policySummary{
		Arn:         rec.Arn,
		AwsManaged:  rec.AwsManaged,
		Description: rec.Description,
		Id:          rec.Id,
		Name:        rec.Name,
		Type:        rec.Type,
	}
}

func (rec *policyRecord) view() policyView {
	return policyView{Content: rec.Content, PolicySummary: rec.summary()}
}

// ---- handlers --------------------------------------------------------------

func (s *Service) createPolicy(ctx context.Context, in *createPolicyRequest) (*createPolicyResponse, *protocol.AWSError) {
	// §3.4: exactly the checks the model states — @required presence,
	// @length, enum membership. Nothing cross-field, nothing referential.
	if in.Name == "" {
		return nil, invalidInput("Name is a required parameter.")
	}
	if in.Content == "" {
		return nil, invalidInput("Content is a required parameter.")
	}
	if in.Description == nil {
		return nil, invalidInput("Description is a required parameter.")
	}
	if !policyTypes[in.Type] {
		return nil, invalidInput("Type must be one of the modeled policy types.")
	}

	id := policyID(in.Name)
	if _, found, err := s.policies.Get(ctx, id); err != nil {
		return nil, inert.StorageError(err)
	} else if found {
		return nil, errDuplicatePolicy
	}

	now := s.policies.Now()
	rec := &policyRecord{
		Id:          id,
		Name:        in.Name,
		Description: *in.Description,
		Content:     in.Content,
		Type:        in.Type,
		AwsManaged:  false,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	rec.Arn = s.policyARN(rec)

	// Tags on the create input write through to the same store the tag
	// operations read, so ListTagsForResource can never disagree with what
	// CreatePolicy accepted (§3.1's Tag class, §7.3).
	if len(in.Tags) > 0 {
		if _, aerr := s.tags.Apply(ctx, rec.Arn, tagMap(in.Tags), policyTagRules); aerr != nil {
			return nil, aerr
		}
	}
	if err := s.policies.Put(ctx, id, rec); err != nil {
		return nil, inert.StorageError(err)
	}
	return &createPolicyResponse{Policy: rec.view()}, nil
}

func (s *Service) describePolicy(ctx context.Context, in *describePolicyRequest) (*describePolicyResponse, *protocol.AWSError) {
	rec, aerr := s.loadPolicy(ctx, in.PolicyId)
	if aerr != nil {
		return nil, aerr
	}
	return &describePolicyResponse{Policy: rec.view()}, nil
}

func (s *Service) updatePolicy(ctx context.Context, in *updatePolicyRequest) (*updatePolicyResponse, *protocol.AWSError) {
	rec, aerr := s.loadPolicy(ctx, in.PolicyId)
	if aerr != nil {
		return nil, aerr
	}
	// A merge, not a replace: only members the caller actually sent move.
	if in.Name != nil {
		if *in.Name == "" {
			return nil, invalidInput("Name must not be empty.")
		}
		rec.Name = *in.Name
	}
	if in.Description != nil {
		rec.Description = *in.Description
	}
	if in.Content != nil {
		if *in.Content == "" {
			return nil, invalidInput("Content must not be empty.")
		}
		rec.Content = *in.Content
	}
	rec.UpdatedAt = s.policies.Now()

	// The ARN is derived from the immutable Type and Id, so a rename does not
	// move it — matching AWS, where a policy's ARN is stable for its life.
	if err := s.policies.Put(ctx, rec.Id, rec); err != nil {
		return nil, inert.StorageError(err)
	}
	return &updatePolicyResponse{Policy: rec.view()}, nil
}

func (s *Service) deletePolicy(ctx context.Context, in *deletePolicyRequest) (*deletePolicyResponse, *protocol.AWSError) {
	rec, aerr := s.loadPolicy(ctx, in.PolicyId)
	if aerr != nil {
		return nil, aerr
	}
	// Tags go with the record. Namespaced tags have nothing tying them to the
	// record's lifetime, so skipping this leaves ListTagsForResource
	// answering for a policy that is gone.
	if aerr := s.tags.Delete(ctx, rec.Arn); aerr != nil {
		return nil, aerr
	}
	if err := s.policies.Delete(ctx, rec.Id); err != nil {
		return nil, inert.StorageError(err)
	}
	return &deletePolicyResponse{}, nil
}

func (s *Service) listPolicies(ctx context.Context, in *listPoliciesRequest) (*listPoliciesResponse, *protocol.AWSError) {
	if !policyTypes[in.Filter] {
		return nil, invalidInput("Filter is a required parameter and must be one of the modeled policy types.")
	}

	all, err := s.policies.List(ctx)
	if err != nil {
		return nil, inert.StorageError(err)
	}
	matching := make([]*policyRecord, 0, len(all))
	for _, rec := range all {
		if rec.Type == in.Filter {
			matching = append(matching, rec)
		}
	}

	// MaxResults is @range min 1 max 20 in the model, which is where both
	// numbers come from — not from a house default.
	page, err := serviceutil.Paginate(matching, int(deref(in.MaxResults)), derefString(in.NextToken),
		serviceutil.PaginateOptions{DefaultLimit: 20, MaxLimit: 20})
	if err != nil {
		return nil, inert.PageError(err, errInvalidInput)
	}

	out := &listPoliciesResponse{NextToken: page.NextToken, Policies: make([]policySummary, 0, len(page.Items))}
	for _, rec := range page.Items {
		out.Policies = append(out.Policies, rec.summary())
	}
	return out, nil
}

func (s *Service) tagResource(ctx context.Context, in *tagResourceRequest) (*tagResourceResponse, *protocol.AWSError) {
	arn, aerr := s.taggableARN(ctx, in.ResourceId)
	if aerr != nil {
		return nil, aerr
	}
	if len(in.Tags) == 0 {
		return nil, invalidInput("Tags is a required parameter.")
	}
	if _, aerr := s.tags.Apply(ctx, arn, tagMap(in.Tags), policyTagRules); aerr != nil {
		return nil, aerr
	}
	return &tagResourceResponse{}, nil
}

func (s *Service) untagResource(ctx context.Context, in *untagResourceRequest) (*untagResourceResponse, *protocol.AWSError) {
	arn, aerr := s.taggableARN(ctx, in.ResourceId)
	if aerr != nil {
		return nil, aerr
	}
	if in.TagKeys == nil {
		return nil, invalidInput("TagKeys is a required parameter.")
	}
	if _, aerr := s.tags.Remove(ctx, arn, in.TagKeys); aerr != nil {
		return nil, aerr
	}
	return &untagResourceResponse{}, nil
}

func (s *Service) listTagsForResource(ctx context.Context, in *listTagsForResourceRequest) (*listTagsForResourceResponse, *protocol.AWSError) {
	arn, aerr := s.taggableARN(ctx, in.ResourceId)
	if aerr != nil {
		return nil, aerr
	}
	tags, aerr := s.tags.Load(ctx, arn)
	if aerr != nil {
		return nil, aerr
	}
	items := serviceutil.TagElements(tags, func(k, v string) policyTag {
		return policyTag{Key: k, Value: v}
	})
	// ListTagsForResource is @paginated with an inputToken/outputToken but no
	// pageSize member, so there is no caller-supplied limit to honour — only
	// a token to keep honest.
	page, err := serviceutil.Paginate(items, 0, derefString(in.NextToken), serviceutil.PaginateOptions{DefaultLimit: 50})
	if err != nil {
		return nil, inert.PageError(err, errInvalidInput)
	}
	return &listTagsForResourceResponse{NextToken: page.NextToken, Tags: page.Items}, nil
}

// ---- shared helpers --------------------------------------------------------

// loadPolicy is the read-or-modeled-not-found every policy operation starts
// with. A malformed persisted record reads as absent (inert.Store isolates
// it), so it surfaces as PolicyNotFoundException rather than a 500.
func (s *Service) loadPolicy(ctx context.Context, id string) (*policyRecord, *protocol.AWSError) {
	if id == "" {
		return nil, invalidInput("PolicyId is a required parameter.")
	}
	rec, found, err := s.policies.Get(ctx, id)
	if err != nil {
		return nil, inert.StorageError(err)
	}
	if !found {
		return nil, errPolicyNotFound
	}
	return rec, nil
}

// taggableARN resolves a TaggableResourceId to the ARN the shared tag store
// is keyed by.
//
// Organizations lets a caller tag a root, an OU, an account, a policy or a
// resource policy. Only policies are Tier 1 here — roots, OUs and accounts
// are not stored at all, and their operations are still Tier 0 — so any other
// id is genuinely unknown to this emulator and gets the operation's own
// modeled TargetNotFoundException rather than a fabricated success. Tagging
// something that does not exist and being told it worked is the §3.6 failure
// mode in a different costume.
func (s *Service) taggableARN(ctx context.Context, resourceID string) (string, *protocol.AWSError) {
	if resourceID == "" {
		return "", invalidInput("ResourceId is a required parameter.")
	}
	if !strings.HasPrefix(resourceID, "p-") {
		return "", errTargetNotFound
	}
	rec, found, err := s.policies.Get(ctx, resourceID)
	if err != nil {
		return "", inert.StorageError(err)
	}
	if !found {
		return "", errTargetNotFound
	}
	return rec.Arn, nil
}

func invalidInput(message string) *protocol.AWSError {
	return &protocol.AWSError{Code: errInvalidInput.Code, Message: message, HTTPStatus: errInvalidInput.HTTPStatus}
}

func tagMap(tags []policyTag) map[string]string {
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		out[t.Key] = t.Value
	}
	return out
}

func deref(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}
