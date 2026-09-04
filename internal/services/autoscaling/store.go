package autoscaling

// Persisted Auto Scaling state and the typed accessors over state.Store.
//
// Everything the reconciler needs lives here so that reconcile.go reads as a
// state machine rather than as JSON plumbing. A malformed persisted record is
// skipped on a list and reported as not-found on a point read, never turned
// into a 500 (AGENTS.md § malformed persisted state).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/overcast-sh/overcast/internal/state"
)

// ─── Store namespaces ─────────────────────────────────────────────────────────

const (
	nsGroups     = "autoscaling:groups"
	nsLaunchCfgs = "autoscaling:launchconfigs"
	nsPolicies   = "autoscaling:policies"
	nsHooks      = "autoscaling:hooks"
	nsGroupTags  = "autoscaling:grouptags"
	// nsInstances holds one record per instance the reconciler owns, keyed
	// "<group>/<instance-id>" so a group's instances are a contiguous prefix.
	nsInstances = "autoscaling:instances"
	// nsActivities holds scaling activities, keyed
	// "<group>/<descending-timestamp>/<activity-id>" so DescribeScalingActivities'
	// most-recent-first order is a plain prefix scan with no sort.
	nsActivities = "autoscaling:activities"
)

// ─── Lifecycle states, as AWS spells them ─────────────────────────────────────

const (
	lifecyclePending        = "Pending"
	lifecyclePendingWait    = "Pending:Wait"
	lifecycleInService      = "InService"
	lifecycleTerminating    = "Terminating"
	lifecycleTerminatingWt  = "Terminating:Wait"
	lifecycleTerminated     = "Terminated"
	healthStatusHealthy     = "Healthy"
	healthStatusUnhealthy   = "Unhealthy"
	transitionLaunching     = "autoscaling:EC2_INSTANCE_LAUNCHING"
	transitionTerminating   = "autoscaling:EC2_INSTANCE_TERMINATING"
	lifecycleResultContinue = "CONTINUE"
	lifecycleResultAbandon  = "ABANDON"
)

// awsTimeLayout is the millisecond-precision UTC layout the Auto Scaling
// Query API uses for CreatedTime, StartTime and EndTime.
const awsTimeLayout = "2006-01-02T15:04:05.000Z"

// defaultCooldown is AWS's default DefaultCooldown, in seconds.
const defaultCooldown = 300

// defaultHeartbeatTimeout is AWS's default lifecycle-hook HeartbeatTimeout, in
// seconds.
const defaultHeartbeatTimeout = 3600

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
	VPCZoneIdentifier       string    `json:"VPCZoneIdentifier,omitempty"`
	HealthCheckType         string    `json:"HealthCheckType,omitempty"`
	HealthCheckGracePeriod  int       `json:"HealthCheckGracePeriod,omitempty"`
	TerminationPolicies     []string  `json:"TerminationPolicies,omitempty"`
	CreatedTime             time.Time `json:"CreatedTime"`

	// NewInstancesProtectedFromScaleIn is the group-level default applied to
	// instances as they launch.
	NewInstancesProtectedFromScaleIn bool `json:"NewInstancesProtectedFromScaleIn,omitempty"`

	// Status is set only while the group is being deleted, matching AWS —
	// DescribeAutoScalingGroups omits it for a healthy group.
	Status string `json:"Status,omitempty"`

	// CooldownUntil is when the group's scaling cooldown expires. Zero means
	// no cooldown is in effect.
	CooldownUntil time.Time `json:"CooldownUntil,omitempty"`

	// LaunchTemplate is the EC2 launch template the group launches from, when
	// it was configured with one instead of a launch configuration. Both
	// identifiers are recorded because AWS echoes both back whichever the
	// caller supplied, and Version is kept verbatim: a group pinned to
	// $Latest follows the template, one pinned to a number does not.
	LaunchTemplate *ASGLaunchTemplate `json:"LaunchTemplate,omitempty"`
}

