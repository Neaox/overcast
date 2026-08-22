package backup

// handler_tags.go — AWS Backup TagResource / ListTags / UntagResource (#1195).
//
//	GET  /tags/{ResourceArn}    ListTags
//	POST /tags/{ResourceArn}    TagResource
//	POST /untag/{ResourceArn}   UntagResource
//
// TagResource and ListTags share the "/tags" path space with Pipes, EKS,
// Scheduler, AppConfig and API Gateway — internal/router/router.go's "/tags
// service dispatch" block owns that path and routes each request to the
// service named by the ARN's own service component (protocol.ServiceFromARN),
// exactly as it does for the others. TagsRouter, below, is what that
// dispatcher mounts for Backup. UntagResource does not: Backup unbinds tags
// at POST /untag/{ResourceArn}, not DELETE /tags, so it is registered
// directly by RegisterRoutes (service.go) instead — see
// docs/plans/resource-tagging-coverage.md's backup section.
//
// Tags are stored inline on the vault/plan record (vaultRecord.Tags,
// planRecord.Tags in store.go) rather than in a namespace of their own, so
// deleting the resource deletes its tags in the same store write — nothing
// can outlive the record it describes. vaultTagStore/planTagStore below
// adapt that inline storage to serviceutil.TagStore so TagResource/
// UntagResource can share serviceutil.ApplyStoreTags/RemoveStoreTags with
// every other service that stores tags this way.

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// backupTagCfg tunes the shared tag validator to Backup's error shape.
//
// AWS Backup's TagResource models InvalidParameterValueException for a
// malformed key or value and no TooManyTagsException — the same audit this
// package's other error helpers rely on (see the "Errors" section comment
// above) found no ValidationException or count-specific exception anywhere in
// the service, so the tag-count limit is reported with the same code as an
// invalid key or value, matching every other rejection this service returns.
var backupTagCfg = serviceutil.TagValidationConfig{
	ExceededCode:    "InvalidParameterValueException",
	InvalidCode:     "InvalidParameterValueException",
	ExceededMessage: "A resource can have no more than 50 tags.",
}

// TagsRouter returns a chi.Router for Backup's TagResource and ListTags,
// which live under the shared /tags/{ResourceArn} path space. The main
// router mounts it behind the ARN-dispatching owner it shares with Pipes,
// EKS, Scheduler, AppConfig and API Gateway. UntagResource is NOT here — see
// this file's header comment.
//
// ListTags is registered both with and without a trailing slash. Confirmed
// against the real AWS CLI (botocore): ListTags's request carries a trailing
// slash after the ARN segment (`GET /tags/{ResourceArn}/`) though TagResource
// does not, the same asymmetric-by-operation quirk service.go's RegisterRoutes
// doc comment already describes for ListBackupVaults/ListBackupPlans/
// CreateBackupPlan/GetBackupPlan — "which of the two spellings a binding uses
// is the model's choice and follows no rule worth inferring". Missing this
// form made every real-CLI ListTags call 404 (compat's cli/backup-tags group,
// #1195).
func (s *Service) TagsRouter() chi.Router {
	r := chi.NewRouter()
	r.Get("/{ResourceArn}", s.listTags)
	r.Get("/{ResourceArn}/", s.listTags)
	r.Post("/{ResourceArn}", s.tagResource)
	return r
}

// resourceArnParam reads and URL-decodes the {ResourceArn} path label. AWS
// SDKs percent-encode the ARN into a single path segment (its colons become
// %3A), which is what the chi pattern matches literally without decoding.
func resourceArnParam(r *http.Request) string {
	raw := chi.URLParam(r, "ResourceArn")
	decoded, err := url.PathUnescape(raw)
	if err != nil {
		return raw
	}
	return decoded
}

// resolveTagTarget maps a resourceArn to the TagStore for the vault or plan
// it names. Backup vault ARNs are arn:aws:backup:region:account:backup-vault:
// name; plan ARNs are arn:aws:backup:region:account:backup-plan:id — see
// vaultARN/planARN in service.go, which this mirrors in reverse. The region
// is read from the ARN itself rather than the request's signing region:
// unlike the vault/plan CRUD routes (which have no ARN to read and so trust
// middleware.RegionFromContext), a resourceArn already names its own region
// unambiguously, and using anything else would let a caller in one region
// address a resource whose ARN claims another.
func (s *Service) resolveTagTarget(arn string) (serviceutil.TagStore, *protocol.AWSError) {
	parts := strings.Split(arn, ":")
	if len(parts) < 7 || parts[0] != "arn" || parts[2] != "backup" || parts[6] == "" {
		return nil, tagResourceNotFound(arn)
	}
	region, kind, id := parts[3], parts[5], parts[6]
	switch kind {
	case "backup-vault":
		return vaultTagStore{s: s, region: region, name: id}, nil
	case "backup-plan":
		return planTagStore{s: s, region: region, id: id}, nil
	default:
		return nil, tagResourceNotFound(arn)
	}
}

