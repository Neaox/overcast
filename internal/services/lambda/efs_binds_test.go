package lambda

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

type stubEFSMount struct {
	volume  string
	subpath string
}

type stubEFSResolver struct {
	mounts map[string]stubEFSMount
}

func (s stubEFSResolver) EFSVolumeForAccessPoint(_ context.Context, arn string) (string, string, bool) {
	m, ok := s.mounts[arn]
	return m.volume, m.subpath, ok
}

func TestEFSMounts(t *testing.T) {
	cr := &ContainerRuntime{logger: zap.NewNop()}
	fn := &Function{
		Name: "fn",
		FileSystemConfigs: []FileSystemConfig{
			{Arn: "arn:ap-resolved", LocalMountPath: "/mnt/shared"},
			{Arn: "arn:ap-unresolved", LocalMountPath: "/mnt/missing"},
		},
	}
	ctx := context.Background()

	// No resolver wired → no mounts.
	if got := cr.efsMounts(ctx, fn); got != nil {
		t.Fatalf("expected nil mounts without a resolver, got %v", got)
	}

	// Resolved configs mount the volume scoped by subpath; unresolved skipped.
	cr.SetEFSResolver(stubEFSResolver{mounts: map[string]stubEFSMount{
		"arn:ap-resolved": {volume: "overcast-efs-fs-1", subpath: "app"},
	}})
	got := cr.efsMounts(ctx, fn)
	if len(got) != 1 {
		t.Fatalf("expected single resolved mount, got %v", got)
	}
	m := got[0]
	if m.Type != "volume" || m.Source != "overcast-efs-fs-1" || m.Target != "/mnt/shared" || m.ReadOnly {
		t.Fatalf("unexpected mount: %+v", m)
	}
	if m.VolumeOptions == nil || m.VolumeOptions.Subpath != "app" {
		t.Fatalf("expected Subpath 'app', got %+v", m.VolumeOptions)
	}

	// A root-directory of "/" maps to no VolumeOptions at all.
	cr.SetEFSResolver(stubEFSResolver{mounts: map[string]stubEFSMount{
		"arn:ap-resolved": {volume: "overcast-efs-fs-1"},
	}})
	if got := cr.efsMounts(ctx, fn); got[0].VolumeOptions != nil {
		t.Fatalf("expected no VolumeOptions for root mounts, got %+v", got[0].VolumeOptions)
	}

	// Functions without configs stay mount-free.
	if got := cr.efsMounts(ctx, &Function{Name: "plain"}); got != nil {
		t.Fatalf("expected nil mounts for function without configs, got %v", got)
	}
}