// ASGLaunchTemplate is the launch template specification a group or an
// instance was launched from.
type ASGLaunchTemplate struct {
	LaunchTemplateId   string `json:"LaunchTemplateId,omitempty"`
	LaunchTemplateName string `json:"LaunchTemplateName,omitempty"`
	Version            string `json:"Version,omitempty"`
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

// StepAdjustment is one interval of a step-scaling policy.
type StepAdjustment struct {
	MetricIntervalLowerBound *float64 `json:"MetricIntervalLowerBound,omitempty"`
	MetricIntervalUpperBound *float64 `json:"MetricIntervalUpperBound,omitempty"`
	ScalingAdjustment        int      `json:"ScalingAdjustment"`
}

// ScalingPolicy represents a scaling policy this emulator can execute.
type ScalingPolicy struct {
	PolicyARN               string           `json:"PolicyARN"`
	PolicyName              string           `json:"PolicyName"`
	AutoScalingGroupName    string           `json:"AutoScalingGroupName"`
	PolicyType              string           `json:"PolicyType"`
	AdjustmentType          string           `json:"AdjustmentType"`
	ScalingAdjustment       int              `json:"ScalingAdjustment"`
	MinAdjustmentMagnitude  int              `json:"MinAdjustmentMagnitude,omitempty"`
	Cooldown                int              `json:"Cooldown"`
	StepAdjustments         []StepAdjustment `json:"StepAdjustments,omitempty"`
	MetricAggregationType   string           `json:"MetricAggregationType,omitempty"`
	EstimatedInstanceWarmup int              `json:"EstimatedInstanceWarmup,omitempty"`
}

// LifecycleHook represents a lifecycle hook.
type LifecycleHook struct {
	LifecycleHookName     string `json:"LifecycleHookName"`
	AutoScalingGroupName  string `json:"AutoScalingGroupName"`
	LifecycleTransition   string `json:"LifecycleTransition"`
	DefaultResult         string `json:"DefaultResult"`
	HeartbeatTimeout      int    `json:"HeartbeatTimeout"`
	GlobalTimeout         int    `json:"GlobalTimeout"`
	NotificationTargetARN string `json:"NotificationTargetARN,omitempty"`
	RoleARN               string `json:"RoleARN,omitempty"`
}

// GroupTag represents a tag on an Auto Scaling group.
type GroupTag struct {
	ResourceId        string `json:"ResourceId"`
	ResourceType      string `json:"ResourceType"`
	Key               string `json:"Key"`
	Value             string `json:"Value"`
	PropagateAtLaunch bool   `json:"PropagateAtLaunch"`
}

// ASGInstance is one EC2 instance the reconciler owns on behalf of a group.
type ASGInstance struct {
	InstanceId              string `json:"InstanceId"`
	AutoScalingGroupName    string `json:"AutoScalingGroupName"`
	InstanceType            string `json:"InstanceType"`
	AvailabilityZone        string `json:"AvailabilityZone"`
	LifecycleState          string `json:"LifecycleState"`
	HealthStatus            string `json:"HealthStatus"`
	LaunchConfigurationName string `json:"LaunchConfigurationName,omitempty"`
	ProtectedFromScaleIn    bool   `json:"ProtectedFromScaleIn"`

	// LaunchTemplate is set instead of LaunchConfigurationName when the
	// instance was launched from a template, which is what AWS reports on the
	// instance too.
	LaunchTemplate *ASGLaunchTemplate `json:"LaunchTemplate,omitempty"`

	// LaunchedAt orders scale-in candidates (oldest first, AWS's
	// OldestInstance policy).
	LaunchedAt time.Time `json:"LaunchedAt"`

	// PendingHookName, LifecycleActionToken and HookDeadline are set while the
	// instance is parked in a *:Wait state by a lifecycle hook.
	PendingHookName      string    `json:"PendingHookName,omitempty"`
	LifecycleActionToken string    `json:"LifecycleActionToken,omitempty"`
	HookDeadline         time.Time `json:"HookDeadline,omitempty"`
	// HookDefaultResult is the hook's DefaultResult, applied when the
	// heartbeat times out.
	HookDefaultResult string `json:"HookDefaultResult,omitempty"`

	// LaunchActivityId and TerminateActivityId link the instance to the
	// scaling activity that created or is removing it, so the activity can be
	// completed when the lifecycle transition finishes.
	LaunchActivityId    string `json:"LaunchActivityId,omitempty"`
	TerminateActivityId string `json:"TerminateActivityId,omitempty"`
}

// waiting reports whether the instance is parked by a lifecycle hook.
func (i *ASGInstance) waiting() bool {
	return i.LifecycleState == lifecyclePendingWait || i.LifecycleState == lifecycleTerminatingWt
}

// counted reports whether the instance counts towards the group's capacity.
// AWS counts everything that is not on its way out.
func (i *ASGInstance) counted() bool {
	switch i.LifecycleState {
	case lifecycleTerminating, lifecycleTerminatingWt, lifecycleTerminated:
		return false
	default:
		return true
	}
}

// Activity is one scaling activity, as DescribeScalingActivities reports it.
type Activity struct {
	ActivityId           string    `json:"ActivityId"`
	AutoScalingGroupName string    `json:"AutoScalingGroupName"`
	AutoScalingGroupARN  string    `json:"AutoScalingGroupARN"`
	Description          string    `json:"Description"`
	Cause                string    `json:"Cause"`
	StartTime            time.Time `json:"StartTime"`
	EndTime              time.Time `json:"EndTime,omitempty"`
	StatusCode           string    `json:"StatusCode"`
	StatusMessage        string    `json:"StatusMessage,omitempty"`
	Progress             int       `json:"Progress"`
	Details              string    `json:"Details,omitempty"`
}

// activitiesRetained caps how many activities are kept per group. Real Auto
// Scaling retains activities for six weeks; Overcast bounds by count instead,
// because the value here is "show what the reconciler did", not historical
// analysis, and an unbounded namespace is a growth leak.
const activitiesRetained = 200

// ─── Store ────────────────────────────────────────────────────────────────────

// asgStore is the typed layer over state.Store.
type asgStore struct{ store state.Store }

func newASGStore(st state.Store) *asgStore { return &asgStore{store: st} }

func putJSON(ctx context.Context, st state.Store, ns, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return st.Set(ctx, ns, key, string(raw))
}

// ── Groups ──

func (s *asgStore) putGroup(ctx context.Context, g *AutoScalingGroup) error {
	return putJSON(ctx, s.store, nsGroups, g.AutoScalingGroupName, g)
}

func (s *asgStore) getGroup(ctx context.Context, name string) (*AutoScalingGroup, bool) {
	raw, found, err := s.store.Get(ctx, nsGroups, name)
	if err != nil || !found {
		return nil, false
	}
	var g AutoScalingGroup
	if json.Unmarshal([]byte(raw), &g) != nil {
		return nil, false
	}
	return &g, true
}

func (s *asgStore) listGroups(ctx context.Context) ([]*AutoScalingGroup, error) {
	pairs, err := s.store.Scan(ctx, nsGroups, "")
	if err != nil {
		return nil, err
	}
	out := make([]*AutoScalingGroup, 0, len(pairs))
	for _, kv := range pairs {
		var g AutoScalingGroup
		if json.Unmarshal([]byte(kv.Value), &g) != nil {
			continue
		}
		out = append(out, &g)
	}
	return out, nil
}

func (s *asgStore) deleteGroup(ctx context.Context, name string) {
	_ = s.store.Delete(ctx, nsGroups, name)
}

// ── Launch configurations ──

func (s *asgStore) getLaunchConfig(ctx context.Context, name string) (*LaunchConfiguration, bool) {
	raw, found, err := s.store.Get(ctx, nsLaunchCfgs, name)
	if err != nil || !found {
		return nil, false
	}
	var lc LaunchConfiguration
	if json.Unmarshal([]byte(raw), &lc) != nil {
		return nil, false
	}
	return &lc, true
}

// ── Instances ──

func instanceKey(group, instanceID string) string { return group + "/" + instanceID }

func (s *asgStore) putInstance(ctx context.Context, inst *ASGInstance) error {
	return putJSON(ctx, s.store, nsInstances, instanceKey(inst.AutoScalingGroupName, inst.InstanceId), inst)
}

// getInstance returns one instance of a named group.
func (s *asgStore) getInstance(ctx context.Context, group, instanceID string) (*ASGInstance, bool) {
	raw, found, err := s.store.Get(ctx, nsInstances, instanceKey(group, instanceID))
	if err != nil || !found {
		return nil, false
	}
	var inst ASGInstance
	if json.Unmarshal([]byte(raw), &inst) != nil {
		return nil, false
	}
	return &inst, true
}

func (s *asgStore) deleteInstance(ctx context.Context, group, instanceID string) {
	_ = s.store.Delete(ctx, nsInstances, instanceKey(group, instanceID))
}

// listInstances returns one group's instances, ordered by instance id so the
// reconciler's decisions are deterministic.
func (s *asgStore) listInstances(ctx context.Context, group string) ([]*ASGInstance, error) {
	prefix := ""
	if group != "" {
		prefix = group + "/"
	}
	pairs, err := s.store.Scan(ctx, nsInstances, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*ASGInstance, 0, len(pairs))
	for _, kv := range pairs {
		var inst ASGInstance
		if json.Unmarshal([]byte(kv.Value), &inst) != nil {
			continue
		}
		out = append(out, &inst)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceId < out[j].InstanceId })
	return out, nil
}

