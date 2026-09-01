package groups

import (
	"context"
	"fmt"
	"strings"

	"github.com/overcast-sh/overcast-compat-cli/internal/awscli"
	"github.com/overcast-sh/overcast-compat-cli/internal/harness"
)

// EFS returns the Elastic File System service group.
func EFS() ServiceGroup {
	g := &efsCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateMountTarget":                 g.CreateMountTarget,
			"DescribeMountTargets":              g.DescribeMountTargets,
			"DescribeMountTargetSecurityGroups": g.DescribeMountTargetSecurityGroups,
			"ModifyMountTargetSecurityGroups":   g.ModifyMountTargetSecurityGroups,
			"DeleteMountTarget":                 g.DeleteMountTarget,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"efs-mount-targets": g.setupMountTargets,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"efs-mount-targets": g.teardownMountTargets,
		},
	}
}

type efsCliGroup struct{}

// efsSubnetID builds an AWS-shaped subnet ID that is still unique per run: a
// file system accepts only one mount target per availability zone, and the
// emulator derives the zone from the subnet ID.
//
// The shape matters — the SDKs validate SubnetId's length before the request
// leaves the client, and "subnet-<runId>" is short enough to be rejected. Real
// long-form IDs are "subnet-" plus 17 hex characters, so the run ID's own hex
// digits are kept and the rest is padded.
func efsSubnetID(runID string) string {
	var hex strings.Builder
	for _, r := range runID {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') {
			hex.WriteRune(r)
		}
	}
	padded := hex.String() + strings.Repeat("0", 17)
	return "subnet-" + padded[:17]
}

// setupMountTargets creates the file system the group's mount targets attach
// to. Mount targets have no meaning without one, so this is setup rather than
// a test — CreateFileSystem is exercised by the emulator's own suite.
func (g *efsCliGroup) setupMountTargets(_ context.Context, t *harness.TestContext) error {
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "create-file-system",
		"--creation-token", fmt.Sprintf("oc-%s-efs-mount-targets", t.RunID),
	)
	if err != nil {
		return err
	}
	id, _ := out["FileSystemId"].(string)
	if id == "" {
		return fmt.Errorf("setup: CreateFileSystem returned no FileSystemId")
	}
	t.Set("file_system_id", id)
	return nil
}

// teardownMountTargets removes the mount target before the file system:
// DeleteFileSystem returns FileSystemInUse while any mount target remains.
func (g *efsCliGroup) teardownMountTargets(_ context.Context, t *harness.TestContext) error {
	if id := t.GetString("mount_target_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "efs", "delete-mount-target", "--mount-target-id", id) //nolint:errcheck
	}
	if id := t.GetString("file_system_id"); id != "" {
		awscli.Run(t.Endpoint, t.Region, "efs", "delete-file-system", "--file-system-id", id) //nolint:errcheck
	}
	return nil
}

func (g *efsCliGroup) CreateMountTarget(_ context.Context, t *harness.TestContext) error {
	fsID := t.GetString("file_system_id")
	if fsID == "" {
		return fmt.Errorf("CreateMountTarget: no file system from setup")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "create-mount-target",
		"--file-system-id", fsID,
		"--subnet-id", efsSubnetID(t.RunID),
		"--security-groups", "sg-compat-a",
	)
	if err != nil {
		return err
	}
	mtID, _ := out["MountTargetId"].(string)
	if mtID == "" {
		return fmt.Errorf("CreateMountTarget: missing MountTargetId")
	}
	if got, _ := out["FileSystemId"].(string); got != fsID {
		return fmt.Errorf("CreateMountTarget: expected FileSystemId %q, got %q", fsID, got)
	}
	if got, _ := out["LifeCycleState"].(string); got == "" {
		return fmt.Errorf("CreateMountTarget: missing LifeCycleState")
	}
	t.Set("mount_target_id", mtID)
	return nil
}

