package cloudformation

// provisioner_ecs_test.go — the rule that decides when an ECS service resource
// is done. Create and update share it, so it is tested once, here, against the
// service shapes each of them sees.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/services/ecs"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

type ecsUpdateRouter struct {
	target string
	body   map[string]any
}

func (r *ecsUpdateRouter) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.target = req.Header.Get("X-Amz-Target")
	if err := json.NewDecoder(req.Body).Decode(&r.body); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	_, _ = w.Write([]byte(`{"service":{}}`))
}

func TestECSServiceUpdate_forceNewDeploymentFromCDK(t *testing.T) {
	// Given: the AWS::ECS::Service shape emitted by CDK when a changed nonce
	// requests a deployment without changing the task definition.
	router := &ecsUpdateRouter{}
	props := map[string]any{
		"ForceNewDeployment": map[string]any{
			"EnableForceNewDeployment": true,
			"ForceNewDeploymentNonce":  "secret-version-2",
		},
	}

	// When: CloudFormation updates the ECS service resource.
	physicalID := "arn:aws:ecs:us-east-1:123456789012:service/app-cluster/app-service"
	gotID, _, err := (&ecsServiceHandler{}).Update(context.Background(), router, nil,
		physicalID, props, nil, &resolveContext{Region: "us-east-1"})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Then: the CloudFormation-only object becomes the ECS API boolean on the
	// same UpdateService path used by aws ecs --force-new-deployment.
	if gotID != physicalID {
		t.Errorf("physical ID = %q, want %q", gotID, physicalID)
	}
	if router.target != "AmazonEC2ContainerServiceV20141113.UpdateService" {
		t.Errorf("target = %q, want ECS UpdateService", router.target)
	}
	if got, ok := router.body["forceNewDeployment"].(bool); !ok || !got {
		t.Errorf("forceNewDeployment = %#v, want true (body %#v)", router.body["forceNewDeployment"], router.body)
	}
	if _, leaked := router.body["forceNewDeploymentNonce"]; leaked {
		t.Errorf("CloudFormation nonce leaked into ECS API request: %#v", router.body)
	}
}

func TestECSServiceStable_deploymentShapes(t *testing.T) {
	// Given: the DescribeServices shapes a service passes through
	cases := []struct {
		name string
		svc  describedECSService
		want bool
	}{
		{
			name: "settled on a single deployment",
			svc: describedECSService{
				DesiredCount: 1, RunningCount: 1,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "COMPLETED"},
				},
			},
			want: true,
		},
		{
			// The instant a container that is about to exit is RUNNING. The
			// counts say the service is there; the deployment says it has not
			// reached a steady state yet, and it is the one that knows.
			name: "single deployment at its desired count but not settled",
			svc: describedECSService{
				DesiredCount: 1, RunningCount: 1,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS"},
				},
			},
			want: false,
		},
		{
			name: "new deployment still placing its tasks",
			svc: describedECSService{
				DesiredCount: 1, RunningCount: 0,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS"},
				},
			},
			want: false,
		},
		{
			// The update shape: the superseded deployment's task is still
			// running, so the service reports its desired count while the new
			// deployment has placed nothing. Counting that as stable is what
			// let a broken update report UPDATE_COMPLETE.
			name: "old deployment still serving while the new one starts",
			svc: describedECSService{
				DesiredCount: 1, RunningCount: 1,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS"},
					{Status: "ACTIVE"},
				},
			},
			want: false,
		},
		{
			name: "old deployment draining after the new one came up",
			svc: describedECSService{
				DesiredCount: 1, RunningCount: 2,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "COMPLETED"},
					{Status: "ACTIVE"},
				},
			},
			want: false,
		},
		{
			name: "service scaled to zero",
			svc: describedECSService{
				DesiredCount: 0, RunningCount: 0,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "COMPLETED"},
				},
			},
			want: true,
		},
		{
			// A CODE_DEPLOY or EXTERNAL controller reports no rolloutState at
			// all, so the counts are all there is to go on.
			name: "non-ECS controller at its desired count",
			svc: describedECSService{
				DesiredCount: 2, RunningCount: 2,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY"},
				},
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the wait asks whether the service has settled
			got := ecsServiceStable(tc.svc)

			// Then: it only says yes once the current deployment is the one running
			if got != tc.want {
				t.Errorf("ecsServiceStable() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestECSServiceRolloutFailure_primaryDeploymentOnly(t *testing.T) {
	// Given: services whose deployments have failed in different ways
	cases := []struct {
		name       string
		svc        describedECSService
		wantFailed bool
		wantReason string
	}{
		{
			name: "healthy rollout in progress",
			svc: describedECSService{
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS"},
				},
			},
			wantFailed: false,
		},
		{
			name: "primary deployment could not place a task",
			svc: describedECSService{
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS", FailedTasks: 1,
						RolloutStateReason: "ECS deployment in progress."},
				},
				Events: []describedECSEvent{
					{Message: fmt.Sprintf(ecs.ServiceEventUnableToPlaceTaskFormat, "s",
						"CannotPullContainerError: no such image")},
				},
			},
			wantFailed: true,
			wantReason: "CannotPullContainerError",
		},
		{
			// Service events are newest first. A replacement may start after
			// the failure and prepend a progress event which must not hide the
			// actionable scheduler reason from CloudFormation.
			name: "newer progress and generic failure events follow placement failure",
			svc: describedECSService{
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS", FailedTasks: 1},
				},
				Events: []describedECSEvent{
					{Message: fmt.Sprintf(ecs.ServiceEventStartedTasksFormat, "s", 1)},
					{Message: fmt.Sprintf(ecs.ServiceEventDeploymentFailedFormat, "s", "ecs-svc/1")},
					{Message: fmt.Sprintf(ecs.ServiceEventUnableToStartConsistentlyFormat, "s")},
					{Message: fmt.Sprintf(ecs.ServiceEventUnableToPlaceTaskFormat, "s",
						"CannotStartContainerError: executable file not found")},
				},
			},
			wantFailed: true,
			wantReason: "CannotStartContainerError",
		},
		{
			// Failed tasks with no circuit breaker and no reason-bearing event:
			// the deployment still carries the rolloutStateReason it was
			// created with, which says the deploy is in progress. Reporting
			// that describes a deploy that is over as though it were running.
			name: "failed tasks with only the in-progress placeholder to quote",
			svc: describedECSService{
				DesiredCount: 1, RunningCount: 0,
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS", FailedTasks: 2,
						RolloutStateReason: "ECS deployment ecs-svc/1 in progress."},
				},
				Events: []describedECSEvent{
					{Message: fmt.Sprintf(ecs.ServiceEventStartedTasksFormat, "s", 1)},
				},
			},
			wantFailed: true,
			wantReason: "2 task(s) failed to stay running; 0 of 1 tasks running",
		},
		{
			name: "circuit breaker failed the primary deployment",
			svc: describedECSService{
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "FAILED",
						RolloutStateReason: "ECS deployment circuit breaker: task failed to start."},
				},
			},
			wantFailed: true,
			wantReason: "circuit breaker",
		},
		{
			// The deployment the update replaced may well have failed tasks on
			// it; only the deployment being rolled out decides the resource.
			name: "superseded deployment carries the failures",
			svc: describedECSService{
				Deployments: []describedECSDeployment{
					{Status: "PRIMARY", RolloutState: "IN_PROGRESS"},
					{Status: "ACTIVE", RolloutState: "FAILED", FailedTasks: 3},
				},
			},
			wantFailed: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When: the wait checks whether to give up
			reason, failed := ecsServiceRolloutFailure(tc.svc)

			// Then: only a failure of the deployment being rolled out counts
			if failed != tc.wantFailed {
				t.Fatalf("ecsServiceRolloutFailure() failed = %v, want %v (reason %q)", failed, tc.wantFailed, reason)
			}
			if tc.wantReason != "" && !strings.Contains(reason, tc.wantReason) {
				t.Errorf("expected a reason mentioning %q, got %q", tc.wantReason, reason)
			}
		})
	}
}

