package autoscaling

// Scaling-policy validation, arithmetic, and the CloudWatch alarm seam.

import (
	"net/http"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/alarmaction"
)

func TestValidatePolicyInput_refusesPolicyTypesItCannotExecute(t *testing.T) {
	cases := []struct {
		name       string
		req        putScalingPolicyReq
		wantStatus int
		wantCode   string
	}{
		{
			name:       "target tracking is refused as unsupported",
			req:        putScalingPolicyReq{PolicyType: policyTargetTracking},
			wantStatus: http.StatusNotImplemented, wantCode: "NotImplemented",
		},
		{
			name:       "predictive scaling is refused as unsupported",
			req:        putScalingPolicyReq{PolicyType: policyPredictive},
			wantStatus: http.StatusNotImplemented, wantCode: "NotImplemented",
		},
		{
			name:       "an unknown policy type is AWS's own validation error",
			req:        putScalingPolicyReq{PolicyType: "MagicScaling"},
			wantStatus: http.StatusBadRequest, wantCode: "ValidationError",
		},
		{
			name:       "an unknown adjustment type is AWS's own validation error",
			req:        putScalingPolicyReq{AdjustmentType: "Sideways"},
			wantStatus: http.StatusBadRequest, wantCode: "ValidationError",
		},
		{
			name:       "step scaling without steps is refused",
			req:        putScalingPolicyReq{PolicyType: policyStepScaling, AdjustmentType: adjustmentChangeInCapacity},
			wantStatus: http.StatusBadRequest, wantCode: "ValidationError",
		},
		{
			name: "step scaling with a cooldown is refused, as on AWS",
			req: putScalingPolicyReq{
				PolicyType: policyStepScaling, AdjustmentType: adjustmentChangeInCapacity, Cooldown: 60,
				StepAdjustments: []stepAdjustmentMember{{ScalingAdjustment: 1}},
			},
			wantStatus: http.StatusBadRequest, wantCode: "ValidationError",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			aerr := validatePolicyInput(&tc.req)
			if aerr == nil {
				t.Fatal("policy was accepted")
			}
			if aerr.HTTPStatus != tc.wantStatus {
				t.Errorf("status = %d, want %d (%s)", aerr.HTTPStatus, tc.wantStatus, aerr.Message)
			}
			if aerr.Code != tc.wantCode {
				t.Errorf("code = %q, want %q", aerr.Code, tc.wantCode)
			}
		})
	}
}

func TestValidatePolicyInput_acceptsExecutablePolicies(t *testing.T) {
	cases := []putScalingPolicyReq{
		{AdjustmentType: adjustmentChangeInCapacity, ScalingAdjustment: 2},
		{PolicyType: policySimpleScaling, AdjustmentType: adjustmentExactCapacity, ScalingAdjustment: 4},
		{
			PolicyType: policyStepScaling, AdjustmentType: adjustmentPercentChangeInCapacity,
			StepAdjustments: []stepAdjustmentMember{{MetricIntervalLowerBound: "0", ScalingAdjustment: 30}},
		},
	}
	for _, req := range cases {
		if aerr := validatePolicyInput(&req); aerr != nil {
			t.Errorf("%+v was refused: %s", req, aerr.Message)
		}
	}
}

func TestApplyAdjustment(t *testing.T) {
	cases := []struct {
		name                         string
		adjustmentType               string
		current, adjustment, minMagn int
		want                         int
	}{
		{"change in capacity", adjustmentChangeInCapacity, 3, 2, 0, 5},
		{"negative change", adjustmentChangeInCapacity, 3, -2, 0, 1},
		{"exact capacity", adjustmentExactCapacity, 3, 7, 0, 7},
		{"percent rounds towards zero", adjustmentPercentChangeInCapacity, 10, 25, 0, 12},
		{"percent honours MinAdjustmentMagnitude", adjustmentPercentChangeInCapacity, 4, 10, 2, 6},
		{"negative percent honours MinAdjustmentMagnitude", adjustmentPercentChangeInCapacity, 4, -10, 2, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyAdjustment(tc.adjustmentType, tc.current, tc.adjustment, tc.minMagn); got != tc.want {
				t.Errorf("= %d, want %d", got, tc.want)
			}
		})
	}
}

