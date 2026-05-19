package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-cli/internal/awscli"
	"github.com/Neaox/overcast-compat-cli/internal/harness"
)

// ECS returns the ECS service group.
func ECS() ServiceGroup {
	g := &ecsCliGroup{}
	return ServiceGroup{
		Impls: map[string]harness.TestFn{
			"CreateCluster":            g.CreateCluster,
			"DescribeClusters":         g.DescribeClusters,
			"ListClusters":             g.ListClusters,
			"RegisterTaskDefinition":   g.RegisterTaskDefinition,
			"ListTaskDefinitions":      g.ListTaskDefinitions,
			"DeregisterTaskDefinition": g.DeregisterTaskDefinition,
			"DeleteCluster":            g.DeleteCluster,
			"RunTask":                  g.RunTask,
			"DescribeTasks":            g.DescribeTasks,
			"ListTasks":                g.ListTasks,
			"StopTask":                 g.StopTask,
			"CreateService":            g.CreateService,
			"DescribeServices":         g.DescribeServices,
			"ListServices":             g.ListServices,
			"UpdateService":            g.UpdateService,
			"DeleteService":            g.DeleteService,
		},
		Setup: map[string]func(context.Context, *harness.TestContext) error{
			"ecs-clusters": g.setupClusters,
			"ecs-tasks":    g.setupTasks,
			"ecs-services": g.setupServices,
		},
		Teardown: map[string]func(context.Context, *harness.TestContext) error{
			"ecs-clusters": g.teardownClusters,
			"ecs-tasks":    g.teardownTasks,
			"ecs-services": g.teardownServices,
		},
	}
}

type ecsCliGroup struct{}

func (g *ecsCliGroup) setupClusters(_ context.Context, _ *harness.TestContext) error { return nil }
func (g *ecsCliGroup) teardownClusters(_ context.Context, t *harness.TestContext) error {
	if name := t.GetString("ecs_cluster"); name != "" {
		awscli.Run(t.Endpoint, t.Region, "ecs", "delete-cluster", "--cluster", name) //nolint:errcheck
	}
	return nil
}

func (g *ecsCliGroup) CreateCluster(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "create-cluster",
		"--cluster-name", name,
	)
	if err != nil {
		return err
	}
	cluster, _ := out["cluster"].(map[string]interface{})
	arn, _ := cluster["clusterArn"].(string)
	if arn == "" {
		return fmt.Errorf("CreateCluster: missing clusterArn")
	}
	t.Set("ecs_cluster", name)
	return nil
}

func (g *ecsCliGroup) DescribeClusters(_ context.Context, t *harness.TestContext) error {
	name := t.GetString("ecs_cluster")
	if name == "" {
		return fmt.Errorf("DescribeClusters: no cluster from CreateCluster")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "describe-clusters",
		"--clusters", name,
	)
	if err != nil {
		return err
	}
	clusters, _ := out["clusters"].([]interface{})
	if len(clusters) == 0 {
		return fmt.Errorf("DescribeClusters: no clusters returned")
	}
	return nil
}

func (g *ecsCliGroup) ListClusters(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "list-clusters")
	return err
}

func (g *ecsCliGroup) RegisterTaskDefinition(_ context.Context, t *harness.TestContext) error {
	family := fmt.Sprintf("compat-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "register-task-definition",
		"--family", family,
		"--network-mode", "bridge",
		"--container-definitions", `[{"name":"app","image":"public.ecr.aws/nginx/nginx:latest","memory":128}]`,
	)
	if err != nil {
		return err
	}
	td, _ := out["taskDefinition"].(map[string]interface{})
	arn, _ := td["taskDefinitionArn"].(string)
	if arn == "" {
		return fmt.Errorf("RegisterTaskDefinition: missing taskDefinitionArn")
	}
	t.Set("task_def_arn", arn)
	return nil
}

func (g *ecsCliGroup) ListTaskDefinitions(_ context.Context, t *harness.TestContext) error {
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "list-task-definitions")
	return err
}

func (g *ecsCliGroup) DeregisterTaskDefinition(_ context.Context, t *harness.TestContext) error {
	arn := t.GetString("task_def_arn")
	if arn == "" {
		return fmt.Errorf("DeregisterTaskDefinition: no task_def_arn from RegisterTaskDefinition")
	}
	return awscli.Run(t.Endpoint, t.Region, "ecs", "deregister-task-definition",
		"--task-definition", arn,
	)
}