func tagResourceNotFound(arn string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "An ARN in the request is not valid because a required field is missing or empty: " + arn,
		HTTPStatus: http.StatusBadRequest,
	}
}

// tagResource handles POST /tags/{ResourceArn}.
func (s *Service) tagResource(w http.ResponseWriter, r *http.Request) {
	target, aerr := s.resolveTagTarget(resourceArnParam(r))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	var req struct {
		Tags map[string]string `json:"Tags"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, aerr := serviceutil.ApplyStoreTags(r.Context(), target, "", req.Tags, backupTagCfg); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// untagResource handles POST /untag/{ResourceArn}.
func (s *Service) untagResource(w http.ResponseWriter, r *http.Request) {
	target, aerr := s.resolveTagTarget(resourceArnParam(r))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	// UntagResource's modeled member is TagKeyList, not the TagKeys spelling
	// most other services use — confirmed against the real AWS CLI (botocore
	// sends {"TagKeyList": [...]}), which a first pass got wrong (#1195): the
	// mismatched field name meant DecodeJSON silently left it empty and every
	// untag was a no-op rather than an error, so no test caught it until the
	// compat suite's real CLI call.
	var req struct {
		TagKeyList []string `json:"TagKeyList"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}
	if _, aerr := serviceutil.RemoveStoreTags(r.Context(), target, "", req.TagKeyList); aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct{}{})
}

// listTags handles GET /tags/{ResourceArn}.
func (s *Service) listTags(w http.ResponseWriter, r *http.Request) {
	target, aerr := s.resolveTagTarget(resourceArnParam(r))
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	tags, aerr := target.Load(r.Context(), "")
	if aerr != nil {
		protocol.WriteJSONError(w, r, aerr)
		return
	}
	protocol.WriteJSON(w, r, http.StatusOK, struct {
		Tags map[string]string `json:"Tags"`
	}{Tags: tags})
}

// vaultTagStore adapts one resolved backup vault's inline Tags field to
// serviceutil.TagStore. The key parameter every TagStore method carries is
// unused: region and name are already resolved, closed over when
// resolveTagTarget builds this value.
type vaultTagStore struct {
	s      *Service
	region string
	name   string
}

func (t vaultTagStore) Load(ctx context.Context, _ string) (map[string]string, *protocol.AWSError) {
	v, found, err := t.s.store.getVault(ctx, t.region, t.name)
	if err != nil {
		return nil, t.s.wrapStoreErr(err)
	}
	if !found {
		return nil, notFoundError("Backup vault", t.name)
	}
	if v.Tags == nil {
		return map[string]string{}, nil
	}
	return v.Tags, nil
}

func (t vaultTagStore) Save(ctx context.Context, _ string, tags map[string]string) *protocol.AWSError {
	v, found, err := t.s.store.getVault(ctx, t.region, t.name)
	if err != nil {
		return t.s.wrapStoreErr(err)
	}
	if !found {
		return notFoundError("Backup vault", t.name)
	}
	v.Tags = tags
	if err := t.s.store.putVault(ctx, t.region, v); err != nil {
		return t.s.wrapStoreErr(err)
	}
	return nil
}

// Delete satisfies TagStore. Nothing calls it: a vault's tags are inline on
// its record, so they are removed by deleteBackupVault deleting the record,
// not by an independent tag-lifecycle call.
func (t vaultTagStore) Delete(ctx context.Context, _ string) *protocol.AWSError { return nil }

// planTagStore is vaultTagStore for backup plans.
type planTagStore struct {
	s      *Service
	region string
	id     string
}

func (t planTagStore) Load(ctx context.Context, _ string) (map[string]string, *protocol.AWSError) {
	p, found, err := t.s.store.getPlan(ctx, t.region, t.id)
	if err != nil {
		return nil, t.s.wrapStoreErr(err)
	}
	if !found {
		return nil, notFoundError("Backup plan", t.id)
	}
	if p.Tags == nil {
		return map[string]string{}, nil
	}
	return p.Tags, nil
}

func (t planTagStore) Save(ctx context.Context, _ string, tags map[string]string) *protocol.AWSError {
	p, found, err := t.s.store.getPlan(ctx, t.region, t.id)
	if err != nil {
		return t.s.wrapStoreErr(err)
	}
	if !found {
		return notFoundError("Backup plan", t.id)
	}
	p.Tags = tags
	if err := t.s.store.putPlan(ctx, t.region, p); err != nil {
		return t.s.wrapStoreErr(err)
	}
	return nil
}

// Delete satisfies TagStore; see vaultTagStore.Delete.
func (t planTagStore) Delete(ctx context.Context, _ string) *protocol.AWSError { return nil }
