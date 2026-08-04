package rds

import (
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/serviceutil"
)

func modeService(t *testing.T, mode config.RDSMode) *Service {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", Port: 4566, RDSMode: mode}
	return &Service{
		handler: testHandler(cfg),
		log:     serviceutil.NewServiceLogger(zap.NewNop(), serviceName),
	}
}

// Mock mode has to refuse the client itself, not merely go unregistered by the
// router. Every RDS path that would touch a container is gated on dockerReady,
// so a client accepted here would start engine containers no matter what the
// mode said.
func TestSetDocker_mockModeStaysMetadataOnly(t *testing.T) {
	s := modeService(t, config.RDSModeMock)

	s.SetDocker(nil)

	if s.handler.dockerReady.Load() {
		t.Fatal("dockerReady: expected false in mock mode, got true — RDS would start engine containers")
	}
	if s.handler.gc != nil {
		t.Fatal("gc: expected nil in mock mode, got a sweeper for containers that cannot exist")
	}
}

// Only an explicit mock opts out. Live mode and the zero-value config that many
// tests build directly both have to keep running containers, or this change
// would quietly turn RDS off for everyone.
func TestContainersEnabled_onlyMockOptsOut(t *testing.T) {
	for _, tc := range []struct {
		name string
		mode config.RDSMode
		want bool
	}{
		{"live", config.RDSModeLive, true},
		{"unset", config.RDSMode(""), true},
		{"mock", config.RDSModeMock, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := modeService(t, tc.mode).containersEnabled(); got != tc.want {
				t.Fatalf("containersEnabled() with mode %q = %v, want %v", tc.mode, got, tc.want)
			}
		})
	}
}