func (g *ecsCliGroup) DeleteCluster(_ context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-del-%s", t.RunID)
	awscli.Run(t.Endpoint, t.Region, "ecs", "create-cluster", "--cluster-name", name) //nolint:errcheck
	return awscli.Run(t.Endpoint, t.Region, "ecs", "delete-cluster",
		"--cluster", name,
	)
}

// ── ecs-tasks ──────────────────────────────────────────────────────────────

func (g *ecsCliGroup) setupTasks(_ context.Context, t *harness.TestContext) error {
	cluster := fmt.Sprintf("compat-task-%s", t.RunID)
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "create-cluster", "--cluster-name", cluster)
	if err != nil {
		return err
	}
	t.Set("task_cluster", cluster)

	family := fmt.Sprintf("compat-task-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "register-task-definition",
		"--family", family,
		"--network-mode", "awsvpc",
		"--requires-compatibilities", "FARGATE",
		"--cpu", "256",
		"--memory", "512",
		"--container-definitions", `[{"name":"app","image":"public.ecr.aws/nginx/nginx:latest","essential":true}]`,
	)
	if err != nil {
		return err
	}
	td, _ := out["taskDefinition"].(map[string]interface{})
	arn, _ := td["taskDefinitionArn"].(string)
	t.Set("task_taskdef_arn", arn)
	return nil
}

func (g *ecsCliGroup) teardownTasks(_ context.Context, t *harness.TestContext) error {
	if taskArn := t.GetString("task_arn"); taskArn != "" {
		if cluster := t.GetString("task_cluster"); cluster != "" {
			awscli.Run(t.Endpoint, t.Region, "ecs", "stop-task", "--cluster", cluster, "--task", taskArn, "--reason", "teardown") //nolint:errcheck
		}
	}
	if arn := t.GetString("task_taskdef_arn"); arn != "" {
		awscli.Run(t.Endpoint, t.Region, "ecs", "deregister-task-definition", "--task-definition", arn) //nolint:errcheck
	}
	if cluster := t.GetString("task_cluster"); cluster != "" {
		awscli.Run(t.Endpoint, t.Region, "ecs", "delete-cluster", "--cluster", cluster) //nolint:errcheck
	}
	return nil
}

func (g *ecsCliGroup) RunTask(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	taskDef := t.GetString("task_taskdef_arn")
	if cluster == "" || taskDef == "" {
		return fmt.Errorf("RunTask: missing cluster or task definition from setup")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "run-task",
		"--cluster", cluster,
		"--task-definition", taskDef,
		"--launch-type", "FARGATE",
		"--network-configuration", `{"awsvpcConfiguration":{"subnets":["subnet-00000000"],"assignPublicIp":"DISABLED"}}`,
	)
	if err != nil {
		return err
	}
	tasks, _ := out["tasks"].([]interface{})
	if len(tasks) == 0 {
		return fmt.Errorf("RunTask: no tasks returned")
	}
	task, _ := tasks[0].(map[string]interface{})
	taskArn, _ := task["taskArn"].(string)
	if taskArn == "" {
		return fmt.Errorf("RunTask: missing taskArn")
	}
	t.Set("task_arn", taskArn)
	return nil
}

func (g *ecsCliGroup) DescribeTasks(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	taskArn := t.GetString("task_arn")
	if taskArn == "" {
		return fmt.Errorf("DescribeTasks: no task from RunTask")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "describe-tasks",
		"--cluster", cluster,
		"--tasks", taskArn,
	)
	if err != nil {
		return err
	}
	tasks, _ := out["tasks"].([]interface{})
	if len(tasks) == 0 {
		return fmt.Errorf("DescribeTasks: no tasks returned")
	}
	return nil
}

func (g *ecsCliGroup) ListTasks(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "list-tasks",
		"--cluster", cluster,
	)
	if err != nil {
		return err
	}
	arns, _ := out["taskArns"].([]interface{})
	if len(arns) == 0 {
		return fmt.Errorf("ListTasks: empty taskArns")
	}
	return nil
}

func (g *ecsCliGroup) StopTask(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	taskArn := t.GetString("task_arn")
	if taskArn == "" {
		return fmt.Errorf("StopTask: no task from RunTask")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "stop-task",
		"--cluster", cluster,
		"--task", taskArn,
		"--reason", "compat test cleanup",
	)
	if err != nil {
		return err
	}
	task, _ := out["task"].(map[string]interface{})
	desired, _ := task["desiredStatus"].(string)
	if desired != "STOPPED" {
		return fmt.Errorf("StopTask: expected desiredStatus STOPPED, got %s", desired)
	}
	return nil
}

// ── ecs-services ───────────────────────────────────────────────────────────

