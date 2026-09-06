package organizations

// inert_ou.go hand-wires Organizations' *root* and *organizational unit*
// resources against internal/inert, the second resource pair to follow
// inert_policy.go's shape (docs/plans/inert-tier-rollout.md phase I2 → I3).
//
// Read inert_policy.go's header first: everything it says about how this file
// is written — typed shapes mirroring the model, one record type that is the
// create input plus the derivations, errors selected from each operation's
// own declared `errors: [...]` list, nothing that is not in the model or in
// §3 of the plan — applies here unchanged. What is specific to this pair:
//
//   - **The root is derived, not stored.** An organization has exactly one
//     root, and this emulator has exactly one organization, so there is no
//     lifecycle to persist: ListRoots projects a value computed from the
//     organization id the same way DescribeOrganization does. Nothing can
//     create or delete a root, which is also true on AWS.
//   - **A unit's parent is immutable.** AWS models no operation that moves an
//     organizational unit, so ParentId is fixed at create — which is what
//     lets Path be computed once and persisted rather than walked on every
//     read.
//
// Every shape here was read out of the pruned Smithy snapshot Phase I1
// committed for this service, not guessed: the member names, the
// required-ness, the OrganizationalUnitId/OrganizationalUnitArn/RootId/
// RootArn/Path patterns, MaxResults' range and the per-operation error lists.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/overcast-sh/overcast/internal/inert"
	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// nsOrganizationalUnits is the state.Store namespace holding unit records.
// Organizations is global, so the keys carry no region (§3.5).
const nsOrganizationalUnits = "organizations:organizational-units"

// rootName is what AWS calls the root it creates with an organization. It is
// a fixed string rather than configuration because nothing in this emulator
// can rename a root: AWS models no operation that does.
const rootName = "Root"

// maxOrganizationalUnitName is OrganizationalUnitName's modeled @length max.
// The minimum, 1, is the presence check below.
const maxOrganizationalUnitName = 128

// listMaxResults is MaxResults' modeled @range — min 1, max 20 — shared by
// every paginated Organizations operation, which all target the one
// MaxResults shape.
const listMaxResults = 20

// ---- modeled errors (§3.3) -------------------------------------------------
//
// Selected from each operation's declared errors, with the HTTP status from
// the shape's @httpError trait — never invented.

var (
	// OrganizationalUnitNotFoundException, @httpError 404.
	errOrganizationalUnitNotFound = &protocol.AWSError{
		Code:       "OrganizationalUnitNotFoundException",
		Message:    "We can't find an OU with the OrganizationalUnitId that you specified.",
		HTTPStatus: http.StatusNotFound,
	}
	// ParentNotFoundException, @httpError 404 — declared by
	// CreateOrganizationalUnit and ListOrganizationalUnitsForParent, the two
	// operations whose input names a parent.
	errParentNotFound = &protocol.AWSError{
		Code:       "ParentNotFoundException",
		Message:    "We can't find a root or OU with the ParentId that you specified.",
		HTTPStatus: http.StatusNotFound,
	}
	// DuplicateOrganizationalUnitException, @httpError 409.
	errDuplicateOrganizationalUnit = &protocol.AWSError{
		Code:       "DuplicateOrganizationalUnitException",
		Message:    "An OU with the same name already exists in the specified parent.",
		HTTPStatus: http.StatusConflict,
	}
	// OrganizationalUnitNotEmptyException, @httpError 409. Unlike
	// DeletePolicy's PolicyInUseException, this one is reachable: child OUs
	// are stored, so a unit really can be non-empty. Accounts are not
	// modeled here, so a child account is the one way to make a unit
	// non-empty that this emulator cannot see.
	errOrganizationalUnitNotEmpty = &protocol.AWSError{
		Code:       "OrganizationalUnitNotEmptyException",
		Message:    "The specified OU is not empty. Remove all accounts and child OUs from the OU and try again.",
		HTTPStatus: http.StatusConflict,
	}
)

// ---- the persisted record (§3.2) -------------------------------------------

