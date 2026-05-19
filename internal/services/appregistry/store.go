package appregistry

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/middleware"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const (
	nsApplications    = "appregistry:applications"
	nsAssociations    = "appregistry:associations"
	nsAttributeGroups = "appregistry:attribute-groups"
	nsAttrGroupAssocs = "appregistry:attribute-group-associations"
)

// AttributeGroup is the domain model for an AppRegistry attribute group.
// Emulated at "inert tier": stored and returned from list/get, but the
// attributes JSON blob is opaque — the emulator does not validate it.
type AttributeGroup struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	ARN            string            `json:"arn"`
	Description    string            `json:"description,omitempty"`
	Attributes     string            `json:"attributes,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	CreationTime   float64           `json:"creationTime"`
	LastUpdateTime float64           `json:"lastUpdateTime"`
}

// Application is the domain model for an AppRegistry application.
type Application struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	ARN            string            `json:"arn"`
	Description    string            `json:"description,omitempty"`
	Tags           map[string]string `json:"tags,omitempty"`
	ApplicationTag map[string]string `json:"applicationTag,omitempty"`
	CreationTime   float64           `json:"creationTime"`
	LastUpdateTime float64           `json:"lastUpdateTime"`
}

// ResourceAssociation links one resource to an application.
// For AppRegistry, the resource type is always "CFN_STACK" in real AWS; we
// model the field generically so the UI can associate arbitrary resources.
type ResourceAssociation struct {
	ApplicationID string  `json:"applicationId"`
	ResourceType  string  `json:"resourceType"`
	ResourceARN   string  `json:"resourceArn"`
	CreationTime  float64 `json:"creationTime"`
}

type arStore struct {
	store         state.Store
	clk           clock.Clock
	defaultRegion string
}

func newStore(store state.Store, clk clock.Clock, defaultRegion string) *arStore {
	return &arStore{store: store, clk: clk, defaultRegion: defaultRegion}
}

func (s *arStore) region(ctx context.Context) string {
	return middleware.RegionFromContext(ctx, s.defaultRegion)
}

func (s *arStore) now() time.Time { return s.clk.Now() }

// ─── Applications ──────────────────────────────────────────────────────────

func (s *arStore) putApplication(ctx context.Context, app *Application) *protocol.AWSError {
	raw, err := json.Marshal(app)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsApplications, serviceutil.RegionKey(s.region(ctx), app.ID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) getApplication(ctx context.Context, id string) (*Application, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsApplications, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNotFound(id)
	}
	var app Application
	if err := json.Unmarshal([]byte(raw), &app); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &app, nil
}

func (s *arStore) deleteApplication(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsApplications, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) listApplications(ctx context.Context) ([]Application, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsApplications, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]Application, 0, len(pairs))
	for _, p := range pairs {
		var app Application
		if err := json.Unmarshal([]byte(p.Value), &app); err != nil {
			continue
		}
		out = append(out, app)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// resolveApplication accepts an application name, id, or ARN and returns the
// stored application. Real AWS AppRegistry accepts all three interchangeably
// in the path parameter, so the emulator does too.
func (s *arStore) resolveApplication(ctx context.Context, identifier string) (*Application, *protocol.AWSError) {
	if app, aerr := s.getApplication(ctx, identifier); aerr == nil {
		return app, nil
	}
	all, aerr := s.listApplications(ctx)
	if aerr != nil {
		return nil, aerr
	}
	for i := range all {
		if all[i].Name == identifier || all[i].ARN == identifier {
			return &all[i], nil
		}
	}
	return nil, errNotFound(identifier)
}

// ─── Associations ──────────────────────────────────────────────────────────

// associationKey namespaces one association uniquely under its application.
func associationKey(appID, resourceType, resourceARN string) string {
	return appID + "/" + resourceType + "/" + resourceARN
}

func (s *arStore) putAssociation(ctx context.Context, a *ResourceAssociation) *protocol.AWSError {
	raw, err := json.Marshal(a)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	k := serviceutil.RegionKey(s.region(ctx), associationKey(a.ApplicationID, a.ResourceType, a.ResourceARN))
	if err := s.store.Set(ctx, nsAssociations, k, string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) getAssociation(ctx context.Context, appID, resourceType, resourceARN string) (*ResourceAssociation, *protocol.AWSError) {
	k := serviceutil.RegionKey(s.region(ctx), associationKey(appID, resourceType, resourceARN))
	raw, found, err := s.store.Get(ctx, nsAssociations, k)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNotFound(resourceARN)
	}
	var a ResourceAssociation
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &a, nil
}

func (s *arStore) deleteAssociation(ctx context.Context, appID, resourceType, resourceARN string) *protocol.AWSError {
	k := serviceutil.RegionKey(s.region(ctx), associationKey(appID, resourceType, resourceARN))
	if err := s.store.Delete(ctx, nsAssociations, k); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) listAssociations(ctx context.Context, appID string) ([]ResourceAssociation, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), appID+"/")
	pairs, err := s.store.Scan(ctx, nsAssociations, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]ResourceAssociation, 0, len(pairs))
	for _, p := range pairs {
		var a ResourceAssociation
		if err := json.Unmarshal([]byte(p.Value), &a); err != nil {
			continue
		}
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ResourceARN < out[j].ResourceARN })
	return out, nil
}

// ─── Attribute groups ─────────────────────────────────────────────────────

func (s *arStore) putAttributeGroup(ctx context.Context, ag *AttributeGroup) *protocol.AWSError {
	raw, err := json.Marshal(ag)
	if err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	if err := s.store.Set(ctx, nsAttributeGroups, serviceutil.RegionKey(s.region(ctx), ag.ID), string(raw)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) getAttributeGroup(ctx context.Context, id string) (*AttributeGroup, *protocol.AWSError) {
	raw, found, err := s.store.Get(ctx, nsAttributeGroups, serviceutil.RegionKey(s.region(ctx), id))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	if !found {
		return nil, errNotFound(id)
	}
	var ag AttributeGroup
	if err := json.Unmarshal([]byte(raw), &ag); err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	return &ag, nil
}

func (s *arStore) deleteAttributeGroup(ctx context.Context, id string) *protocol.AWSError {
	if err := s.store.Delete(ctx, nsAttributeGroups, serviceutil.RegionKey(s.region(ctx), id)); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) listAttributeGroups(ctx context.Context) ([]AttributeGroup, *protocol.AWSError) {
	pairs, err := s.store.Scan(ctx, nsAttributeGroups, serviceutil.RegionKey(s.region(ctx), ""))
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]AttributeGroup, 0, len(pairs))
	for _, p := range pairs {
		var ag AttributeGroup
		if err := json.Unmarshal([]byte(p.Value), &ag); err != nil {
			continue
		}
		out = append(out, ag)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *arStore) resolveAttributeGroup(ctx context.Context, identifier string) (*AttributeGroup, *protocol.AWSError) {
	if ag, aerr := s.getAttributeGroup(ctx, identifier); aerr == nil {
		return ag, nil
	}
	all, aerr := s.listAttributeGroups(ctx)
	if aerr != nil {
		return nil, aerr
	}
	for i := range all {
		if all[i].Name == identifier || all[i].ARN == identifier {
			return &all[i], nil
		}
	}
	return nil, errNotFound(identifier)
}

// Attribute-group ↔ application associations (inert).
func (s *arStore) putAttrGroupAssoc(ctx context.Context, appID, agID string) *protocol.AWSError {
	k := serviceutil.RegionKey(s.region(ctx), appID+"/"+agID)
	if err := s.store.Set(ctx, nsAttrGroupAssocs, k, "{}"); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) deleteAttrGroupAssoc(ctx context.Context, appID, agID string) *protocol.AWSError {
	k := serviceutil.RegionKey(s.region(ctx), appID+"/"+agID)
	if err := s.store.Delete(ctx, nsAttrGroupAssocs, k); err != nil {
		return protocol.Wrap(protocol.ErrInternalError, err)
	}
	return nil
}

func (s *arStore) listAttrGroupAssocs(ctx context.Context, appID string) ([]string, *protocol.AWSError) {
	prefix := serviceutil.RegionKey(s.region(ctx), appID+"/")
	pairs, err := s.store.Scan(ctx, nsAttrGroupAssocs, prefix)
	if err != nil {
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}
	out := make([]string, 0, len(pairs))
	for _, p := range pairs {
		if i := strings.LastIndex(p.Key, "/"); i != -1 {
			out = append(out, p.Key[i+1:])
		}
	}
	sort.Strings(out)
	return out, nil
}

// ─── Errors ────────────────────────────────────────────────────────────────

func errNotFound(id string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ResourceNotFoundException",
		Message:    "The resource " + id + " was not found.",
		HTTPStatus: 404,
	}
}

func errConflict(name string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ConflictException",
		Message:    "An application with name " + name + " already exists.",
		HTTPStatus: 409,
	}
}

func errInvalidParameter(msg string) *protocol.AWSError {
	return &protocol.AWSError{
		Code:       "ValidationException",
		Message:    msg,
		HTTPStatus: 400,
	}
}
