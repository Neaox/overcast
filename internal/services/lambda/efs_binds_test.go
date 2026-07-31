package lambda

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

type stubEFSResolver struct {
	volumes map[string]string
}

func (s stubEFSResolver) EFSVolumeForAccessPoint(_ context.Context, arn string) (string, bool) {
	v, ok := s.volumes[arn]
	return v, ok
}

func TestEFSBinds(t *testing.T) {
	cr := &ContainerRuntime{logger: zap.NewNop()}
	fn := &Function{
		Name: "fn",
		FileSystemConfigs: []FileSystemConfig{
			{Arn: "arn:ap-resolved", LocalMountPath: "/mnt/shared"},
			{Arn: "arn:ap-unresolved", LocalMountPath: "/mnt/missing"},
		},
	}
	ctx := context.Background()

	// No resolver wired → no binds.
	if got := cr.efsBinds(ctx, fn); got != nil {
		t.Fatalf("expected nil binds without a resolver, got %v", got)
	}

	// Resolved configs bind volume:path; unresolved ones are skipped.
	cr.SetEFSResolver(stubEFSResolver{volumes: map[string]string{
		"arn:ap-resolved": "overcast-efs-fs-1",
	}})
	got := cr.efsBinds(ctx, fn)
	if len(got) != 1 || got[0] != "overcast-efs-fs-1:/mnt/shared" {
		t.Fatalf("expected single resolved bind, got %v", got)
	}

	// Functions without configs stay bind-free.
	if got := cr.efsBinds(ctx, &Function{Name: "plain"}); got != nil {
		t.Fatalf("expected nil binds for function without configs, got %v", got)
	}
}
