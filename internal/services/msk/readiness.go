package msk

// readiness.go — what ACTIVE is allowed to mean, and what a cluster whose
// broker never came up is called instead.
//
// # The state
//
// FAILED, which is a real ClusterState: the MSK API reference lists ACTIVE,
// CREATING, UPDATING, DELETING, FAILED, MAINTENANCE, REBOOTING_BROKER and
// HEALING (the ClusterState model on
// docs.aws.amazon.com/msk/1.0/apireference/clusters-clusterarn.html, checked
// 2026-08-15). It is terminal for `aws kafka wait cluster-active`, which is the
// point: a cluster stuck in CREATING made that waiter spin out its attempts and
// then report a timeout, which says nothing about what went wrong.
//
// # Where the reason lives
//
// In stateInfo, which AWS models for exactly this and describes as "if the
// cluster is in an unusable state, this field contains the code/message that
// describes the issue" — the same job eks/live_runtime.go gives health.issues,
// and the model it was following. Unlike the ElastiCache side, this one is on
// the wire: DescribeCluster and DescribeClusterV2 both carry stateInfo, so the
// reason reaches a caller through the API rather than only a log line.

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/serviceutil/readiness"
)

// stateFailed is AWS's terminal ClusterState for a cluster that cannot serve.
const stateFailed = "FAILED"

// stateInfoUnhealthy is the stateInfo code carried with it. AWS does not
// publish an enum for stateInfo.code — the model types it as a plain string —
// so this is Overcast's own value, kept descriptive because the message beside
// it is where the detail is.
const stateInfoUnhealthy = "BROKER_UNREACHABLE"

// brokerReadiness is the timing an MSK readiness watch runs on.
//
// Calibrated against a Redpanda container on this machine's Docker daemon: it
// forms a single-node cluster, elects itself controller and opens its Kafka
// listener, which is seconds rather than the sub-second an already-initialised
// Redis takes and is why this budget is double ElastiCache's. It is still well
// short of RDS's five minutes, because nothing here initialises a data
// directory it then has to replay.
var brokerReadiness = readiness.Policy{
	FirstDelay: 1 * time.Second,
	Interval:   2 * time.Second,
	Budget:     120 * time.Second,
}

// clusterAwaiting are the states a readiness watch is entitled to settle.
// Anything else means something moved the cluster on purpose.
//
// FAILED is in the set so the failure is recoverable: a broker that comes back
// — through a Docker start event or the startup reconcile sweep — schedules a
// fresh watch, and that watch may promote the cluster to ACTIVE again.
var clusterAwaiting = []string{"CREATING", "STARTING", "HEALING", stateFailed}

// clusterRuntimeExpected reports whether a cluster in this state should have a
// broker behind it, which is what reconciliation replaces and re-checks.
//
// FAILED is in the set for the same reason it is in clusterAwaiting: the broker
// that never came up is the whole reason the cluster is there, so a restart
// replaces it and a broker that does come up promotes the cluster out again.
// Left out, a cluster would have been failed once and then never looked at.
func clusterRuntimeExpected(state string) bool {
	switch state {
	case "ACTIVE", "CREATING", "STARTING", "HEALING", stateFailed:
		return true
	default:
		return false
	}
}

// settleClusterWithoutBroker marks a cluster that has no broker container
// coming ACTIVE.
//
// Without Docker nothing is started, so no readiness watch is scheduled and
// nothing exists that could move the cluster out of CREATING. Leaving it there
// claims progress that will never be made — the mirror image of the ACTIVE with
// no broker behind it that the watch above exists to prevent, and just as
// untrue. `aws kafka wait cluster-active` spun out its attempts against one,
// and a CloudFormation stack waiting on it had the whole budget to burn before
// rolling back a cluster that was as ready as it would ever be.
//
// A metadata-only cluster is therefore ACTIVE the moment it is recorded,
// because there is no broker being claimed and nothing is coming to prove one.
// createClusterV2 already says exactly this about a serverless cluster, which
// has no brokers to provision either; RDS and Lambda say it about their own
// metadata-only resources.
//
// Two properties this relies on, both deliberate:
//
//   - The transition is guarded on the cluster still being in CREATING, so a
//     delete arriving in the same moment wins and this becomes a no-op rather
//     than resurrecting what that call decided.
//   - ACTIVE is a state reconciliation still expects a broker for (see
//     clusterRuntimeExpected), so a daemon that appears later still gets a
//     container built for the record and a real ApiVersions probe run against
//     it. Marking it ready is not the same as writing it off.
func (h *Handler) settleClusterWithoutBroker(clusterARN string) {
	region := serviceutil.ARNRegion(clusterARN)
	h.scheduler.AfterScoped(region, clusterARN, "active", 0, func(ctx context.Context) {
		h.transitionCluster(ctx, clusterARN, "ACTIVE", "CREATING")
	})
}

// failCluster settles a cluster whose broker never answered, recording why in
// the stateInfo DescribeCluster returns. Re-read inside the record's lock
// rather than written from a snapshot: a watch that has spent two minutes
// waiting must not resurrect a cluster that was deleted while it waited.
func (h *Handler) failCluster(ctx context.Context, clusterARN, reason string) {
	if _, aerr := h.mutateCluster(ctx, clusterARN, func(c *Cluster) *protocol.AWSError {
		if !stateIn(c.State, clusterAwaiting) {
			return errClusterMovedOn
		}
		c.State = stateFailed
		c.StateInfo = &StateInfo{Code: stateInfoUnhealthy, Message: reason}
		return nil
	}); aerr != nil {
		if aerr != errClusterMovedOn {
			h.log.Warn("MSK: persist failed cluster",
				zap.String("cluster", clusterARN), zap.String("error", aerr.Message))
		}
		return
	}
	h.log.Warn("MSK: cluster never became ready",
		zap.String("cluster", clusterARN), zap.String("state", stateFailed),
		zap.String("reason", reason))
}
