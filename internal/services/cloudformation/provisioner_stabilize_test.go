package cloudformation

// provisioner_stabilize_test.go — the contract every resourceStabilizer relies
// on, tested once here rather than per resource type.
//
// A resource that fails to stabilize is not a resource that was never created.
// Its create succeeded and named it — an RDS instance exists and is holding its
// identifier, an ECS service exists and is holding its name — and only then did
// the database or the containers fail to come up. Rollback has to delete it, or
// the stack disappears leaving a real resource behind under a name nothing
// records, which the next deploy then collides with.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

// unstableHandler creates successfully and then never settles, recording what
// it was asked to delete so a test can see whether the resource it created was
// cleaned up.
type unstableHandler struct {
	mu      sync.Mutex
	deleted []string
}

const unstablePhysicalID = "physical-id-of-a-resource-that-exists"

func (h *unstableHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	return unstablePhysicalID, nil, nil
}

func (h *unstableHandler) Delete(_ context.Context, _ http.Handler, _ *config.Config, physicalID string, _ *resolveContext) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.deleted = append(h.deleted, physicalID)
	return nil
}

func (h *unstableHandler) Stabilize(context.Context, http.Handler, *config.Config, clock.Clock, string, *resolveContext) error {
	return errors.New("the database never came up")
}

func (h *unstableHandler) deletedIDs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.deleted...)
}

// registerUnstable installs the handler under a private resource type for the
// duration of the test.
func registerUnstable(t *testing.T) (*unstableHandler, string) {
	t.Helper()
	handler := &unstableHandler{}
	const resType = "Test::Stabilize::Never"
	resourceHandlers[resType] = handler
	t.Cleanup(func() { delete(resourceHandlers, resType) })
	return handler, resType
}

func newProvisionerTestFixture(t *testing.T) *provisioner {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	clk := clock.New()
	st := newCFNStore(state.NewMemoryStore(), cfg.Region, clk)
	p := newProvisioner(cfg, st, clk, serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation"))
	p.initRouter(http.NotFoundHandler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.stop(ctx)
	})
	return p
}

// The physical ID is what rollback deletes the resource by, so it has to travel
// out with the failure rather than be dropped for an empty string.
func TestProvisionResource_failedStabilizationKeepsThePhysicalID(t *testing.T) {
	// Given: a resource whose create succeeds and which then never settles
	_, resType := registerUnstable(t)
	p := newProvisionerTestFixture(t)

	// When: the stack provisions it
	physID, err := p.provisionResource(context.Background(), "R", TemplateResource{Type: resType},
		map[string]any{}, &resolveContext{Region: "us-east-1", StackName: "s"})

	// Then: it fails, and names the resource it created
	if err == nil {
		t.Fatal("expected the resource to fail")
	}
	if !strings.Contains(err.Error(), "never came up") {
		t.Errorf("expected the stabilizer's own reason, got %v", err)
	}
	if physID != unstablePhysicalID {
		t.Errorf("physical ID = %q, want %q — the resource exists, and rollback deletes it by name",
			physID, unstablePhysicalID)
	}
}

// A replacement is created before the old resource is deleted. If that new
// resource then fails to stabilize, rollback needs both IDs: the new one to
// delete and the old one to keep serving the stack.
func TestUpdateResource_failedReplacementStabilizationKeepsBothPhysicalIDs(t *testing.T) {
	// Given: an existing resource whose handler replaces it, then fails to settle
	_, resType := registerUnstable(t)
	p := newProvisionerTestFixture(t)
	old := &StackResource{PhysicalID: "original-physical-id"}

	// When: CloudFormation attempts the replacement
	outcome, err := p.updateResource(context.Background(), "R", TemplateResource{Type: resType},
		map[string]any{}, old.PhysicalID, old, &resolveContext{Region: "us-east-1", StackName: "s"})

	// Then: it fails but preserves the replacement relationship for rollback
	if err == nil {
		t.Fatal("expected the replacement to fail stabilization")
	}
	if outcome.PhysicalID != unstablePhysicalID {
		t.Errorf("replacement physical ID = %q, want %q", outcome.PhysicalID, unstablePhysicalID)
	}
	if outcome.ReplacedPhysicalID != old.PhysicalID {
		t.Errorf("original physical ID = %q, want %q", outcome.ReplacedPhysicalID, old.PhysicalID)
	}
}

