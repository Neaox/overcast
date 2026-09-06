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
	"unicode/utf8"

	"github.com/overcast-sh/overcast/internal/inert"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// Namespaces. Organizations is a global service, so its keys carry no region
// (§3.5) — inert.Config leaves Region nil for exactly this reason.
const (
	nsPolicies = "organizations:policies"
	nsTags     = "organizations:tags"
)

// maxPolicyName is PolicyName's modeled @length max. The minimum, 1, is the
// presence check in createPolicy/updatePolicy.
const maxPolicyName = 128

// maxPolicyDescription is PolicyDescription's modeled @length max. Its
// minimum is 0 — an empty description is legal, unlike PolicyName and
// PolicyContent — so there is no accompanying minimum check.
const maxPolicyDescription = 512

// organizationID derives the emulator's single organization identifier from
// the account ID, shared with DescribeOrganization's hand-written response so
// a policy ARN names the org the caller was told it is in.
//
// AWS mints an opaque ten-character organization ID; the modeled
// OrganizationId shape's pattern (`^o-[a-z0-9]{10,32}$`) is the lower
// bound that #1736 found the old `o-overcast` constant violating — eight
// characters is not a valid AWS organization ID, and every Organizations ARN
// this service mints is rooted at this value, so real AWS SDK/CLI clients
// that validate ARN shape (or the #1113 G2 pilot's own DescribeOrganization
// assertion) reject it outright.
//
// It must also be stable for the lifetime of the store rather than
// regenerated per process — it is the root of every ARN, so a value that
// moved on restart would move every ARN derived from it too. This service
// does not otherwise persist organization metadata, so rather than add a
// dedicated record for a single fixed value, the ID is derived
// deterministically from the account ID: a SHA-256 digest of the account ID,
// hex-encoded (lowercase, alphanumeric — exactly what the pattern requires)
// and truncated to ten characters. Two services constructed against the same
// account ID — including the same service restarted — always agree on the
// same organization ID without persisting anything extra.
func organizationID(accountID string) string {
	sum := sha256.Sum256([]byte(accountID))
	return "o-" + hex.EncodeToString(sum[:5])
}

// organizationID resolves the service's own organization identifier from its
// configured account ID (see the package-level organizationID above).
func (s *Service) organizationID() string {
	return organizationID(s.accountID())
}

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
	// errInvalidNextToken is InvalidInputException with Reason set to the
	// wire value of the modeled INVALID_PAGINATION_TOKEN enum member
	// (INVALID_NEXT_TOKEN — the enum's Go-side member name and its
	// @enumValue differ, see the reason constants below). It is the
	// invalid-token template every paginated operation in this service
	// passes to inert.PageError, so a garbage continuation token is
	// distinguished from any other bad input by Reason alone, the same way
	// AWS distinguishes it.
	errInvalidNextToken = &protocol.AWSError{
		Code:       "InvalidInputException",
		Message:    "You provided an invalid value for NextToken.",
		HTTPStatus: http.StatusBadRequest,
		Reason:     reasonInvalidNextToken,
	}
	// TargetNotFoundException, @httpError 404 — the tag operations'
	// not-found, since their input names a target rather than a policy.
	errTargetNotFound = &protocol.AWSError{
		Code:       "TargetNotFoundException",
		Message:    "We can't find a root, OU, account, or policy with the ResourceId that you specified.",
		HTTPStatus: http.StatusNotFound,
	}
)

