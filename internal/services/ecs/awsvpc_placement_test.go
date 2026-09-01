package ecs

// awsvpc_placement_test.go — what an awsvpc task is allowed to reach.
//
// Naming a subnet places the task in that VPC and, since enforcement, takes
// away everything outside it. `assignPublicIp: ENABLED` is the way back, and it
// is AWS's own field rather than an Overcast one: a task that needs it here
// needs it deployed too.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/serviceutil"
)

// stubVPCResolver places every subnet in one launchable VPC, so these cases
// turn on the network configuration rather than on EC2 state.
type stubVPCResolver struct{ networkID string }

func (s stubVPCResolver) VpcIDForSubnet(context.Context, string) string   { return "vpc-abc" }
func (s stubVPCResolver) VPCNetworkStatus(context.Context, string) string { return "ok" }
func (s stubVPCResolver) DockerNetworkForVpc(context.Context, string) string {
	return s.networkID
}
func (s stubVPCResolver) AllocatePrivateIPForSubnet(context.Context, string) string {
	return "10.0.1.5"
}

func placementHandler(t *testing.T) *Handler {
	t.Helper()
	h := &Handler{log: serviceutil.NewServiceLogger(zap.NewNop(), "ecs")}
	h.vpcResolver = stubVPCResolver{networkID: testVPCNetworkID}
	return h
}

func awsvpcConfig(assignPublicIP string) *NetworkConfiguration {
	return &NetworkConfiguration{
		AwsvpcConfiguration: &AwsvpcConfiguration{
			Subnets:        []string{"subnet-1"},
			AssignPublicIp: assignPublicIP,
		},
	}
}

func TestResolveAwsvpcPlacement_assignPublicIP(t *testing.T) {
	cases := map[string]struct {
		value string
		want  bool
	}{
		"ENABLED opts out of the restriction":     {value: "ENABLED", want: true},
		"DISABLED stays in the VPC":               {value: "DISABLED", want: false},
		"unset stays in the VPC":                  {value: "", want: false},
		"the value is matched case-insensitively": {value: "enabled", want: true},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h := placementHandler(t)

			got, aerr := h.resolveAwsvpcPlacement(context.Background(), awsvpcConfig(tc.value), "RunTask")
			if aerr != nil {
				t.Fatalf("resolveAwsvpcPlacement: %s", aerr.Message)
			}
			if got.assignPublicIP != tc.want {
				t.Fatalf("assignPublicIP = %v, want %v", got.assignPublicIP, tc.want)
			}
			// The VPC placement itself is unaffected either way — the flag adds
			// the default plane back, it does not move the task out of its VPC.
			if got.networkID != testVPCNetworkID {
				t.Fatalf("networkID = %q, want the VPC network", got.networkID)
			}
		})
	}
}

// A task with no network configuration is not in a VPC at all, so there is
// nothing for the flag to opt out of.
func TestResolveAwsvpcPlacement_noNetworkConfiguration(t *testing.T) {
	h := placementHandler(t)

	got, aerr := h.resolveAwsvpcPlacement(context.Background(), nil, "RunTask")
	if aerr != nil {
		t.Fatalf("resolveAwsvpcPlacement: %s", aerr.Message)
	}
	if got.networkID != "" || got.assignPublicIP {
		t.Fatalf("placement = %#v, want the zero placement", got)
	}
}