// organizationalUnitRecord is the create input unioned with everything
// UpdateOrganizationalUnit can set, plus the derivations. Responses are a
// projection of it onto the modeled OrganizationalUnit shape.
//
// ParentId is stored and never projected: OrganizationalUnit models no parent
// member, and §3.2 is explicit that a field the output shape does not carry
// is stored rather than bolted onto the response. It is load-bearing all the
// same — it is what ListOrganizationalUnitsForParent filters on and what
// DeleteOrganizationalUnit's emptiness check reads.
//
// Path is stored rather than computed per read because a unit's parent is
// immutable (AWS models no move operation), so the value is fixed the moment
// the unit is created. Computing it once also means a read never walks the
// ancestor chain, and a chain broken by a corrupt record cannot produce a
// Path that violates the modeled pattern.
//
// CreatedAt and UpdatedAt are stored but never projected either — the
// OrganizationalUnit shape carries no timestamp member. They still come from
// the injected clock, never time.Now() (§3.5);
// TestOrganizationalUnitTimestampsComeFromTheClock holds that, since the
// conformance suite's §3.5/timestamps clause has no modeled member to look
// at here.
type organizationalUnitRecord struct {
	Id        string    `json:"Id"`
	Arn       string    `json:"Arn"`
	Name      string    `json:"Name"`
	ParentId  string    `json:"ParentId"`
	Path      string    `json:"Path"`
	CreatedAt time.Time `json:"CreatedAt"`
	UpdatedAt time.Time `json:"UpdatedAt"`
}

// ---- modeled wire shapes ---------------------------------------------------

// organizationalUnitView mirrors the OrganizationalUnit shape, every member
// of which is optional and all four of which this service can answer for.
type organizationalUnitView struct {
	Arn  string `json:"Arn" cbor:"Arn"`
	Id   string `json:"Id" cbor:"Id"`
	Name string `json:"Name" cbor:"Name"`
	Path string `json:"Path" cbor:"Path"`
}

// rootView mirrors the Root shape.
type rootView struct {
	Arn  string `json:"Arn" cbor:"Arn"`
	Id   string `json:"Id" cbor:"Id"`
	Name string `json:"Name" cbor:"Name"`
	// PolicyTypes is the set of policy types *enabled on this root*, which is
	// not the same set DescribeOrganization reports as available in the
	// organization (see describeOrganizationTyped's
	// organizationDetails.AvailablePolicyTypes in typed_logic.go — that list
	// is hand-picked to just SERVICE_CONTROL_POLICY, independent of this
	// one). Enabling one is EnablePolicyType's job and that operation is
	// Tier 0 here, so nothing can ever have enabled one and the honest
	// answer is the empty list — never omitted, because "no policy types are
	// enabled" is a fact about the root rather than an absence of
	// information.
	PolicyTypes []policyTypeSummary `json:"PolicyTypes" cbor:"PolicyTypes"`
}

// policyTypeSummary mirrors the PolicyTypeSummary shape. No root this service
// mints carries one yet (see rootView.PolicyTypes), but the list has to be
// typed as something, and the model says what.
type policyTypeSummary struct {
	Status string `json:"Status" cbor:"Status"`
	Type   string `json:"Type" cbor:"Type"`
}

type listRootsRequest struct {
	MaxResults *int32  `json:"MaxResults" cbor:"MaxResults"`
	NextToken  *string `json:"NextToken" cbor:"NextToken"`
}

