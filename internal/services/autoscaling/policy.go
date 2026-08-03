package autoscaling

// Scaling policies, and the CloudWatch-alarm seam that fires them.
//
// Only the policy types this file can actually execute are accepted:
// SimpleScaling and StepScaling. Target-tracking and predictive scaling are
// refused with a 501 at PutScalingPolicy — a policy that looks configured and
// never runs is exactly what docs/plans/full-emulation-priority.md §2.1
// forbids.
//
// Alarm delivery arrives through internal/alarmaction, the standalone
// dispatcher #473 built: CloudWatch hands over the whole Transition and the
// action ARN, so nothing here calls back into CloudWatch and neither package
// imports the other.

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/alarmaction"
	"github.com/Neaox/overcast/internal/protocol"
)

// Policy types, as AWS spells them.
const (
	policySimpleScaling  = "SimpleScaling"
	policyStepScaling    = "StepScaling"
	policyTargetTracking = "TargetTrackingScaling"
	policyPredictive     = "PredictiveScaling"
)

// Adjustment types, as AWS spells them.
const (
	adjustmentChangeInCapacity        = "ChangeInCapacity"
	adjustmentExactCapacity           = "ExactCapacity"
	adjustmentPercentChangeInCapacity = "PercentChangeInCapacity"
)

var validAdjustmentTypes = map[string]bool{
	adjustmentChangeInCapacity:        true,
	adjustmentExactCapacity:           true,
	adjustmentPercentChangeInCapacity: true,
}

// ─── Validation ───────────────────────────────────────────────────────────────

// validatePolicyInput decides whether a policy can be executed at all, and
// returns either AWS's own 400 for input AWS itself rejects or a 501 for a
// policy type this emulator does not run. The two claims are different and a
// caller acts on them differently.
func validatePolicyInput(req *putScalingPolicyReq) *protocol.AWSError {
	policyType := req.PolicyType
	if policyType == "" {
		policyType = policySimpleScaling
	}

	switch policyType {
	case policyTargetTracking:
		return asgUnsupported("Overcast does not implement %s scaling policies: there is no controller that tracks a target metric value, so the policy would be stored and never executed. Use SimpleScaling or StepScaling with a CloudWatch alarm instead.", policyTargetTracking)
	case policyPredictive:
		return asgUnsupported("Overcast does not implement %s policies: they require a forecasting model over historical metrics, which the emulator does not have.", policyPredictive)
	case policySimpleScaling, policyStepScaling:
		// Executable — validated below.
	default:
		return asgValidationError("Value '%s' at 'policyType' failed to satisfy constraint: Member must satisfy enum value set: [SimpleScaling, StepScaling, TargetTrackingScaling, PredictiveScaling]", req.PolicyType)
	}

	if req.AdjustmentType == "" {
		return asgValidationError("AdjustmentType is required for %s policies", policyType)
	}
	if !validAdjustmentTypes[req.AdjustmentType] {
		return asgValidationError("Value '%s' at 'adjustmentType' failed to satisfy constraint: Member must satisfy enum value set: [ChangeInCapacity, ExactCapacity, PercentChangeInCapacity]", req.AdjustmentType)
	}

	if policyType == policyStepScaling {
		if len(req.StepAdjustments) == 0 {
			return asgValidationError("You must specify at least one step adjustment for a StepScaling policy.")
		}
		if req.Cooldown != 0 {
			// AWS rejects this outright rather than ignoring it.
			return asgValidationError("Cooldown is not supported for policies of type StepScaling.")
		}
	}
	return nil
}

// ─── Execution ────────────────────────────────────────────────────────────────

// executePolicy applies one policy to its group. metricValue and breachAlarm
// describe the alarm that fired it, if any; a manual ExecutePolicy passes a
// zero value and an empty name.
//
// honorCooldown mirrors the API parameter: a simple-scaling policy inside its
// cooldown window does nothing, exactly as on AWS.
func (s *Service) executePolicy(ctx context.Context, p *ScalingPolicy, metricValue float64, breachAlarm string, honorCooldown bool) *protocol.AWSError {
	now := s.clk.Now().UTC()

	s.mu.Lock()
	g, ok := s.st.getGroup(ctx, p.AutoScalingGroupName)
	if !ok {
		s.mu.Unlock()
		return asgValidationError("Auto Scaling group name not found - AutoScalingGroup %s not found", p.AutoScalingGroupName)
	}

	if honorCooldown && p.PolicyType != policyStepScaling &&
		!g.CooldownUntil.IsZero() && now.Before(g.CooldownUntil) {
		s.mu.Unlock()
		s.log.Debug("autoscaling: policy suppressed by cooldown",
			zap.String("group", p.AutoScalingGroupName), zap.String("policy", p.PolicyName))
		return nil
	}

	current := g.DesiredCapacity
	adjustment, applies := policyAdjustment(p, metricValue)
	if !applies {
		s.mu.Unlock()
		s.log.Debug("autoscaling: no step adjustment matched the breach",
			zap.String("group", p.AutoScalingGroupName), zap.String("policy", p.PolicyName),
			zap.Float64("metric_value", metricValue))
		return nil
	}

	target := applyAdjustment(p.AdjustmentType, current, adjustment, p.MinAdjustmentMagnitude)
	target = clampCapacity(target, g.MinSize, g.MaxSize)
	if target == current {
		s.mu.Unlock()
		return nil
	}

	g.DesiredCapacity = target
	cooldown := p.Cooldown
	if cooldown == 0 {
		cooldown = g.DefaultCooldown
	}
	if cooldown > 0 {
		g.CooldownUntil = now.Add(time.Duration(cooldown) * time.Second)
	}
	if err := s.st.putGroup(ctx, g); err != nil {
		s.mu.Unlock()
		return &protocol.AWSError{Code: "InternalFailure", Message: "failed to persist group", HTTPStatus: http.StatusInternalServerError}
	}
	s.recordActivity(ctx, g, &Activity{
		Description: fmt.Sprintf("Setting desired capacity to %d.", target),
		Cause:       causePolicy(now, p.PolicyName, breachAlarm, current, target),
		StartTime:   now,
		EndTime:     now,
		StatusCode:  "Successful",
		Progress:    100,
	})
	s.mu.Unlock()

	s.poke()
	return nil
}

