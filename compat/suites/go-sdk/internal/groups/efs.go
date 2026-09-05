package groups

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/efs"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/clients"
	"github.com/overcast-sh/overcast-compat-go-sdk/internal/harness"
)

// EFS returns the Elastic File System service group.
func EFS(c *clients.Clients) ServiceGroup {
	g := &efsGroup{c: c}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"efs-mount-targets:CreateMountTarget":                 g.CreateMountTarget,
			"efs-mount-targets:DescribeMountTargets":              g.DescribeMountTargets,
			"efs-mount-targets:DescribeMountTargetSecurityGroups": g.DescribeMountTargetSecurityGroups,
			"efs-mount-targets:ModifyMountTargetSecurityGroups":   g.ModifyMountTargetSecurityGroups,
			"efs-mount-targets:DeleteMountTarget":                 g.DeleteMountTarget,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"efs-mount-targets": g.setupMountTargets,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"efs-mount-targets": g.teardownMountTargets,
		},
	}
}

type efsGroup struct{ c *clients.Clients }

func (g *efsGroup) cl() *efs.Client { return g.c.EFS() }

// subnetID builds an AWS-shaped subnet ID that is still unique per run: a file
// system accepts only one mount target per availability zone, and the emulator
// derives the zone from the subnet ID.
//
// The shape matters — the SDKs validate SubnetId's length before the request
// leaves the client, and "subnet-<runId>" is short enough to be rejected. Real
// long-form IDs are "subnet-" plus 17 hex characters, so the run ID's own hex
// digits are kept and the rest is padded.
func (g *efsGroup) subnetID(t *harness.TestContext) string {
	var hex strings.Builder
	for _, r := range t.RunID {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			hex.WriteRune(r)
		}
	}
	padded := hex.String() + strings.Repeat("0", 17)
	return "subnet-" + padded[:17]
}

// setupMountTargets creates the file system the group's mount targets attach
// to. Mount targets have no meaning without one, so this is setup rather than
// a test.
func (g *efsGroup) setupMountTargets(ctx context.Context, t *harness.TestContext) error {
	out, err := g.cl().CreateFileSystem(ctx, &efs.CreateFileSystemInput{
		CreationToken: aws.String(fmt.Sprintf("oc-%s-efs-mount-targets", t.RunID)),
	})
	if err != nil {
		return err
	}
	if aws.ToString(out.FileSystemId) == "" {
		return fmt.Errorf("setup: CreateFileSystem returned no FileSystemId")
	}
	t.Set("efsFileSystemId", aws.ToString(out.FileSystemId))
	return nil
}

// teardownMountTargets removes the mount target before the file system:
// DeleteFileSystem answers FileSystemInUse while any mount target remains.
func (g *efsGroup) teardownMountTargets(ctx context.Context, t *harness.TestContext) error {
	if id := t.GetString("efsMountTargetId"); id != "" {
		g.cl().DeleteMountTarget(ctx, &efs.DeleteMountTargetInput{ //nolint:errcheck
			MountTargetId: aws.String(id),
		})
	}
	if id := t.GetString("efsFileSystemId"); id != "" {
		g.cl().DeleteFileSystem(ctx, &efs.DeleteFileSystemInput{ //nolint:errcheck
			FileSystemId: aws.String(id),
		})
	}
	return nil
}

func (g *efsGroup) CreateMountTarget(ctx context.Context, t *harness.TestContext) error {
	fsID := t.GetString("efsFileSystemId")
	if fsID == "" {
		return fmt.Errorf("CreateMountTarget: no file system from setup")
	}
	out, err := g.cl().CreateMountTarget(ctx, &efs.CreateMountTargetInput{
		FileSystemId:   aws.String(fsID),
		SubnetId:       aws.String(g.subnetID(t)),
		SecurityGroups: []string{"sg-compat-a"},
	})
	if err != nil {
		return err
	}
	if aws.ToString(out.MountTargetId) == "" {
		return fmt.Errorf("CreateMountTarget: missing MountTargetId")
	}
	if got := aws.ToString(out.FileSystemId); got != fsID {
		return fmt.Errorf("CreateMountTarget: expected FileSystemId %q, got %q", fsID, got)
	}
	if out.LifeCycleState == "" {
		return fmt.Errorf("CreateMountTarget: missing LifeCycleState")
	}
	t.Set("efsMountTargetId", aws.ToString(out.MountTargetId))
	return nil
}