// ---- InvalidInputException.Reason values (§3.3/InvalidInputExceptionReason) -
//
// Read out of the modeled InvalidInputExceptionReason enum, verbatim — the
// enum member's *wire value* (its `smithy.api#enumValue` trait), which for
// most members equals the member name but not all: the model spells the
// missing-NextToken member INVALID_PAGINATION_TOKEN while its wire value is
// INVALID_NEXT_TOKEN, which is what actually goes on the wire and is called
// out below. (The pruned shape snapshot itself is generator input only and
// deliberately not named by path in runtime code — see the file header.)
//
// Only the reasons an existing invalid-input path in this service can
// actually produce are declared here. Two enum members reachable in
// principle are deliberately absent:
//   - INVALID_PATTERN never fires because every string member this service
//     validates a pattern for is unconstrained: PolicyName, PolicyContent,
//     PolicyDescription and OrganizationalUnitName all carry the modeled
//     pattern `^[\s\S]*$`, which accepts anything. The one place a pattern
//     really does constrain input — PolicyId/OrganizationalUnitId/ParentId's
//     `p-…`/`ou-…`/`r-…` shapes — is deliberately not pattern-checked; see
//     the "Differences from AWS" row in docs/services/organizations.md.
//   - MIN_LENGTH_EXCEEDED does not apply to a create path: CreatePolicy's
//     Name/Content and CreateOrganizationalUnit's Name are plain (non-pointer)
//     strings, so an empty value there is indistinguishable from an absent
//     one and reports INPUT_REQUIRED instead. It does apply to Update, whose
//     equivalent members are pointers — see reasonMinLengthExceeded's use in
//     updatePolicy and validateOrganizationalUnitName.
const (
	// reasonInputRequired is INPUT_REQUIRED — a required member that is
	// missing (or, for a plain-string required member, indistinguishable
	// from missing because it decoded to "").
	reasonInputRequired = "INPUT_REQUIRED"
	// reasonInvalidEnumPolicyType is INVALID_ENUM_POLICY_TYPE — CreatePolicy's
	// Type and ListPolicies' Filter both target the PolicyType enum, and a
	// value outside policyTypes fails the same way for both.
	reasonInvalidEnumPolicyType = "INVALID_ENUM_POLICY_TYPE"
	// reasonInvalidNextToken is INVALID_NEXT_TOKEN, the wire value of the
	// INVALID_PAGINATION_TOKEN enum member (the enum's Go-side name differs
	// from what actually goes on the wire — see the const block's header).
	reasonInvalidNextToken = "INVALID_NEXT_TOKEN"
	// reasonMaxLengthExceeded is MAX_LENGTH_EXCEEDED — OrganizationalUnitName's
	// modeled @length max (128).
	reasonMaxLengthExceeded = "MAX_LENGTH_EXCEEDED"
	// reasonMinLengthExceeded is MIN_LENGTH_EXCEEDED — a pointer member that
	// was sent but decoded to a value shorter than its modeled @length min.
	reasonMinLengthExceeded = "MIN_LENGTH_EXCEEDED"
	// reasonMaxValueExceeded and reasonMinValueExceeded are MaxResults'
	// modeled @range violations (min 1, max 20) — see checkMaxResults.
	reasonMaxValueExceeded = "MAX_VALUE_EXCEEDED"
	reasonMinValueExceeded = "MIN_VALUE_EXCEEDED"
)

// checkMaxResults enforces MaxResults' modeled @range (min 1, max 20 — the
// one shape every paginated operation in this service binds its MaxResults
// member to). AWS answers InvalidInputException for a value outside that
// range instead of silently clamping to the bound, so this must run before
// serviceutil.Paginate, whose own MaxLimit clamp exists for services whose
// model declares no explicit range and is the wrong behaviour here.
//
// A nil MaxResults (the member omitted) is not a violation — Paginate's
// DefaultLimit applies, as it always has.
func checkMaxResults(v *int32) *protocol.AWSError {
	if v == nil {
		return nil
	}
	switch {
	case *v > listMaxResults:
		return invalidInput(reasonMaxValueExceeded,
			fmt.Sprintf("You specified a value for MaxResults that is greater than the maximum allowed value of %d.", listMaxResults))
	case *v < 1:
		return invalidInput(reasonMinValueExceeded,
			"You specified a value for MaxResults that is less than the minimum allowed value of 1.")
	}
	return nil
}