// findInstance locates an instance by id across every group — the shape
// SetInstanceHealth and SetInstanceProtection need, which name an instance
// without necessarily naming its group.
func (s *asgStore) findInstance(ctx context.Context, instanceID string) (*ASGInstance, bool) {
	pairs, err := s.store.Scan(ctx, nsInstances, "")
	if err != nil {
		return nil, false
	}
	suffix := "/" + instanceID
	for _, kv := range pairs {
		if !strings.HasSuffix(kv.Key, suffix) {
			continue
		}
		var inst ASGInstance
		if json.Unmarshal([]byte(kv.Value), &inst) != nil {
			continue
		}
		return &inst, true
	}
	return nil, false
}

// ── Policies ──

func policyKey(group, policy string) string { return group + "/" + policy }

func (s *asgStore) putPolicy(ctx context.Context, p *ScalingPolicy) error {
	return putJSON(ctx, s.store, nsPolicies, policyKey(p.AutoScalingGroupName, p.PolicyName), p)
}

func (s *asgStore) getPolicy(ctx context.Context, group, policy string) (*ScalingPolicy, bool) {
	raw, found, err := s.store.Get(ctx, nsPolicies, policyKey(group, policy))
	if err != nil || !found {
		return nil, false
	}
	var p ScalingPolicy
	if json.Unmarshal([]byte(raw), &p) != nil {
		return nil, false
	}
	return &p, true
}

