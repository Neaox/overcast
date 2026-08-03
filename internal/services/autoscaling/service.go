// Package autoscaling emulates Amazon EC2 Auto Scaling, including the
// reconciliation loop that makes a group's DesiredCapacity mean something.
//
// Implemented operations (Query protocol, XML responses):
//
//	Auto Scaling Groups:
//	  CreateAutoScalingGroup, UpdateAutoScalingGroup,
//	  DescribeAutoScalingGroups, DeleteAutoScalingGroup,
//	  SetDesiredCapacity, TerminateInstanceInAutoScalingGroup
//
//	Launch Configurations:
//	  CreateLaunchConfiguration, DescribeLaunchConfigurations, DeleteLaunchConfiguration
//
//	Scaling Policies:
//	  PutScalingPolicy, DescribePolicies, DeletePolicy, ExecutePolicy
//
//	Lifecycle Hooks:
//	  PutLifecycleHook, DescribeLifecycleHooks, DeleteLifecycleHook,
//	  CompleteLifecycleAction, RecordLifecycleActionHeartbeat
//
//	Instances & activities:
//	  DescribeAutoScalingInstances, DescribeScalingActivities,
//	  SetInstanceHealth, SetInstanceProtection
//
//	Tags:
//	  CreateOrUpdateTags, DeleteTags, DescribeTags
//
// A group backed by a launch configuration really converges: a single
// background reconciler launches and terminates EC2 instances through the
// emulator's own router until the owned instance set matches DesiredCapacity,
// runs the Pending/InService/Terminating lifecycle state machine, honours
// lifecycle hooks, and records a scaling activity for every launch and
// termination. Group shapes it cannot converge — launch templates, mixed
// instances policies, launch-from-instance — and scaling policy types it
// cannot execute — target tracking, predictive — are refused with a 501 at the
// configuring operation rather than stored and silently ignored.
package autoscaling

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/awsapi"
	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

const serviceName = "autoscaling"

const asXMLNS = "http://autoscaling.amazonaws.com/doc/2011-01-01/"

// ─── Service ─────────────────────────────────────────────────────────────────

// Service implements router.Service and router.QueryDispatcher for Auto Scaling.
type Service struct {
	cfg     *config.Config
	st      *asgStore
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	handler *Handler

	// router is the emulator's root handler, used to launch and terminate EC2
	// instances and to publish lifecycle events. Nil until InitRouter is
	// called, which keeps the service usable in unit tests that wire none.
	router http.Handler

	// ticker drives the single reconciliation loop. Created in New so a mock
	// clock has it registered before New returns.
	ticker *clock.Ticker

	// wake lets a handler ask for a pass now rather than on the next tick.
	// Buffered to one: a pending wake already covers a second request.
	wake chan struct{}

	// groupCount caches how many groups exist, so a tick with none costs one
	// atomic load and no store I/O. -1 means "unknown, re-read next tick".
	groupCount atomic.Int64

	// mu guards the read-modify-write of a group's own records. It is never
	// held across an EC2 or EventBridge call.
	mu sync.Mutex

	stopOnce sync.Once
	stopCh   chan struct{}
	wg       sync.WaitGroup
}

// New returns a configured Auto Scaling Service.
//
// It performs no store I/O: the reconciler only registers its ticker here and
// does not touch the store until its first pass (AGENTS.md § startup budget).
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	s := &Service{
		cfg:    cfg,
		st:     newASGStore(st),
		log:    log,
		clk:    clk,
		wake:   make(chan struct{}, 1),
		stopCh: make(chan struct{}),
	}
	s.handler = newHandler(s)
	// -1 = unknown: the first pass learns the real count from the store, after
	// construction, so a restart with existing groups still reconciles.
	s.groupCount.Store(-1)
	s.ticker = s.clk.Ticker(reconcileInterval)
	s.wg.Add(1)
	go s.runReconciler()
	return s
}

// InitRouter wires the emulator's root handler. Called once from router.New;
// a Service without it still serves the API, it just has nowhere to launch
// instances — every launch is then recorded as a Failed scaling activity
// rather than silently skipped.
func (s *Service) InitRouter(r http.Handler) { s.router = r }

// Stop drains the reconciliation loop.
func (s *Service) Stop(ctx context.Context) {
	s.stopOnce.Do(func() { close(s.stopCh) })
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}

// Name satisfies router.Service.
func (s *Service) Name() string { return serviceName }

// RegisterRoutes satisfies router.Service. All operations go through DispatchQuery.
func (s *Service) RegisterRoutes(_ chi.Router) {}

// OwnsVersion satisfies router.QueryVersionOwner.
func (s *Service) OwnsVersion(v string) bool { return v == awsapi.VersionAutoScaling }

// DispatchQuery satisfies router.QueryDispatcher.
func (s *Service) DispatchQuery(w http.ResponseWriter, r *http.Request) {
	c, opName := codec.FromContext(r.Context())
	if c != nil && opName != "" {
		if !serviceutil.AllowProtocolDrift(s.cfg, s.log, opName, c, s.SupportedProtocols()) {
			c.WriteError(w, r, &protocol.AWSError{
				Code: "UnsupportedProtocol", Message: "AutoScaling does not support wire protocol " + c.Name() + ".",
				HTTPStatus: http.StatusUnsupportedMediaType,
			})
			return
		}
		if typed, ok := s.handler.typedOp[opName]; ok {
			typed.Invoke(w, r, c)
			return
		}
	}
	// A caller that reached the service without the protocol middleware (an
	// internal dispatch, a bare unit-test request) still resolves its
	// operation from the form and runs the same typed implementation — there
	// is deliberately no second, divergent copy of the handlers.
	s.handler.dispatch(w, r)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (s *Service) accountID() string {
	if s.cfg != nil && s.cfg.AccountID != "" {
		return s.cfg.AccountID
	}
	return "000000000000"
}

func (s *Service) region() string {
	if s.cfg != nil && s.cfg.Region != "" {
		return s.cfg.Region
	}
	return "us-east-1"
}

func (s *Service) asgARN(name string) string {
	return "arn:aws:autoscaling:" + s.region() + ":" + s.accountID() +
		":autoScalingGroup:" + newID() + ":autoScalingGroupName/" + name
}

func (s *Service) lcARN(name string) string {
	return "arn:aws:autoscaling:" + s.region() + ":" + s.accountID() +
		":launchConfiguration:" + newID() + ":launchConfigurationName/" + name
}

func (s *Service) policyARN(asgName, policyName string) string {
	return "arn:aws:autoscaling:" + s.region() + ":" + s.accountID() +
		":scalingPolicy:" + newID() + ":autoScalingGroupName/" + asgName + ":policyName/" + policyName
}

// parsePolicyARN splits an Auto Scaling scaling-policy ARN back into the group
// and policy it names. This is what makes an alarm action deliverable: the
// alarm carries only the ARN.
func parsePolicyARN(arn string) (group, policy string, ok bool) {
	idx := strings.Index(arn, ":autoScalingGroupName/")
	if idx < 0 {
		return "", "", false
	}
	rest := arn[idx+len(":autoScalingGroupName/"):]
	group, policy, found := strings.Cut(rest, ":policyName/")
	if !found || group == "" || policy == "" {
		return "", "", false
	}
	return group, policy, true
}

// newID returns a fresh UUID, the identifier shape Auto Scaling uses for
// activity ids and the resource segment of its ARNs.
func newID() string { return uuid.NewString() }