// tagRules are the tag constraints Organizations models. The codes are
// the ones its tag operations declare. They are per-service rather than
// per-resource: one tag store, one set of rules, whatever is being tagged.
var tagRules = serviceutil.TagValidationConfig{
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

// resourceTag mirrors the Tag shape, which Organizations shares across every
// taggable resource — a policy's Tags member, an organizational unit's, and
// the generic TagResource/ListTagsForResource pair all carry this one shape.
type resourceTag struct {
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
	Content     string        `json:"Content" cbor:"Content"`
	Description *string       `json:"Description" cbor:"Description"`
	Name        string        `json:"Name" cbor:"Name"`
	Tags        []resourceTag `json:"Tags" cbor:"Tags"`
	Type        string        `json:"Type" cbor:"Type"`
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
	ResourceId string        `json:"ResourceId" cbor:"ResourceId"`
	Tags       []resourceTag `json:"Tags" cbor:"Tags"`
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
	NextToken string        `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
	Tags      []resourceTag `json:"Tags" cbor:"Tags"`
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
		s.accountID(), s.organizationID(), strings.ToLower(rec.Type), rec.Id)
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
	// @length maxima, and enum membership. Nothing cross-field, nothing
	// referential.
	//
	// PolicyContent gets no max-length check: its modeled @length is min 1
	// only (no max member in the trait), and the presence check below
	// already covers the minimum. PolicyName (max 128) and PolicyDescription
	// (max 512) are the two maxima the model does declare, checked with
	// validateMaxLength. Name and Content are plain, non-pointer strings
	// here, so an empty value is indistinguishable from absent and reports
	// INPUT_REQUIRED, same as validateOrganizationalUnitName's create path;
	// Description carries a modeled minimum of 0, so an explicitly empty
	// description is legal and only its maximum is checked.
	if in.Name == "" {
		return nil, invalidInput(reasonInputRequired, "Name is a required parameter.")
	}
	if aerr := validateMaxLength("Name", in.Name, maxPolicyName); aerr != nil {
		return nil, aerr
	}
	if in.Content == "" {
		return nil, invalidInput(reasonInputRequired, "Content is a required parameter.")
	}
	if in.Description == nil {
		return nil, invalidInput(reasonInputRequired, "Description is a required parameter.")
	}
	if aerr := validateMaxLength("Description", *in.Description, maxPolicyDescription); aerr != nil {
		return nil, aerr
	}
	if !policyTypes[in.Type] {
		return nil, invalidInput(reasonInvalidEnumPolicyType, "Type must be one of the modeled policy types.")
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

	// The record is persisted before its tags are applied: a failed Put must
	// never leave tags written against an ARN with no backing record (an
	// orphan ListTagsForResource could answer for).
	if err := s.policies.Put(ctx, id, rec); err != nil {
		return nil, inert.StorageError(err)
	}
	// Tags on the create input write through to the same store the tag
	// operations read, so ListTagsForResource can never disagree with what
	// CreatePolicy accepted (§3.1's Tag class, §7.3).
	if len(in.Tags) > 0 {
		if _, aerr := s.tags.Apply(ctx, rec.Arn, tagMap(in.Tags), tagRules); aerr != nil {
			return nil, aerr
		}
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
	// changed tracks whether anything on the record actually differs from
	// what is stored, so a caller that omits every optional member (or
	// resends the current values) is a true no-op: no UpdatedAt bump and no
	// store write.
	//
	// Content and Name are pointers here, so a non-nil, empty-string value
	// is a value the caller actually sent — PolicyContent and PolicyName
	// both carry a modeled @length min of 1, so that is a length violation
	// (MIN_LENGTH_EXCEEDED), not a missing parameter (unlike CreatePolicy's
	// plain-string Name/Content, where "" is indistinguishable from absent).
	// Name and Description also carry a modeled @length max (128 and 512),
	// checked with the same validateMaxLength createPolicy uses; PolicyContent
	// has no modeled max, so it gets no such check here either.
	changed := false
	if in.Name != nil {
		if *in.Name == "" {
			return nil, invalidInput(reasonMinLengthExceeded, "Name must be at least 1 character.")
		}
		if aerr := validateMaxLength("Name", *in.Name, maxPolicyName); aerr != nil {
			return nil, aerr
		}
		if *in.Name != rec.Name {
			rec.Name = *in.Name
			changed = true
		}
	}
	if in.Description != nil {
		if aerr := validateMaxLength("Description", *in.Description, maxPolicyDescription); aerr != nil {
			return nil, aerr
		}
		if *in.Description != rec.Description {
			rec.Description = *in.Description
			changed = true
		}
	}
	if in.Content != nil {
		if *in.Content == "" {
			return nil, invalidInput(reasonMinLengthExceeded, "Content must be at least 1 character.")
		}
		if *in.Content != rec.Content {
			rec.Content = *in.Content
			changed = true
		}
	}
	if !changed {
		return &updatePolicyResponse{Policy: rec.view()}, nil
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
	if in.Filter == "" {
		return nil, invalidInput(reasonInputRequired, "Filter is a required parameter.")
	}
	if !policyTypes[in.Filter] {
		return nil, invalidInput(reasonInvalidEnumPolicyType, "Filter must be one of the modeled policy types.")
	}
	if aerr := checkMaxResults(in.MaxResults); aerr != nil {
		return nil, aerr
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
	// numbers come from — not from a house default. AWS answers
	// InvalidInputException (MAX_VALUE_EXCEEDED / MIN_VALUE_EXCEEDED)
	// instead of silently clamping, which is why checkMaxResults runs above
	// rather than relying on Paginate's own MaxLimit clamp.
	page, err := serviceutil.Paginate(matching, int(deref(in.MaxResults)), derefString(in.NextToken),
		serviceutil.PaginateOptions{DefaultLimit: 20, MaxLimit: 20})
	if err != nil {
		return nil, inert.PageError(err, errInvalidNextToken)
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
		return nil, invalidInput(reasonInputRequired, "Tags is a required parameter.")
	}
	if _, aerr := s.tags.Apply(ctx, arn, tagMap(in.Tags), tagRules); aerr != nil {
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
		return nil, invalidInput(reasonInputRequired, "TagKeys is a required parameter.")
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
	items := serviceutil.TagElements(tags, func(k, v string) resourceTag {
		return resourceTag{Key: k, Value: v}
	})
	// ListTagsForResource is @paginated with an inputToken/outputToken but no
	// pageSize member (confirmed against ListTagsForResourceRequest in the
	// model — it has no MaxResults), so there is no caller-supplied limit to
	// validate or honour — only a token to keep honest.
	page, err := serviceutil.Paginate(items, 0, derefString(in.NextToken), serviceutil.PaginateOptions{DefaultLimit: 50})
	if err != nil {
		return nil, inert.PageError(err, errInvalidNextToken)
	}
	return &listTagsForResourceResponse{NextToken: page.NextToken, Tags: page.Items}, nil
}

// ---- shared helpers --------------------------------------------------------

// loadPolicy is the read-or-modeled-not-found every policy operation starts
// with. A malformed persisted record reads as absent (inert.Store isolates
// it), so it surfaces as PolicyNotFoundException rather than a 500.
func (s *Service) loadPolicy(ctx context.Context, id string) (*policyRecord, *protocol.AWSError) {
	if id == "" {
		return nil, invalidInput(reasonInputRequired, "PolicyId is a required parameter.")
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
// resource policy. Three of those are resources this emulator holds — a
// policy, an organizational unit, and the organization's single root — and
// each resolves to its own ARN, so two resource types with overlapping id
// spaces could never share a tag set.
//
// Anything else is genuinely unknown here: accounts and resource policies are
// Tier 0, and a root- or OU-shaped id this organization does not hold names
// nothing. Those get the operation's own modeled TargetNotFoundException
// rather than a fabricated success — tagging something that does not exist
// and being told it worked is the §3.6 failure mode in a different costume.
func (s *Service) taggableARN(ctx context.Context, resourceID string) (string, *protocol.AWSError) {
	switch {
	case resourceID == "":
		return "", invalidInput(reasonInputRequired, "ResourceId is a required parameter.")
	case resourceID == s.rootID():
		// The root is derived rather than stored, so there is no record to
		// look up — but it is a resource ListRoots just returned, and
		// refusing to tag it would be telling the caller it does not exist.
		return s.rootARN(), nil
	case strings.HasPrefix(resourceID, "ou-"):
		rec, found, err := s.ous.Get(ctx, resourceID)
		if err != nil {
			return "", inert.StorageError(err)
		}
		if !found {
			return "", errTargetNotFound
		}
		return rec.Arn, nil
	case strings.HasPrefix(resourceID, "p-"):
		rec, found, err := s.policies.Get(ctx, resourceID)
		if err != nil {
			return "", inert.StorageError(err)
		}
		if !found {
			return "", errTargetNotFound
		}
		return rec.Arn, nil
	default:
		return "", errTargetNotFound
	}
}

// invalidInput builds InvalidInputException with the modeled Reason member
// set — AWS always populates it, and every invalid-input path in this
// service goes through here so no path can omit it by accident.
func invalidInput(reason, message string) *protocol.AWSError {
	return &protocol.AWSError{Code: errInvalidInput.Code, Message: message, HTTPStatus: errInvalidInput.HTTPStatus, Reason: reason}
}

// validateMaxLength is the one place every modeled @length maximum in this
// service is checked, shared by OrganizationalUnitName
// (validateOrganizationalUnitName, inert_ou.go) and PolicyName/
// PolicyDescription (createPolicy/updatePolicy, below) — three different
// shapes with the same rule, rather than three copies of it.
//
// It counts Unicode code points with utf8.RuneCountInString, not bytes:
// Smithy's @length trait is defined over a string's length as a sequence of
// code points, every pattern these fields carry (`^[\s\S]*$`) accepts
// non-ASCII text, and a multi-byte value must not be rejected based on
// len(value)'s byte count — see TestCreateOrganizationalUnit_NameLengthCountsRunesNotBytes
// and its policy siblings in inert_review_followups_test.go.
//
// It only checks the maximum. None of this service's minimums are checked
// here: a required, non-pointer member reports INPUT_REQUIRED when empty
// (indistinguishable from absent) and a pointer member the caller explicitly
// sent reports MIN_LENGTH_EXCEEDED when it decoded to empty — both are
// call-site decisions this length-only helper has no way to make on its own.
func validateMaxLength(field, value string, max int) *protocol.AWSError {
	if n := utf8.RuneCountInString(value); n > max {
		return invalidInput(reasonMaxLengthExceeded, fmt.Sprintf("%s must be at most %d characters.", field, max))
	}
	return nil
}

func tagMap(tags []resourceTag) map[string]string {
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
