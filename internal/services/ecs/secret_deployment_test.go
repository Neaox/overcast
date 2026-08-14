package ecs

import (
	"context"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/middleware"
)

func TestUpdateService_forceNewDeploymentRefreshesSecret(t *testing.T) {
	const secretARN = "arn:aws:secretsmanager:us-east-1:123456789012:secret:app-AbCdEf"

	for _, tc := range []struct {
		name             string
		initialContainer string
		exitImmediately  bool
	}{
		{name: "running task", initialContainer: "before"},
		{name: "previous task exited one", initialContainer: "placeholder", exitImmediately: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a one-task service whose first container received the
			// current secret value, whether it stays running or exits immediately.
			h, clk, fd := newECSDockerTestHandler(t)
			ctx := middleware.ContextWithRegion(context.Background(), "us-east-1")
			secrets := stubSecrets{secretARN: tc.initialContainer}
			h.secrets = secrets
			if w := postJSON(t, ctx, h.CreateCluster, map[string]any{"clusterName": "secret-cluster"}); w.Code != 200 {
				t.Fatalf("CreateCluster: HTTP %d: %s", w.Code, w.Body.String())
			}
			if w := postJSON(t, ctx, h.RegisterTaskDefinition, map[string]any{
				"family": "secret-task",
				"containerDefinitions": []map[string]any{{
					"name": "app", "image": "busybox",
					"secrets": []map[string]any{{"name": "APP_SECRET", "valueFrom": secretARN}},
				}},
			}); w.Code != 200 {
				t.Fatalf("RegisterTaskDefinition: HTTP %d: %s", w.Code, w.Body.String())
			}

			var exited atomic.Bool
			exitHandled := make(chan struct{})
			if tc.exitImmediately {
				fd.setOnStart(func(containerID string) {
					if !exited.CompareAndSwap(false, true) {
						return
					}
					resourceID := fd.latestResourceID()
					go func() {
						defer close(exitHandled)
						h.handleContainerDied(ctx, events.Event{
							Type: events.DockerContainerDied,
							Payload: events.DockerContainerPayload{
								ContainerID: containerID, Action: "die", ExitCode: "1", Reason: "exit 1",
								Service: serviceName, ResourceID: resourceID,
							},
						})
					}()
				})
			}

			if w := postJSON(t, ctx, h.CreateService, map[string]any{
				"cluster": "secret-cluster", "serviceName": "secret-service",
				"taskDefinition": "secret-task:1", "desiredCount": 1,
			}); w.Code != 200 {
				t.Fatalf("CreateService: HTTP %d: %s", w.Code, w.Body.String())
			}
			if tc.exitImmediately {
				select {
				case <-exitHandled:
				case <-time.After(5 * time.Second):
					t.Fatal("timed out waiting for the first container to exit")
				}
			} else {
				h.scheduler.AdvanceAndSettle(clk, time.Second)
			}
			if got := fd.createdEnvironments(); len(got) != 1 || !slices.Contains(got[0], "APP_SECRET="+tc.initialContainer) {
				t.Fatalf("initial container environments = %#v, want APP_SECRET=%s", got, tc.initialContainer)
			}

			// When: the secret changes immediately before the caller forces a
			// fresh deployment, which is AWS's documented refresh mechanism.
			secrets[secretARN] = "after"
			if w := postJSON(t, ctx, h.UpdateService, map[string]any{
				"cluster": "secret-cluster", "service": "secret-service", "forceNewDeployment": true,
			}); w.Code != 200 {
				t.Fatalf("UpdateService: HTTP %d: %s", w.Code, w.Body.String())
			}
			h.scheduler.AdvanceAndSettle(clk, time.Second)

			// Then: ECS creates a replacement container and resolves the secret
			// again at that container's start, even when the old task exited 1.
			got := fd.createdEnvironments()
			if len(got) != 2 {
				t.Fatalf("created %d containers, want 2 after a forced deployment: %#v", len(got), got)
			}
			if !slices.Contains(got[1], "APP_SECRET=after") {
				t.Errorf("replacement container environments = %#v, want APP_SECRET=after", got[1])
			}
		})
	}
}