func (g *efsCliGroup) DescribeMountTargets(_ context.Context, t *harness.TestContext) error {
	fsID := t.GetString("file_system_id")
	mtID := t.GetString("mount_target_id")
	if mtID == "" {
		return fmt.Errorf("DescribeMountTargets: no mount target from CreateMountTarget")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "describe-mount-targets",
		"--file-system-id", fsID,
	)
	if err != nil {
		return err
	}
	targets, _ := out["MountTargets"].([]any)
	if len(targets) == 0 {
		return fmt.Errorf("DescribeMountTargets: expected at least 1 mount target, got 0")
	}
	for _, raw := range targets {
		mt, _ := raw.(map[string]any)
		if id, _ := mt["MountTargetId"].(string); id != mtID {
			continue
		}
		if ip, _ := mt["IpAddress"].(string); ip == "" {
			return fmt.Errorf("DescribeMountTargets: mount target %q has no IpAddress", mtID)
		}
		return nil
	}
	return fmt.Errorf("DescribeMountTargets: mount target %q not in the response", mtID)
}

func (g *efsCliGroup) DescribeMountTargetSecurityGroups(_ context.Context, t *harness.TestContext) error {
	mtID := t.GetString("mount_target_id")
	if mtID == "" {
		return fmt.Errorf("DescribeMountTargetSecurityGroups: no mount target from CreateMountTarget")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "describe-mount-target-security-groups",
		"--mount-target-id", mtID,
	)
	if err != nil {
		return err
	}
	groups, _ := out["SecurityGroups"].([]any)
	if len(groups) != 1 {
		return fmt.Errorf("DescribeMountTargetSecurityGroups: expected the 1 group set at create, got %d", len(groups))
	}
	if got, _ := groups[0].(string); got != "sg-compat-a" {
		return fmt.Errorf("DescribeMountTargetSecurityGroups: expected sg-compat-a, got %q", got)
	}
	return nil
}

func (g *efsCliGroup) ModifyMountTargetSecurityGroups(_ context.Context, t *harness.TestContext) error {
	mtID := t.GetString("mount_target_id")
	if mtID == "" {
		return fmt.Errorf("ModifyMountTargetSecurityGroups: no mount target from CreateMountTarget")
	}
	if _, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "modify-mount-target-security-groups",
		"--mount-target-id", mtID,
		"--security-groups", "sg-compat-b", "sg-compat-c",
	); err != nil {
		return err
	}
	// The mutation is the point of the test, so assert it here rather than
	// leaving it for the Describe test to notice.
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "describe-mount-target-security-groups",
		"--mount-target-id", mtID,
	)
	if err != nil {
		return fmt.Errorf("ModifyMountTargetSecurityGroups: re-describe failed: %w", err)
	}
	groups, _ := out["SecurityGroups"].([]any)
	if len(groups) != 2 {
		return fmt.Errorf("ModifyMountTargetSecurityGroups: expected 2 groups after modify, got %d", len(groups))
	}
	for i, want := range []string{"sg-compat-b", "sg-compat-c"} {
		if got, _ := groups[i].(string); got != want {
			return fmt.Errorf("ModifyMountTargetSecurityGroups: group %d: expected %q, got %q", i, want, got)
		}
	}
	return nil
}

func (g *efsCliGroup) DeleteMountTarget(_ context.Context, t *harness.TestContext) error {
	fsID := t.GetString("file_system_id")
	mtID := t.GetString("mount_target_id")
	if mtID == "" {
		return fmt.Errorf("DeleteMountTarget: no mount target from CreateMountTarget")
	}
	if _, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "delete-mount-target",
		"--mount-target-id", mtID,
	); err != nil {
		return err
	}
	t.Set("mount_target_id", "")

	out, err := awscli.RunOutput(t.Endpoint, t.Region, "efs", "describe-mount-targets",
		"--file-system-id", fsID,
	)
	if err != nil {
		return fmt.Errorf("DeleteMountTarget: re-describe failed: %w", err)
	}
	targets, _ := out["MountTargets"].([]any)
	for _, raw := range targets {
		mt, _ := raw.(map[string]any)
		if id, _ := mt["MountTargetId"].(string); id == mtID {
			return fmt.Errorf("DeleteMountTarget: mount target %q still listed after delete", mtID)
		}
	}
	return nil
}