func (g *ecsCliGroup) setupServices(_ context.Context, t *harness.TestContext) error {
	cluster := fmt.Sprintf("compat-svc-%s", t.RunID)
	_, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "create-cluster", "--cluster-name", cluster)
	if err != nil {
		return err
	}
	t.Set("svc_cluster", cluster)

	family := fmt.Sprintf("compat-svc-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "register-task-definition",
		"--family", family,
		"--network-mode", "awsvpc",
		"--requires-compatibilities", "FARGATE",
		"--cpu", "256",
		"--memory", "512",
		"--container-definitions", `[{"name":"app","image":"public.ecr.aws/nginx/nginx:latest","essential":true}]`,
	)
	if err != nil {
		return err
	}
	td, _ := out["taskDefinition"].(map[string]interface{})
	arn, _ := td["taskDefinitionArn"].(string)
	t.Set("svc_taskdef_arn", arn)
	return nil
}

func (g *ecsCliGroup) teardownServices(_ context.Context, t *harness.TestContext) error {
	svcName := t.GetString("svc_name")
	cluster := t.GetString("svc_cluster")
	if svcName != "" && cluster != "" {
		awscli.Run(t.Endpoint, t.Region, "ecs", "update-service", "--cluster", cluster, "--service", svcName, "--desired-count", "0") //nolint:errcheck
		awscli.Run(t.Endpoint, t.Region, "ecs", "delete-service", "--cluster", cluster, "--service", svcName)                         //nolint:errcheck
	}
	if arn := t.GetString("svc_taskdef_arn"); arn != "" {
		awscli.Run(t.Endpoint, t.Region, "ecs", "deregister-task-definition", "--task-definition", arn) //nolint:errcheck
	}
	if cluster != "" {
		awscli.Run(t.Endpoint, t.Region, "ecs", "delete-cluster", "--cluster", cluster) //nolint:errcheck
	}
	return nil
}

func (g *ecsCliGroup) CreateService(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	taskDef := t.GetString("svc_taskdef_arn")
	if cluster == "" || taskDef == "" {
		return fmt.Errorf("CreateService: missing cluster or task definition from setup")
	}
	svcName := fmt.Sprintf("compat-svc-%s", t.RunID)
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "create-service",
		"--cluster", cluster,
		"--service-name", svcName,
		"--task-definition", taskDef,
		"--desired-count", "1",
		"--launch-type", "FARGATE",
		"--network-configuration", `{"awsvpcConfiguration":{"subnets":["subnet-00000000"],"assignPublicIp":"DISABLED"}}`,
	)
	if err != nil {
		return err
	}
	svc, _ := out["service"].(map[string]interface{})
	if svc["serviceArn"] == nil {
		return fmt.Errorf("CreateService: missing serviceArn")
	}
	t.Set("svc_name", svcName)
	return nil
}

func (g *ecsCliGroup) DescribeServices(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	svcName := t.GetString("svc_name")
	if svcName == "" {
		return fmt.Errorf("DescribeServices: no service from CreateService")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "describe-services",
		"--cluster", cluster,
		"--services", svcName,
	)
	if err != nil {
		return err
	}
	services, _ := out["services"].([]interface{})
	if len(services) == 0 {
		return fmt.Errorf("DescribeServices: no services returned")
	}
	return nil
}

func (g *ecsCliGroup) ListServices(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "list-services",
		"--cluster", cluster,
	)
	if err != nil {
		return err
	}
	arns, _ := out["serviceArns"].([]interface{})
	if len(arns) == 0 {
		return fmt.Errorf("ListServices: empty serviceArns")
	}
	return nil
}

func (g *ecsCliGroup) UpdateService(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	svcName := t.GetString("svc_name")
	if svcName == "" {
		return fmt.Errorf("UpdateService: no service from CreateService")
	}
	out, err := awscli.RunOutput(t.Endpoint, t.Region, "ecs", "update-service",
		"--cluster", cluster,
		"--service", svcName,
		"--desired-count", "2",
	)
	if err != nil {
		return err
	}
	svc, _ := out["service"].(map[string]interface{})
	if svc == nil {
		return fmt.Errorf("UpdateService: missing service in response")
	}
	return nil
}

func (g *ecsCliGroup) DeleteService(_ context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	svcName := t.GetString("svc_name")
	if svcName == "" {
		return nil
	}
	awscli.Run(t.Endpoint, t.Region, "ecs", "update-service", "--cluster", cluster, "--service", svcName, "--desired-count", "0") //nolint:errcheck
	return awscli.Run(t.Endpoint, t.Region, "ecs", "delete-service",
		"--cluster", cluster,
		"--service", svcName,
	)
}