func TestPolicyAdjustment_stepSelection(t *testing.T) {
	lower := func(v float64) *float64 { return &v }
	policy := &ScalingPolicy{
		PolicyType: policyStepScaling,
		StepAdjustments: []StepAdjustment{
			{MetricIntervalLowerBound: lower(0), MetricIntervalUpperBound: lower(10), ScalingAdjustment: 1},
			{MetricIntervalLowerBound: lower(10), ScalingAdjustment: 3},
		},
	}

	if got, ok := policyAdjustment(policy, 5); !ok || got != 1 {
		t.Errorf("breach 5 selected %d (ok=%v), want 1", got, ok)
	}
	if got, ok := policyAdjustment(policy, 25); !ok || got != 3 {
		t.Errorf("breach 25 selected %d (ok=%v), want 3", got, ok)
	}
	// A breach below every interval matches nothing, so nothing happens —
	// rather than silently applying the nearest step.
	if _, ok := policyAdjustment(policy, -5); ok {
		t.Error("breach -5 matched a step it does not fall in")
	}

	// Simple scaling always applies its single adjustment.
	simple := &ScalingPolicy{PolicyType: policySimpleScaling, ScalingAdjustment: 2}
	if got, ok := policyAdjustment(simple, -100); !ok || got != 2 {
		t.Errorf("simple scaling selected %d (ok=%v), want 2", got, ok)
	}
}

func TestExecutePolicy_honoursCooldown(t *testing.T) {
	// Given: a converged group with a +1 simple-scaling policy and a cooldown
	f := newFixture(t)
	f.group(t, "g", 0, 10, 1)
	f.converge(t)
	policy := &ScalingPolicy{
		PolicyARN: f.svc.policyARN("g", "out"), PolicyName: "out", AutoScalingGroupName: "g",
		PolicyType: policySimpleScaling, AdjustmentType: adjustmentChangeInCapacity,
		ScalingAdjustment: 1, Cooldown: 300,
	}
	if err := f.svc.st.putPolicy(f.ctx, policy); err != nil {
		t.Fatalf("putPolicy: %v", err)
	}

	// When: it runs once
	if aerr := f.svc.executePolicy(f.ctx, policy, 0, "", true); aerr != nil {
		t.Fatalf("executePolicy: %v", aerr)
	}
	g, _ := f.svc.st.getGroup(f.ctx, "g")
	if g.DesiredCapacity != 2 {
		t.Fatalf("desired capacity = %d, want 2", g.DesiredCapacity)
	}

	// And again immediately
	if aerr := f.svc.executePolicy(f.ctx, policy, 0, "", true); aerr != nil {
		t.Fatalf("executePolicy: %v", aerr)
	}

	// Then: the cooldown suppressed the second run
	g, _ = f.svc.st.getGroup(f.ctx, "g")
	if g.DesiredCapacity != 2 {
		t.Errorf("desired capacity = %d after a cooled-down run, want 2", g.DesiredCapacity)
	}

	// When: the cooldown expires
	f.mock.Add(301 * time.Second)
	if aerr := f.svc.executePolicy(f.ctx, policy, 0, "", true); aerr != nil {
		t.Fatalf("executePolicy: %v", aerr)
	}

	// Then: it scales again
	g, _ = f.svc.st.getGroup(f.ctx, "g")
	if g.DesiredCapacity != 3 {
		t.Errorf("desired capacity = %d after the cooldown, want 3", g.DesiredCapacity)
	}
}

func TestExecutePolicy_clampsToGroupBounds(t *testing.T) {
	// Given: a group at its maximum with a +5 policy
	f := newFixture(t)
	f.group(t, "g", 0, 2, 2)
	f.converge(t)
	policy := &ScalingPolicy{
		PolicyName: "out", AutoScalingGroupName: "g", PolicyType: policySimpleScaling,
		AdjustmentType: adjustmentChangeInCapacity, ScalingAdjustment: 5,
	}

	// When: it runs
	if aerr := f.svc.executePolicy(f.ctx, policy, 0, "", false); aerr != nil {
		t.Fatalf("executePolicy: %v", aerr)
	}
	f.converge(t)

	// Then: the group stays at MaxSize rather than overshooting
	g, _ := f.svc.st.getGroup(f.ctx, "g")
	if g.DesiredCapacity != 2 {
		t.Errorf("desired capacity = %d, want 2 (MaxSize)", g.DesiredCapacity)
	}
	if got := f.instances(t, "g"); len(got) != 2 {
		t.Errorf("instance count = %d, want 2", len(got))
	}
}

// ─── Alarm seam ───────────────────────────────────────────────────────────────