func (s *asgStore) listPolicies(ctx context.Context, group string) ([]*ScalingPolicy, error) {
	prefix := ""
	if group != "" {
		prefix = group + "/"
	}
	pairs, err := s.store.Scan(ctx, nsPolicies, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*ScalingPolicy, 0, len(pairs))
	for _, kv := range pairs {
		var p ScalingPolicy
		if json.Unmarshal([]byte(kv.Value), &p) != nil {
			continue
		}
		out = append(out, &p)
	}
	return out, nil
}

// ── Lifecycle hooks ──

func (s *asgStore) listHooks(ctx context.Context, group string) ([]*LifecycleHook, error) {
	prefix := ""
	if group != "" {
		prefix = group + "/"
	}
	pairs, err := s.store.Scan(ctx, nsHooks, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*LifecycleHook, 0, len(pairs))
	for _, kv := range pairs {
		var h LifecycleHook
		if json.Unmarshal([]byte(kv.Value), &h) != nil {
			continue
		}
		out = append(out, &h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LifecycleHookName < out[j].LifecycleHookName })
	return out, nil
}

// hookByName returns one named hook on a group.
func (s *asgStore) hookByName(ctx context.Context, group, name string) (*LifecycleHook, bool) {
	raw, found, err := s.store.Get(ctx, nsHooks, group+"/"+name)
	if err != nil || !found {
		return nil, false
	}
	var h LifecycleHook
	if json.Unmarshal([]byte(raw), &h) != nil {
		return nil, false
	}
	return &h, true
}