// policyAdjustment returns the adjustment a policy applies for a given metric
// value, and whether any adjustment applies at all. Simple scaling always
// applies; step scaling applies only when a step's interval contains the
// breach.
func policyAdjustment(p *ScalingPolicy, metricValue float64) (int, bool) {
	if p.PolicyType != policyStepScaling {
		return p.ScalingAdjustment, true
	}
	for _, step := range p.StepAdjustments {
		if step.MetricIntervalLowerBound != nil && metricValue < *step.MetricIntervalLowerBound {
			continue
		}
		if step.MetricIntervalUpperBound != nil && metricValue >= *step.MetricIntervalUpperBound {
			continue
		}
		return step.ScalingAdjustment, true
	}
	return 0, false
}

// applyAdjustment turns an adjustment into a target capacity, following AWS's
// rules for each AdjustmentType — including MinAdjustmentMagnitude, which
// exists precisely so a percentage change of a small group is not rounded away
// to nothing.
func applyAdjustment(adjustmentType string, current, adjustment, minMagnitude int) int {
	switch adjustmentType {
	case adjustmentExactCapacity:
		return adjustment
	case adjustmentPercentChangeInCapacity:
		raw := float64(current) * float64(adjustment) / 100
		delta := int(math.Trunc(raw))
		if minMagnitude > 0 && delta != 0 && abs(delta) < minMagnitude {
			if delta < 0 {
				delta = -minMagnitude
			} else {
				delta = minMagnitude
			}
		}
		if delta == 0 && adjustment != 0 && minMagnitude > 0 {
			if adjustment < 0 {
				delta = -minMagnitude
			} else {
				delta = minMagnitude
			}
		}
		return current + delta
	default: // ChangeInCapacity
		return current + adjustment
	}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// ─── CloudWatch alarm seam ────────────────────────────────────────────────────

// HandleAlarmAction executes the scaling policy an alarm action ARN names.
//
// It is registered with internal/alarmaction as the sink for every
// "autoscaling" action ARN (`alarmActions.Register("autoscaling", …)` in
// router.New). CloudWatch hands over the whole Transition, so nothing here
// calls back into CloudWatch: the breaching metric value comes from the
// transition's StateReasonData, which is what a step-scaling policy needs to
// pick its interval.
//
// A returned error is reported by the dispatcher as a failed alarm action —
// the transition already happened, so a policy that cannot run is surfaced,
// not swallowed.
func (s *Service) HandleAlarmAction(ctx context.Context, t alarmaction.Transition, arn string) error {
	group, policyName, ok := parsePolicyARN(arn)
	if !ok {
		return fmt.Errorf("autoscaling: %q is not a scaling-policy ARN", arn)
	}
	policy, found := s.st.getPolicy(ctx, group, policyName)
	if !found {
		return fmt.Errorf("autoscaling: no scaling policy %q on group %q", policyName, group)
	}

	// The ARN was in the action list CloudWatch selected for this state, so
	// the policy runs — AWS executes a policy attached to OKActions on the way
	// back to OK just as it does one attached to AlarmActions.
	value := breachValue(t)
	if aerr := s.executePolicy(ctx, policy, value, t.AlarmName, true); aerr != nil {
		return fmt.Errorf("autoscaling: %s: %s", aerr.Code, aerr.Message)
	}
	return nil
}

// breachValue extracts the datapoint that moved the alarm, so a step-scaling
// policy can pick the interval the breach falls in. AWS measures the breach as
// the metric value itself against the step bounds, which are relative to the
// alarm threshold — so the value returned here is (datapoint − threshold).
//
// StateReasonData is the document CloudWatch publishes on every transition;
// when it carries no datapoints (a missing-data transition) the breach is
// reported as zero, which selects the step whose interval contains the
// threshold.
func breachValue(t alarmaction.Transition) float64 {
	if t.ReasonData == "" {
		return 0
	}
	var doc struct {
		RecentDatapoints []float64 `json:"recentDatapoints"`
	}
	if json.Unmarshal([]byte(t.ReasonData), &doc) != nil || len(doc.RecentDatapoints) == 0 {
		return 0
	}
	return doc.RecentDatapoints[len(doc.RecentDatapoints)-1] - t.Threshold
}