func TestHandleAlarmAction_executesTheNamedPolicy(t *testing.T) {
	// Given: a converged group with a step-scaling policy an alarm names
	f := newFixture(t)
	f.group(t, "g", 1, 10, 1)
	f.converge(t)
	resp, aerr := f.svc.handler.putScalingPolicyTyped(f.ctx, &putScalingPolicyReq{
		AutoScalingGroupName: "g", PolicyName: "cpu-high", PolicyType: policyStepScaling,
		AdjustmentType: adjustmentChangeInCapacity,
		StepAdjustments: []stepAdjustmentMember{
			{MetricIntervalLowerBound: "0", MetricIntervalUpperBound: "20", ScalingAdjustment: 1},
			{MetricIntervalLowerBound: "20", ScalingAdjustment: 4},
		},
	})
	if aerr != nil {
		t.Fatalf("PutScalingPolicy: %v", aerr)
	}

	// When: an alarm fires with a datapoint 30 above the threshold
	err := f.svc.HandleAlarmAction(f.ctx, alarmaction.Transition{
		AlarmName: "cpu-high-alarm", NewState: "ALARM", Threshold: 50,
		ReasonData: `{"version":"1.0","recentDatapoints":[80.0],"threshold":50.0}`,
	}, resp.Result.PolicyARN)
	if err != nil {
		t.Fatalf("HandleAlarmAction: %v", err)
	}

	// Then: the larger step applied
	g, _ := f.svc.st.getGroup(f.ctx, "g")
	if g.DesiredCapacity != 5 {
		t.Errorf("desired capacity = %d, want 5 (1 + the >=20 step)", g.DesiredCapacity)
	}

	// And: the activity names the alarm, as AWS's Cause does
	activities, _ := f.svc.st.listActivities(f.ctx, "g")
	found := false
	for _, a := range activities {
		if a.Cause != "" && a.StatusCode == "Successful" &&
			containsAll(a.Cause, "cpu-high-alarm", "triggered policy cpu-high") {
			found = true
		}
	}
	if !found {
		t.Errorf("no activity attributes the change to the alarm: %+v", activities)
	}
}

func TestHandleAlarmAction_reportsAnUnknownPolicy(t *testing.T) {
	f := newFixture(t)

	// A well-formed ARN naming a policy that does not exist is an error the
	// dispatcher reports, not a silent no-op.
	err := f.svc.HandleAlarmAction(f.ctx, alarmaction.Transition{NewState: "ALARM"},
		"arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:abc:autoScalingGroupName/g:policyName/nope")
	if err == nil {
		t.Error("a missing policy was not reported")
	}

	// So is an ARN that is not a scaling policy at all.
	if err := f.svc.HandleAlarmAction(f.ctx, alarmaction.Transition{}, "arn:aws:sns:us-east-1:1:topic"); err == nil {
		t.Error("a non-policy ARN was not reported")
	}
}

func TestBreachValue(t *testing.T) {
	cases := []struct {
		name string
		t    alarmaction.Transition
		want float64
	}{
		{"no reason data", alarmaction.Transition{Threshold: 50}, 0},
		{"malformed reason data", alarmaction.Transition{Threshold: 50, ReasonData: "{"}, 0},
		{"no datapoints", alarmaction.Transition{Threshold: 50, ReasonData: `{"recentDatapoints":[]}`}, 0},
		{"last datapoint above threshold", alarmaction.Transition{Threshold: 50, ReasonData: `{"recentDatapoints":[60,75]}`}, 25},
		{"last datapoint below threshold", alarmaction.Transition{Threshold: 50, ReasonData: `{"recentDatapoints":[10]}`}, -40},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := breachValue(tc.t); got != tc.want {
				t.Errorf("= %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParsePolicyARN(t *testing.T) {
	group, policy, ok := parsePolicyARN(
		"arn:aws:autoscaling:us-east-1:123456789012:scalingPolicy:uuid:autoScalingGroupName/my-asg:policyName/scale-out")
	if !ok || group != "my-asg" || policy != "scale-out" {
		t.Errorf("= (%q, %q, %v), want (my-asg, scale-out, true)", group, policy, ok)
	}
	if _, _, ok := parsePolicyARN("arn:aws:sns:us-east-1:1:topic"); ok {
		t.Error("an SNS ARN parsed as a scaling policy")
	}
	if _, _, ok := parsePolicyARN("arn:aws:autoscaling:us-east-1:1:scalingPolicy:u:autoScalingGroupName/g"); ok {
		t.Error("an ARN with no policy segment parsed")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		found := false
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}