// ─── An update that failed to settle must not be answered by replacement ─────

// stabilizeFailHandler is a resource whose in-place update applies and then
// fails to settle — the shape of an ECS service whose new deployment cannot
// start its tasks. It counts creates so a test can see whether the provisioner
// tried to replace it.
type stabilizeFailHandler struct{ creates int }

func (h *stabilizeFailHandler) Create(context.Context, http.Handler, *config.Config, map[string]any, *resolveContext) (string, map[string]string, error) {
	h.creates++
	return "replacement-id", nil, nil
}

func (h *stabilizeFailHandler) Delete(context.Context, http.Handler, *config.Config, string, *resolveContext) error {
	return nil
}

func (h *stabilizeFailHandler) Update(context.Context, http.Handler, *config.Config, string, map[string]any, map[string]any, *resolveContext) (string, map[string]string, error) {
	return "", nil, failUpdate(errors.New("service svc did not stabilize: CannotPullContainerError"))
}

func TestUpdateResource_updateFailureIsNotReplaced(t *testing.T) {
	// Given: a resource type whose update applies and then settles into failure
	handler := &stabilizeFailHandler{}
	const resType = "Test::Stabilize::Fail"
	resourceHandlers[resType] = handler
	t.Cleanup(func() { delete(resourceHandlers, resType) })

	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	clk := clock.New()
	st := newCFNStore(state.NewMemoryStore(), cfg.Region, clk)
	p := newProvisioner(cfg, st, clk, serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation"))
	p.initRouter(http.NotFoundHandler())
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		p.stop(ctx)
	})

	// When: the stack updates it
	_, err := p.updateResource(context.Background(), "R", TemplateResource{Type: resType},
		map[string]any{}, "old-id", nil, &resolveContext{Region: cfg.Region, StackName: "s"})

	// Then: the resource fails with the reason it gave, and nothing was
	// replaced. Falling back to a create here answers "the new tasks will not
	// start" by building a second copy of the resource, which does not fix it,
	// is not what CloudFormation does, and for a resource whose name
	// CloudFormation generates leaves a stray one behind.
	if err == nil {
		t.Fatal("expected the update to fail")
	}
	if !strings.Contains(err.Error(), "did not stabilize") {
		t.Errorf("expected the handler's own reason to survive, got %v", err)
	}
	if handler.creates != 0 {
		t.Errorf("expected no replacement create, got %d", handler.creates)
	}
}