func (g *efsGroup) DescribeMountTargets(ctx context.Context, t *harness.TestContext) error {
	fsID := t.GetString("efsFileSystemId")
	mtID := t.GetString("efsMountTargetId")
	if mtID == "" {
		return fmt.Errorf("DescribeMountTargets: no mount target from CreateMountTarget")
	}
	out, err := g.cl().DescribeMountTargets(ctx, &efs.DescribeMountTargetsInput{
		FileSystemId: aws.String(fsID),
	})
	if err != nil {
		return err
	}
	if len(out.MountTargets) == 0 {
		return fmt.Errorf("DescribeMountTargets: expected at least 1 mount target, got 0")
	}
	for _, mt := range out.MountTargets {
		if aws.ToString(mt.MountTargetId) != mtID {
			continue
		}
		if aws.ToString(mt.IpAddress) == "" {
			return fmt.Errorf("DescribeMountTargets: mount target %q has no IpAddress", mtID)
		}
		return nil
	}
	return fmt.Errorf("DescribeMountTargets: mount target %q not in the response", mtID)
}

func (g *efsGroup) DescribeMountTargetSecurityGroups(ctx context.Context, t *harness.TestContext) error {
	mtID := t.GetString("efsMountTargetId")
	if mtID == "" {
		return fmt.Errorf("DescribeMountTargetSecurityGroups: no mount target from CreateMountTarget")
	}
	out, err := g.cl().DescribeMountTargetSecurityGroups(ctx, &efs.DescribeMountTargetSecurityGroupsInput{
		MountTargetId: aws.String(mtID),
	})
	if err != nil {
		return err
	}
	if len(out.SecurityGroups) != 1 {
		return fmt.Errorf("DescribeMountTargetSecurityGroups: expected the 1 group set at create, got %d", len(out.SecurityGroups))
	}
	if out.SecurityGroups[0] != "sg-compat-a" {
		return fmt.Errorf("DescribeMountTargetSecurityGroups: expected sg-compat-a, got %q", out.SecurityGroups[0])
	}
	return nil
}

func (g *efsGroup) ModifyMountTargetSecurityGroups(ctx context.Context, t *harness.TestContext) error {
	mtID := t.GetString("efsMountTargetId")
	if mtID == "" {
		return fmt.Errorf("ModifyMountTargetSecurityGroups: no mount target from CreateMountTarget")
	}
	if _, err := g.cl().ModifyMountTargetSecurityGroups(ctx, &efs.ModifyMountTargetSecurityGroupsInput{
		MountTargetId:  aws.String(mtID),
		SecurityGroups: []string{"sg-compat-b", "sg-compat-c"},
	}); err != nil {
		return err
	}
	// The mutation is what this test is about, so assert it here rather than
	// leaving it for another test to notice.
	out, err := g.cl().DescribeMountTargetSecurityGroups(ctx, &efs.DescribeMountTargetSecurityGroupsInput{
		MountTargetId: aws.String(mtID),
	})
	if err != nil {
		return fmt.Errorf("ModifyMountTargetSecurityGroups: re-describe failed: %w", err)
	}
	if len(out.SecurityGroups) != 2 {
		return fmt.Errorf("ModifyMountTargetSecurityGroups: expected 2 groups after modify, got %d", len(out.SecurityGroups))
	}
	for i, want := range []string{"sg-compat-b", "sg-compat-c"} {
		if out.SecurityGroups[i] != want {
			return fmt.Errorf("ModifyMountTargetSecurityGroups: group %d: expected %q, got %q", i, want, out.SecurityGroups[i])
		}
	}
	return nil
}

func (g *efsGroup) DeleteMountTarget(ctx context.Context, t *harness.TestContext) error {
	fsID := t.GetString("efsFileSystemId")
	mtID := t.GetString("efsMountTargetId")
	if mtID == "" {
		return fmt.Errorf("DeleteMountTarget: no mount target from CreateMountTarget")
	}
	if _, err := g.cl().DeleteMountTarget(ctx, &efs.DeleteMountTargetInput{
		MountTargetId: aws.String(mtID),
	}); err != nil {
		return err
	}
	t.Set("efsMountTargetId", "")

	out, err := g.cl().DescribeMountTargets(ctx, &efs.DescribeMountTargetsInput{
		FileSystemId: aws.String(fsID),
	})
	if err != nil {
		return fmt.Errorf("DeleteMountTarget: re-describe failed: %w", err)
	}
	for _, mt := range out.MountTargets {
		if aws.ToString(mt.MountTargetId) == mtID {
			return fmt.Errorf("DeleteMountTarget: mount target %q still listed after delete", mtID)
		}
	}
	return nil
}