// End to end: when stabilization fails after a replacement create, update
// rollback deletes the replacement and restores the original resource record.
func TestUpdateStack_rollbackDeletesAReplacementThatFailedToStabilize(t *testing.T) {
	// Given: an existing resource whose changed template requires replacement
	handler, resType := registerUnstable(t)
	p := newProvisionerTestFixture(t)
	const originalID = "original-physical-id"
	stack := &Stack{
		StackName: "unstable-update-stack",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/unstable-update-stack/1111",
		Region:    "us-east-1",
		Resources: []StackResource{{
			LogicalID:      "R",
			PhysicalID:     originalID,
			Type:           resType,
			Status:         ResourceCreateComplete,
			PropertiesHash: "old-properties",
		}},
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"R": {Type: resType, Properties: map[string]any{"Version": "new"}},
	}}

	// When: the replacement is created but cannot stabilize
	p.updateStackResources(stack, tmpl, captureStackGeneration(stack))

	// Then: rollback removes the replacement and puts the original back
	if stack.Status != StatusUpdateRollbackComplete {
		t.Errorf("stack status = %q, want %q", stack.Status, StatusUpdateRollbackComplete)
	}
	deleted := handler.deletedIDs()
	if len(deleted) != 1 || deleted[0] != unstablePhysicalID {
		t.Errorf("rollback deleted %v, want exactly [%s]", deleted, unstablePhysicalID)
	}
	if len(stack.Resources) != 1 || stack.Resources[0].PhysicalID != originalID {
		t.Errorf("restored resources = %+v, want original physical ID %q", stack.Resources, originalID)
	}
}

// End to end: a stack whose resource cannot stabilize rolls back, and the
// rollback deletes the resource that was created.
func TestCreateStack_rollbackDeletesAResourceThatFailedToStabilize(t *testing.T) {
	// Given: a one-resource stack whose resource never settles
	handler, resType := registerUnstable(t)
	p := newProvisionerTestFixture(t)

	stack := &Stack{
		StackName: "unstable-stack",
		StackID:   "arn:aws:cloudformation:us-east-1:000000000000:stack/unstable-stack/1111",
		Region:    "us-east-1",
	}
	tmpl := &Template{Resources: map[string]TemplateResource{
		"R": {Type: resType},
	}}

	// When: the stack is provisioned
	p.provisionStackResources(stack, tmpl)

	// Then: the stack rolls back rather than reporting the failure and walking
	// away from what it built
	if stack.Status != StatusRollbackComplete {
		t.Errorf("stack status = %q, want %q", stack.Status, StatusRollbackComplete)
	}
	deleted := handler.deletedIDs()
	if len(deleted) != 1 || deleted[0] != unstablePhysicalID {
		t.Errorf("rollback deleted %v, want exactly [%s] — a resource that failed to stabilize "+
			"was still created, and skipping it strands it", deleted, unstablePhysicalID)
	}
}

// A resource that never got as far as being created has no physical ID, and
// rollback must not try to delete one.
func TestStackResourceExistsServiceSide_onlyWhenItWasNamed(t *testing.T) {
	cases := []struct {
		name string
		res  StackResource
		want bool
	}{
		{
			name: "created and complete",
			res:  StackResource{Status: ResourceCreateComplete, PhysicalID: "id"},
			want: true,
		},
		{
			// The case this predicate exists for.
			name: "created, then failed to stabilize",
			res:  StackResource{Status: ResourceCreateFailed, PhysicalID: "id"},
			want: true,
		},
		{
			name: "create failed before naming anything",
			res:  StackResource{Status: ResourceCreateFailed},
			want: false,
		},
		{
			name: "already deleted",
			res:  StackResource{Status: ResourceDeleteComplete, PhysicalID: "id"},
			want: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.res.existsServiceSide(); got != tc.want {
				t.Errorf("existsServiceSide() = %v, want %v", got, tc.want)
			}
		})
	}
}
