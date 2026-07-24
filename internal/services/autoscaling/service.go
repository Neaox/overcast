// Package autoscaling provides metadata-level emulation of Amazon EC2 Auto Scaling.
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
//	  PutScalingPolicy, DescribePolicies, DeletePolicy
//
//	Lifecycle Hooks:
//	  PutLifecycleHook, DescribeLifecycleHooks, DeleteLifecycleHook
//
//	Tags:
//	  CreateOrUpdateTags, DeleteTags, DescribeTags
//
//	Instances (metadata-only):
//	  DescribeAutoScalingInstances
//
// All operations are metadata-only: no instances are launched, no scaling
// actions are executed, and no CloudWatch alarms are evaluated. This is
// sufficient to unblock CDK/Terraform stacks that reference ASG resources.
package autoscaling

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
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

// ─── Store namespaces ─────────────────────────────────────────────────────────

const (
	nsGroups     = "autoscaling:groups"
	nsLaunchCfgs = "autoscaling:launchconfigs"
	nsPolicies   = "autoscaling:policies"
	nsHooks      = "autoscaling:hooks"
	nsGroupTags  = "autoscaling:grouptags"
)

// ─── Types ────────────────────────────────────────────────────────────────────

// AutoScalingGroup represents an Auto Scaling group.
//
//nolint:revive // AWS names the resource AutoScalingGroup.
type AutoScalingGroup struct {
	AutoScalingGroupName    string    `json:"AutoScalingGroupName"`
	AutoScalingGroupARN     string    `json:"AutoScalingGroupARN"`
	LaunchConfigurationName string    `json:"LaunchConfigurationName,omitempty"`
	MinSize                 int       `json:"MinSize"`
	MaxSize                 int       `json:"MaxSize"`
	DesiredCapacity         int       `json:"DesiredCapacity"`
	DefaultCooldown         int       `json:"DefaultCooldown"`
	AvailabilityZones       []string  `json:"AvailabilityZones"`
	Status                  string    `json:"Status"`
	CreatedTime             time.Time `json:"CreatedTime"`
}

// LaunchConfiguration represents an EC2 Auto Scaling launch configuration.
type LaunchConfiguration struct {
	LaunchConfigurationName string    `json:"LaunchConfigurationName"`
	LaunchConfigurationARN  string    `json:"LaunchConfigurationARN"`
	ImageId                 string    `json:"ImageId"`
	InstanceType            string    `json:"InstanceType"`
	KeyName                 string    `json:"KeyName,omitempty"`
	SecurityGroups          []string  `json:"SecurityGroups,omitempty"`
	IamInstanceProfile      string    `json:"IamInstanceProfile,omitempty"`
	UserData                string    `json:"UserData,omitempty"`
	CreatedTime             time.Time `json:"CreatedTime"`
}

// ScalingPolicy represents a scaling policy.
type ScalingPolicy struct {
	PolicyARN            string `json:"PolicyARN"`
	PolicyName           string `json:"PolicyName"`
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
	PolicyType           string `json:"PolicyType"`
	AdjustmentType       string `json:"AdjustmentType"`
	ScalingAdjustment    int    `json:"ScalingAdjustment"`
	Cooldown             int    `json:"Cooldown"`
}

// LifecycleHook represents a lifecycle hook.
type LifecycleHook struct {
	LifecycleHookName    string `json:"LifecycleHookName"`
	AutoScalingGroupName string `json:"AutoScalingGroupName"`
	LifecycleTransition  string `json:"LifecycleTransition"`
	DefaultResult        string `json:"DefaultResult"`
	HeartbeatTimeout     int    `json:"HeartbeatTimeout"`
}

// GroupTag represents a tag on an Auto Scaling group.
type GroupTag struct {
	ResourceId        string `json:"ResourceId"`
	ResourceType      string `json:"ResourceType"`
	Key               string `json:"Key"`
	Value             string `json:"Value"`
	PropagateAtLaunch bool   `json:"PropagateAtLaunch"`
}

// ─── Service ─────────────────────────────────────────────────────────────────

// Service implements router.Service and router.QueryDispatcher for Auto Scaling.
type Service struct {
	cfg     *config.Config
	store   state.Store
	log     *serviceutil.ServiceLogger
	clk     clock.Clock
	handler *Handler
}

// New returns a configured Auto Scaling Service.
func New(cfg *config.Config, st state.Store, logger *zap.Logger, clk clock.Clock) *Service {
	log := serviceutil.NewServiceLogger(logger, serviceName)
	return &Service{
		cfg:     cfg,
		store:   st,
		log:     log,
		clk:     clk,
		handler: newHandler(cfg, st, log, clk),
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
	if c, opName := codec.FromContext(r.Context()); c != nil && opName != "" {
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
		// No typed impl for this op — fall through to legacy dispatch below.
	}
	s.handler.dispatch(w, r)
}

// ─── Package-level helpers ────────────────────────────────────────────────────

func parseInt(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func parseBool(s string) bool {
	b, _ := strconv.ParseBool(s)
	return b
}

// parseIndexedStrings extracts "Param.member.N" values (1-based) from form data.
func parseIndexedStrings(r *http.Request, prefix string) []string {
	var result []string
	for i := 1; ; i++ {
		v := r.FormValue(fmt.Sprintf("%s.member.%d", prefix, i))
		if v == "" {
			break
		}
		result = append(result, v)
	}
	return result
}
