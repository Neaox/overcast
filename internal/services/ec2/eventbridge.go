package ec2

// eventbridge.go — bridges EC2 instance state transitions onto the default
// EventBridge bus, the way real EC2 publishes an `EC2 Instance State-change
// Notification` every time an instance's state changes.
//
// The hook lives at the store layer (ec2Store.putInstance, in store.go) so it
// fires for every call site that commits a state — RunInstances' initial
// "pending" write, the scheduler's pending→running callback, StopInstances,
// StartInstances, TerminateInstances — with no per-handler wiring and no risk
// of a new call site forgetting to notify. See docs/plans/service-metrics-platform.md's
// "the bus is an input, not a ledger" principle: this emits at the point the
// transition becomes durable in the store, not best-effort off the internal
// event bus.
//
// Reference: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/monitoring-instance-state-changes.html
//
//	{
//	   "id":"7bf73129-1428-4cd3-a780-95db273d1602",
//	   "detail-type":"EC2 Instance State-change Notification",
//	   "source":"aws.ec2",
//	   "account":"123456789012",
//	   "time":"2021-11-11T21:29:54Z",
//	   "region":"us-east-1",
//	   "resources":["arn:aws:ec2:us-east-1:123456789012:instance/i-1234567890abcdef0"],
//	   "detail":{"instance-id":"i-1234567890abcdef0","state":"pending"}
//	}
//
// version, id, account, time and region are filled in by the EventBridge
// service itself (internal/services/eventbridge/delivery.go putEventsEnvelope) —
// producers only supply source, detail-type, detail and resources.

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/overcast-sh/overcast/internal/events"
)

const (
	eventBridgeEC2Source          = "aws.ec2"
	eventBridgeEC2StateChangeType = "EC2 Instance State-change Notification"
)

// ec2InstanceStateChangeDetail is the detail document AWS documents for this
// event: just the instance ID and its new state.
type ec2InstanceStateChangeDetail struct {
	InstanceID string `json:"instance-id"`
	State      string `json:"state"`
}

// InitEventBridge wires the EventBridge bus publisher so EC2 instance state
// transitions emit onto the default bus the way real EC2 does. Called once
// during router construction, after both services exist; nil (untested/wired)
// leaves notifyInstanceStateChange a no-op.
func (s *Service) InitEventBridge(bus events.BusPublisher) {
	s.handler.store.eventBridge = bus
}

// notifyInstanceStateChange emits the EC2 Instance State-change Notification
// whenever an instance's state actually changed between prev and cur. prev is
// nil for the first write of a given instance ID, which is itself a real
// transition AWS notifies on — the instance entering "pending" for the first
// time — so it is treated as a change rather than skipped.
//
// It is a no-op when EventBridge is not wired (most unit tests) or the state
// name did not change (e.g. ModifyInstanceAttribute rewriting InstanceType).
func (s *ec2Store) notifyInstanceStateChange(ctx context.Context, prev, cur *Instance) {
	if s.eventBridge == nil || cur == nil {
		return
	}
	if prev != nil && prev.State.Name == cur.State.Name {
		return
	}

	detail, err := json.Marshal(ec2InstanceStateChangeDetail{
		InstanceID: cur.InstanceID,
		State:      cur.State.Name,
	})
	if err != nil {
		return
	}

	resource := fmt.Sprintf("arn:aws:ec2:%s:%s:instance/%s", s.region(ctx), s.accountID, cur.InstanceID)
	// PublishBusEvent is best-effort by contract (internal/events/sink.go):
	// an error means EventBridge itself could not accept the entry, not that
	// a matching rule failed, and there is nothing more useful to do with it
	// here than the alarmaction dispatcher does with its own PutEvents call.
	_ = s.eventBridge.PublishBusEvent(ctx, events.BusEntry{
		Source:     eventBridgeEC2Source,
		DetailType: eventBridgeEC2StateChangeType,
		Detail:     string(detail),
		Resources:  []string{resource},
	})
}