type listRootsResponse struct {
	NextToken string     `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
	Roots     []rootView `json:"Roots" cbor:"Roots"`
}

// createOrganizationalUnitRequest mirrors CreateOrganizationalUnitRequest:
// Name and ParentId are @required, Tags is optional.
type createOrganizationalUnitRequest struct {
	Name     string        `json:"Name" cbor:"Name"`
	ParentId string        `json:"ParentId" cbor:"ParentId"`
	Tags     []resourceTag `json:"Tags" cbor:"Tags"`
}

type createOrganizationalUnitResponse struct {
	OrganizationalUnit organizationalUnitView `json:"OrganizationalUnit" cbor:"OrganizationalUnit"`
}

type describeOrganizationalUnitRequest struct {
	OrganizationalUnitId string `json:"OrganizationalUnitId" cbor:"OrganizationalUnitId"`
}

type describeOrganizationalUnitResponse struct {
	OrganizationalUnit organizationalUnitView `json:"OrganizationalUnit" cbor:"OrganizationalUnit"`
}

// updateOrganizationalUnitRequest mirrors UpdateOrganizationalUnitRequest.
// Name is a pointer because it is the model's only optional member here and
// a nil pointer means "not sent", which is what makes §3.1's merge a merge.
type updateOrganizationalUnitRequest struct {
	Name                 *string `json:"Name" cbor:"Name"`
	OrganizationalUnitId string  `json:"OrganizationalUnitId" cbor:"OrganizationalUnitId"`
}

type updateOrganizationalUnitResponse struct {
	OrganizationalUnit organizationalUnitView `json:"OrganizationalUnit" cbor:"OrganizationalUnit"`
}

type deleteOrganizationalUnitRequest struct {
	OrganizationalUnitId string `json:"OrganizationalUnitId" cbor:"OrganizationalUnitId"`
}

// deleteOrganizationalUnitResponse is the operation's modeled
// smithy.api#Unit output: an empty body, not an echo of what was deleted.
type deleteOrganizationalUnitResponse struct{}

type listOrganizationalUnitsForParentRequest struct {
	MaxResults *int32  `json:"MaxResults" cbor:"MaxResults"`
	NextToken  *string `json:"NextToken" cbor:"NextToken"`
	ParentId   string  `json:"ParentId" cbor:"ParentId"`
}

type listOrganizationalUnitsForParentResponse struct {
	NextToken           string                   `json:"NextToken,omitempty" cbor:"NextToken,omitempty"`
	OrganizationalUnits []organizationalUnitView `json:"OrganizationalUnits" cbor:"OrganizationalUnits"`
}

// ---- derivations (§3.5) ----------------------------------------------------

// rootID derives an organization's root identifier from the organization id,
// the same way organizationID derives that from the account id: a SHA-256
// digest, hex-encoded (lowercase alphanumeric, exactly what RootId's
// `^r-[0-9a-z]{4,32}$` accepts) and truncated to the four characters AWS
// issues after `r-`.
//
// Deriving rather than persisting is what makes the root survive a restart
// and a state export/import: it is the parent of every organizational unit
// and appears in every OU id and Path, so a value that moved would move all
// of those with it. Nothing else about a root is mutable, so there is
// nothing else to store.
func rootID(orgID string) string {
	sum := sha256.Sum256([]byte(orgID))
	return "r-" + hex.EncodeToString(sum[:2])
}

// rootID resolves this service's own root identifier.
func (s *Service) rootID() string { return rootID(s.organizationID()) }

// organizationalUnitID derives a unit's identifier from its parent and its
// name.
//
// AWS mints an opaque `ou-<root's four characters>-<eight or more>`; the
// model constrains the shape (`^ou-[0-9a-z]{4,32}-[a-z0-9]{8,32}$`) and, in
// every AWS-issued id, the first segment is the root the unit belongs to.
// Both halves are reproduced here: the root's four characters, then eight
// hex characters of a digest over the parent id and the name.
//
// Deriving from parent+name rather than from a counter or a UUID buys the
// same two things policyID does — an identifier stable across restarts and
// across a state export/import, and duplicate detection with no second index
// to keep consistent — and it encodes the constraint AWS actually states:
// DuplicateOrganizationalUnitException is scoped to the parent, so the same
// name under two parents is two different units and derives two different
// ids.
//
// The same divergence policyID records applies: renaming a unit leaves its
// identifier derived from the old name, so the old name stays taken under
// that parent and recreating it returns DuplicateOrganizationalUnitException
// where real AWS would allow it. AWS identifiers are opaque, so nothing
// observable depends on the id tracking the name.
func organizationalUnitID(rootID, parentID, name string) string {
	sum := sha256.Sum256([]byte(parentID + "\x00" + name))
	return "ou-" + strings.TrimPrefix(rootID, "r-") + "-" + hex.EncodeToString(sum[:4])
}

// rootARN renders the RootArn template, verbatim from the snapshot's pattern.
func (s *Service) rootARN() string {
	return fmt.Sprintf("arn:aws:organizations::%s:root/%s/%s", s.accountID(), s.organizationID(), s.rootID())
}

// organizationalUnitARN renders the OrganizationalUnitArn template, verbatim
// from the snapshot's pattern. Note it names the organization and the unit
// but not the unit's parent, so an ARN is stable for the unit's life.
func (s *Service) organizationalUnitARN(id string) string {
	return fmt.Sprintf("arn:aws:organizations::%s:ou/%s/%s", s.accountID(), s.organizationID(), id)
}

// rootPath is the Path a direct child of the root inherits — the first two
// segments of Path's modeled pattern, `<org>/<root>/`.
func (s *Service) rootPath() string {
	return s.organizationID() + "/" + s.rootID() + "/"
}

// root projects this organization's single root onto the modeled Root shape.
func (s *Service) root() rootView {
	return rootView{
		Arn:         s.rootARN(),
		Id:          s.rootID(),
		Name:        rootName,
		PolicyTypes: []policyTypeSummary{},
	}
}

func (rec *organizationalUnitRecord) view() organizationalUnitView {
	return organizationalUnitView{Arn: rec.Arn, Id: rec.Id, Name: rec.Name, Path: rec.Path}
}

// ---- handlers --------------------------------------------------------------

func (s *Service) listRoots(_ context.Context, in *listRootsRequest) (*listRootsResponse, *protocol.AWSError) {
	if aerr := checkMaxResults(in.MaxResults); aerr != nil {
		return nil, aerr
	}
	// One organization, one root. The list is built rather than read, so
	// there is no store call to fail — but the continuation token still has
	// to be honest: a garbage token is the modeled error, never a silent
	// restart at page 1 (pagination-plan H1/G3).
	page, err := serviceutil.Paginate([]rootView{s.root()}, int(deref(in.MaxResults)), derefString(in.NextToken),
		serviceutil.PaginateOptions{DefaultLimit: listMaxResults, MaxLimit: listMaxResults})
	if err != nil {
		return nil, inert.PageError(err, errInvalidNextToken)
	}
	return &listRootsResponse{NextToken: page.NextToken, Roots: page.Items}, nil
}

func (s *Service) createOrganizationalUnit(ctx context.Context, in *createOrganizationalUnitRequest) (*createOrganizationalUnitResponse, *protocol.AWSError) {
	// §3.4: exactly the checks the model states — @required presence and
	// @length. Nothing cross-field, nothing referential beyond the parent
	// reference the operation's own ParentNotFoundException exists for.
	if aerr := validateOrganizationalUnitName(in.Name, true); aerr != nil {
		return nil, aerr
	}
	parent, aerr := s.resolveParent(ctx, in.ParentId)
	if aerr != nil {
		return nil, aerr
	}

	// Two duplicate checks, because they catch two different things. The
	// sibling scan is the constraint AWS states — one name per parent — and
	// it sees a unit that was renamed onto this name. The id check catches
	// the converse: a unit created under this name, renamed away, and now
	// recreated, which derives the identifier that unit still occupies (see
	// organizationalUnitID). Without it the create would silently overwrite
	// the renamed unit.
	siblings, aerr := s.childrenOf(ctx, parent.id)
	if aerr != nil {
		return nil, aerr
	}
	for _, sibling := range siblings {
		if sibling.Name == in.Name {
			return nil, errDuplicateOrganizationalUnit
		}
	}
	id := organizationalUnitID(s.rootID(), parent.id, in.Name)
	if _, found, err := s.ous.Get(ctx, id); err != nil {
		return nil, inert.StorageError(err)
	} else if found {
		return nil, errDuplicateOrganizationalUnit
	}

	now := s.ous.Now()
	rec := &organizationalUnitRecord{
		Id:        id,
		Arn:       s.organizationalUnitARN(id),
		Name:      in.Name,
		ParentId:  parent.id,
		Path:      parent.path + id + "/",
		CreatedAt: now,
		UpdatedAt: now,
	}

	// The record is persisted before its tags are applied: a failed Put must
	// never leave tags written against an ARN with no backing record (an
	// orphan ListTagsForResource could answer for).
	if err := s.ous.Put(ctx, id, rec); err != nil {
		return nil, inert.StorageError(err)
	}
	// Tags on the create input write through to the same store the tag
	// operations read, so ListTagsForResource can never disagree with what
	// CreateOrganizationalUnit accepted (§3.1's Tag class, §7.3).
	if len(in.Tags) > 0 {
		if _, aerr := s.tags.Apply(ctx, rec.Arn, tagMap(in.Tags), tagRules); aerr != nil {
			return nil, aerr
		}
	}
	return &createOrganizationalUnitResponse{OrganizationalUnit: rec.view()}, nil
}

func (s *Service) describeOrganizationalUnit(ctx context.Context, in *describeOrganizationalUnitRequest) (*describeOrganizationalUnitResponse, *protocol.AWSError) {
	rec, aerr := s.loadOrganizationalUnit(ctx, in.OrganizationalUnitId)
	if aerr != nil {
		return nil, aerr
	}
	return &describeOrganizationalUnitResponse{OrganizationalUnit: rec.view()}, nil
}

func (s *Service) updateOrganizationalUnit(ctx context.Context, in *updateOrganizationalUnitRequest) (*updateOrganizationalUnitResponse, *protocol.AWSError) {
	rec, aerr := s.loadOrganizationalUnit(ctx, in.OrganizationalUnitId)
	if aerr != nil {
		return nil, aerr
	}
	// A merge, not a replace: Name is the only member this operation takes
	// besides the identifier, and only a caller who sent it moves anything.
	// changed tracks whether anything on the record actually moved, so a
	// caller that omits Name (or resends the current one) is a true no-op:
	// no UpdatedAt bump and no store write, rather than a rewrite of an
	// otherwise-identical record.
	changed := false
	if in.Name != nil && *in.Name != rec.Name {
		if aerr := validateOrganizationalUnitName(*in.Name, false); aerr != nil {
			return nil, aerr
		}
		// UpdateOrganizationalUnit declares
		// DuplicateOrganizationalUnitException, and a rename onto a sibling's
		// name is the only way to reach it — the same one-name-per-parent
		// constraint the create enforces.
		siblings, aerr := s.childrenOf(ctx, rec.ParentId)
		if aerr != nil {
			return nil, aerr
		}
		for _, sibling := range siblings {
			if sibling.Id != rec.Id && sibling.Name == *in.Name {
				return nil, errDuplicateOrganizationalUnit
			}
		}
		rec.Name = *in.Name
		changed = true
	}
	if !changed {
		return &updateOrganizationalUnitResponse{OrganizationalUnit: rec.view()}, nil
	}
	rec.UpdatedAt = s.ous.Now()

	// Id, Arn and Path are derived from the immutable parent and the id, so a
	// rename moves none of them — matching AWS, where an OU's identity is
	// fixed for its life.
	if err := s.ous.Put(ctx, rec.Id, rec); err != nil {
		return nil, inert.StorageError(err)
	}
	return &updateOrganizationalUnitResponse{OrganizationalUnit: rec.view()}, nil
}

func (s *Service) deleteOrganizationalUnit(ctx context.Context, in *deleteOrganizationalUnitRequest) (*deleteOrganizationalUnitResponse, *protocol.AWSError) {
	rec, aerr := s.loadOrganizationalUnit(ctx, in.OrganizationalUnitId)
	if aerr != nil {
		return nil, aerr
	}
	// OrganizationalUnitNotEmptyException. AWS counts child accounts as well
	// as child OUs; accounts are Tier 0 here, so a child unit is the only
	// occupant this emulator can see.
	children, aerr := s.childrenOf(ctx, rec.Id)
	if aerr != nil {
		return nil, aerr
	}
	if len(children) > 0 {
		return nil, errOrganizationalUnitNotEmpty
	}
	// Tags go with the record, as they do for a policy: namespaced tags have
	// nothing tying them to the record's lifetime, so skipping this leaves
	// ListTagsForResource answering for a unit that is gone.
	if aerr := s.tags.Delete(ctx, rec.Arn); aerr != nil {
		return nil, aerr
	}
	if err := s.ous.Delete(ctx, rec.Id); err != nil {
		return nil, inert.StorageError(err)
	}
	return &deleteOrganizationalUnitResponse{}, nil
}

func (s *Service) listOrganizationalUnitsForParent(ctx context.Context, in *listOrganizationalUnitsForParentRequest) (*listOrganizationalUnitsForParentResponse, *protocol.AWSError) {
	// The parent is resolved before the scan so an unknown parent is the
	// operation's own ParentNotFoundException rather than an empty list: the
	// difference between "no children" and "no such parent" is one a caller
	// acts on.
	if aerr := checkMaxResults(in.MaxResults); aerr != nil {
		return nil, aerr
	}
	parent, aerr := s.resolveParent(ctx, in.ParentId)
	if aerr != nil {
		return nil, aerr
	}
	children, aerr := s.childrenOf(ctx, parent.id)
	if aerr != nil {
		return nil, aerr
	}

	// MaxResults is @range min 1 max 20 in the model, which is where both
	// numbers come from — not from a house default. AWS answers
	// InvalidInputException (MAX_VALUE_EXCEEDED / MIN_VALUE_EXCEEDED)
	// instead of silently clamping, which is why checkMaxResults runs before
	// Paginate rather than relying on its own MaxLimit clamp.
	page, err := serviceutil.Paginate(children, int(deref(in.MaxResults)), derefString(in.NextToken),
		serviceutil.PaginateOptions{DefaultLimit: listMaxResults, MaxLimit: listMaxResults})
	if err != nil {
		return nil, inert.PageError(err, errInvalidNextToken)
	}

	out := &listOrganizationalUnitsForParentResponse{
		NextToken:           page.NextToken,
		OrganizationalUnits: make([]organizationalUnitView, 0, len(page.Items)),
	}
	for _, rec := range page.Items {
		out.OrganizationalUnits = append(out.OrganizationalUnits, rec.view())
	}
	return out, nil
}

// ---- shared helpers --------------------------------------------------------

// parentRef is a resolved ParentId: the identifier itself, and the Path
// prefix a child of it inherits.
type parentRef struct {
	id   string
	path string
}

// resolveParent turns a ParentId into the root or the stored unit it names.
//
// ParentId's modeled pattern accepts a root id or an OU id and nothing else.
// A value of either shape that this organization does not hold — and any
// value of neither shape — is the operation's own ParentNotFoundException,
// which is the same choice taggableARN makes for an unknown tag target: an
// identifier this emulator cannot resolve is genuinely not there, and saying
// so is better than a fabricated success under an invented parent.
func (s *Service) resolveParent(ctx context.Context, parentID string) (parentRef, *protocol.AWSError) {
	if parentID == "" {
		return parentRef{}, invalidInput(reasonInputRequired, "ParentId is a required parameter.")
	}
	if parentID == s.rootID() {
		return parentRef{id: parentID, path: s.rootPath()}, nil
	}
	if strings.HasPrefix(parentID, "ou-") {
		rec, found, err := s.ous.Get(ctx, parentID)
		if err != nil {
			return parentRef{}, inert.StorageError(err)
		}
		if found {
			return parentRef{id: rec.Id, path: rec.Path}, nil
		}
	}
	return parentRef{}, errParentNotFound
}

// loadOrganizationalUnit is the read-or-modeled-not-found every unit
// operation starts with. A malformed persisted record reads as absent
// (inert.Store isolates it), so it surfaces as
// OrganizationalUnitNotFoundException rather than a 500.
func (s *Service) loadOrganizationalUnit(ctx context.Context, id string) (*organizationalUnitRecord, *protocol.AWSError) {
	if id == "" {
		return nil, invalidInput(reasonInputRequired, "OrganizationalUnitId is a required parameter.")
	}
	rec, found, err := s.ous.Get(ctx, id)
	if err != nil {
		return nil, inert.StorageError(err)
	}
	if !found {
		return nil, errOrganizationalUnitNotFound
	}
	return rec, nil
}

// childrenOf returns the units directly under parentID, in store-key order —
// which is identifier order, so the listing is stable-sorted without a
// re-sort (§3.1's List class).
//
// It scans the collection and filters, the way ListPolicies filters by Type,
// rather than maintaining a parent→children index. A second index is a second
// thing to keep consistent, and it would have to be updated by every create
// and delete; the plan's §7.3 names exactly that shape as the failure mode to
// design against.
func (s *Service) childrenOf(ctx context.Context, parentID string) ([]*organizationalUnitRecord, *protocol.AWSError) {
	all, err := s.ous.List(ctx)
	if err != nil {
		return nil, inert.StorageError(err)
	}
	out := make([]*organizationalUnitRecord, 0, len(all))
	for _, rec := range all {
		if rec.ParentId == parentID {
			out = append(out, rec)
		}
	}
	return out, nil
}

// validateOrganizationalUnitName holds OrganizationalUnitName's modeled
// @length 1..128. Its @pattern is `^[\s\S]*$`, which accepts anything, so
// there is nothing else to check.
//
// The maximum is checked by validateMaxLength (inert_policy.go), shared with
// PolicyName and PolicyDescription's own maxima — see that function's
// comment for why it counts Unicode code points rather than bytes.
//
// required distinguishes why an empty name is invalid: CreateOrganizationalUnit
// takes Name as a plain (non-pointer) string, so an empty value there is
// indistinguishable from an absent one and reports the model's
// CALLER_REQUIRED_FIELD_MISSING-style INPUT_REQUIRED. UpdateOrganizationalUnit's
// Name is a pointer — the caller here already sent a non-nil, differing
// value that decoded to "" — so the same empty string is a length violation
// (MIN_LENGTH_EXCEEDED) rather than a missing parameter.
func validateOrganizationalUnitName(name string, required bool) *protocol.AWSError {
	if name == "" {
		if required {
			return invalidInput(reasonInputRequired, "Name is a required parameter.")
		}
		return invalidInput(reasonMinLengthExceeded, "Name must be at least 1 character.")
	}
	return validateMaxLength("Name", name, maxOrganizationalUnitName)
}
