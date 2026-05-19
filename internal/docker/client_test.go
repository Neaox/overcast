package docker

import (
	"testing"

	"go.uber.org/zap"
)

func TestNewClient_UnixSocket(t *testing.T) {
	c := NewClient("/var/run/docker.sock", zap.NewNop())
	if c.host != "http://docker" {
		t.Errorf("expected host http://docker, got %s", c.host)
	}
}

func TestNewClient_TCP(t *testing.T) {
	c := NewClient("tcp://dind:2375", zap.NewNop())
	if c.host != "http://dind:2375" {
		t.Errorf("expected host http://dind:2375, got %s", c.host)
	}
}

func TestNewClient_TCPLocalhost(t *testing.T) {
	c := NewClient("tcp://127.0.0.1:2375", zap.NewNop())
	if c.host != "http://127.0.0.1:2375" {
		t.Errorf("expected host http://127.0.0.1:2375, got %s", c.host)
	}
}

func TestNewClient_BarePathIsUnix(t *testing.T) {
	c := NewClient("/tmp/custom.sock", zap.NewNop())
	if c.host != "http://docker" {
		t.Errorf("expected host http://docker for bare path, got %s", c.host)
	}
}