// hookFor returns the first hook on group watching transition, if any. AWS
// runs every matching hook; Overcast parks the instance on one at a time,
// which is the observable difference only when two hooks watch the same
// transition — documented in docs/services/autoscaling.md.
func (s *asgStore) hookFor(ctx context.Context, group, transition string) (*LifecycleHook, bool) {
	hooks, err := s.listHooks(ctx, group)
	if err != nil {
		return nil, false
	}
	for _, h := range hooks {
		if h.LifecycleTransition == transition {
			return h, true
		}
	}
	return nil, false
}

// ── Tags ──

// listTagsForGroup returns a group's tags, or every tag when group is empty.
func (s *asgStore) listTagsForGroup(ctx context.Context, group string) ([]*GroupTag, error) {
	prefix := ""
	if group != "" {
		prefix = group + "/"
	}
	pairs, err := s.store.Scan(ctx, nsGroupTags, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*GroupTag, 0, len(pairs))
	for _, kv := range pairs {
		var t GroupTag
		if json.Unmarshal([]byte(kv.Value), &t) != nil {
			continue
		}
		out = append(out, &t)
	}
	return out, nil
}

// ── Activities ──

// activityKey sorts a group's activities newest-first by inverting the
// timestamp, so DescribeScalingActivities is a plain prefix scan.
func activityKey(group string, ts time.Time, id string) string {
	inverted := int64(^uint64(0)>>1) - ts.UTC().UnixNano()
	return fmt.Sprintf("%s/%019d/%s", group, inverted, id)
}

func (s *asgStore) putActivity(ctx context.Context, a *Activity) error {
	if err := putJSON(ctx, s.store, nsActivities, activityKey(a.AutoScalingGroupName, a.StartTime, a.ActivityId), a); err != nil {
		return err
	}
	pairs, err := s.store.Scan(ctx, nsActivities, a.AutoScalingGroupName+"/")
	if err != nil {
		return nil // trimming is best-effort; the activity is already stored
	}
	for i := activitiesRetained; i < len(pairs); i++ {
		_ = s.store.Delete(ctx, nsActivities, pairs[i].Key)
	}
	return nil
}

// listActivities returns a group's activities, newest first.
func (s *asgStore) listActivities(ctx context.Context, group string) ([]*Activity, error) {
	prefix := ""
	if group != "" {
		prefix = group + "/"
	}
	pairs, err := s.store.Scan(ctx, nsActivities, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]*Activity, 0, len(pairs))
	for _, kv := range pairs {
		var a Activity
		if json.Unmarshal([]byte(kv.Value), &a) != nil {
			continue
		}
		out = append(out, &a)
	}
	if group == "" {
		sort.SliceStable(out, func(i, j int) bool { return out[i].StartTime.After(out[j].StartTime) })
	}
	return out, nil
}

// deleteGroupChildren removes every instance and activity record belonging to
// a deleted group, so a delete leaves nothing behind.
func (s *asgStore) deleteGroupChildren(ctx context.Context, group string) {
	for _, ns := range []string{nsInstances, nsActivities, nsHooks, nsPolicies} {
		pairs, err := s.store.Scan(ctx, ns, group+"/")
		if err != nil {
			continue
		}
		for _, kv := range pairs {
			_ = s.store.Delete(ctx, ns, kv.Key)
		}
	}
}
