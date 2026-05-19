package groups

import (
	"context"
	"fmt"

	"github.com/Neaox/overcast-compat-go-sdk/internal/clients"
	"github.com/Neaox/overcast-compat-go-sdk/internal/harness"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func ECS(c *clients.Clients) ServiceGroup {
	g := &ecsGroup{c: c}
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

type ecsGroup struct{ c *clients.Clients }

func (g *ecsGroup) cl() *ecs.Client { return g.c.ECS() }

func (g *ecsGroup) setupClusters(_ context.Context, _ *harness.TestContext) error { return nil }

func (g *ecsGroup) teardownClusters(ctx context.Context, t *harness.TestContext) error {
	if arn := t.GetString("ecs_taskdef_arn"); arn != "" {
		g.cl().DeregisterTaskDefinition(ctx, &ecs.DeregisterTaskDefinitionInput{ //nolint:errcheck
			TaskDefinition: aws.String(arn),
		})
	}
	if name := t.GetString("ecs_cluster_name"); name != "" {
		g.cl().DeleteCluster(ctx, &ecs.DeleteClusterInput{Cluster: aws.String(name)}) //nolint:errcheck
	}
	return nil
}

func (g *ecsGroup) CreateCluster(ctx context.Context, t *harness.TestContext) error {
	name := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().CreateCluster(ctx, &ecs.CreateClusterInput{
		ClusterName: aws.String(name),
	})
	if err != nil {
		return err
	}
	if resp.Cluster == nil || resp.Cluster.ClusterArn == nil {
		return fmt.Errorf("CreateCluster: missing clusterArn")
	}
	t.Set("ecs_cluster_name", name)
	return nil
}

func (g *ecsGroup) DescribeClusters(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ecs_cluster_name")
	if name == "" {
		return fmt.Errorf("DescribeClusters: no cluster from CreateCluster")
	}
	resp, err := g.cl().DescribeClusters(ctx, &ecs.DescribeClustersInput{
		Clusters: []string{name},
	})
	if err != nil {
		return err
	}
	if len(resp.Clusters) == 0 {
		return fmt.Errorf("DescribeClusters: no clusters returned")
	}
	return nil
}

func (g *ecsGroup) ListClusters(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListClusters(ctx, &ecs.ListClustersInput{})
	return err
}

func (g *ecsGroup) RegisterTaskDefinition(ctx context.Context, t *harness.TestContext) error {
	family := fmt.Sprintf("compat-%s", t.RunID)
	resp, err := g.cl().RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{
				Name:      aws.String("app"),
				Image:     aws.String("public.ecr.aws/nginx/nginx:latest"),
				Essential: aws.Bool(true),
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.TaskDefinition == nil || resp.TaskDefinition.TaskDefinitionArn == nil {
		return fmt.Errorf("RegisterTaskDefinition: missing taskDefinitionArn")
	}
	t.Set("ecs_taskdef_arn", *resp.TaskDefinition.TaskDefinitionArn)
	return nil
}

func (g *ecsGroup) ListTaskDefinitions(ctx context.Context, t *harness.TestContext) error {
	_, err := g.cl().ListTaskDefinitions(ctx, &ecs.ListTaskDefinitionsInput{})
	return err
}

func (g *ecsGroup) DeregisterTaskDefinition(ctx context.Context, t *harness.TestContext) error {
	arn := t.GetString("ecs_taskdef_arn")
	if arn == "" {
		return nil
	}
	_, err := g.cl().DeregisterTaskDefinition(ctx, &ecs.DeregisterTaskDefinitionInput{
		TaskDefinition: aws.String(arn),
	})
	return err
}

func (g *ecsGroup) DeleteCluster(ctx context.Context, t *harness.TestContext) error {
	name := t.GetString("ecs_cluster_name")
	if name == "" {
		return nil
	}
	_, err := g.cl().DeleteCluster(ctx, &ecs.DeleteClusterInput{
		Cluster: aws.String(name),
	})
	return err
}

// ── ecs-tasks ──────────────────────────────────────────────────────────────

func (g *ecsGroup) setupTasks(ctx context.Context, t *harness.TestContext) error {
	cluster := fmt.Sprintf("compat-task-%s", t.RunID)
	_, err := g.cl().CreateCluster(ctx, &ecs.CreateClusterInput{
		ClusterName: aws.String(cluster),
	})
	if err != nil {
		return err
	}
	t.Set("task_cluster", cluster)

	family := fmt.Sprintf("compat-task-%s", t.RunID)
	resp, err := g.cl().RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{
				Name:      aws.String("app"),
				Image:     aws.String("public.ecr.aws/nginx/nginx:latest"),
				Essential: aws.Bool(true),
			},
		},
	})
	if err != nil {
		return err
	}
	t.Set("task_taskdef_arn", *resp.TaskDefinition.TaskDefinitionArn)
	return nil
}

func (g *ecsGroup) teardownTasks(ctx context.Context, t *harness.TestContext) error {
	if taskArn := t.GetString("task_arn"); taskArn != "" {
		if cluster := t.GetString("task_cluster"); cluster != "" {
			g.cl().StopTask(ctx, &ecs.StopTaskInput{ //nolint:errcheck
				Cluster: aws.String(cluster),
				Task:    aws.String(taskArn),
				Reason:  aws.String("teardown"),
			})
		}
	}
	if arn := t.GetString("task_taskdef_arn"); arn != "" {
		g.cl().DeregisterTaskDefinition(ctx, &ecs.DeregisterTaskDefinitionInput{ //nolint:errcheck
			TaskDefinition: aws.String(arn),
		})
	}
	if cluster := t.GetString("task_cluster"); cluster != "" {
		g.cl().DeleteCluster(ctx, &ecs.DeleteClusterInput{Cluster: aws.String(cluster)}) //nolint:errcheck
	}
	return nil
}

func (g *ecsGroup) RunTask(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	taskDef := t.GetString("task_taskdef_arn")
	if cluster == "" || taskDef == "" {
		return fmt.Errorf("RunTask: missing cluster or task definition from setup")
	}
	resp, err := g.cl().RunTask(ctx, &ecs.RunTaskInput{
		Cluster:        aws.String(cluster),
		TaskDefinition: aws.String(taskDef),
		LaunchType:     ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        []string{"subnet-00000000"},
				AssignPublicIp: ecstypes.AssignPublicIpDisabled,
			},
		},
	})
	if err != nil {
		return err
	}
	if len(resp.Tasks) == 0 || resp.Tasks[0].TaskArn == nil {
		return fmt.Errorf("RunTask: no tasks returned")
	}
	t.Set("task_arn", *resp.Tasks[0].TaskArn)
	return nil
}

func (g *ecsGroup) DescribeTasks(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	taskArn := t.GetString("task_arn")
	if taskArn == "" {
		return fmt.Errorf("DescribeTasks: no task from RunTask")
	}
	resp, err := g.cl().DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: aws.String(cluster),
		Tasks:   []string{taskArn},
	})
	if err != nil {
		return err
	}
	if len(resp.Tasks) == 0 {
		return fmt.Errorf("DescribeTasks: no tasks returned")
	}
	return nil
}

func (g *ecsGroup) ListTasks(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	resp, err := g.cl().ListTasks(ctx, &ecs.ListTasksInput{
		Cluster: aws.String(cluster),
	})
	if err != nil {
		return err
	}
	if len(resp.TaskArns) == 0 {
		return fmt.Errorf("ListTasks: empty taskArns")
	}
	return nil
}

func (g *ecsGroup) StopTask(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("task_cluster")
	taskArn := t.GetString("task_arn")
	if taskArn == "" {
		return fmt.Errorf("StopTask: no task from RunTask")
	}
	resp, err := g.cl().StopTask(ctx, &ecs.StopTaskInput{
		Cluster: aws.String(cluster),
		Task:    aws.String(taskArn),
		Reason:  aws.String("compat test cleanup"),
	})
	if err != nil {
		return err
	}
	if resp.Task == nil || resp.Task.DesiredStatus == nil || *resp.Task.DesiredStatus != "STOPPED" {
		return fmt.Errorf("StopTask: expected desiredStatus STOPPED")
	}
	return nil
}

// ── ecs-services ───────────────────────────────────────────────────────────

func (g *ecsGroup) setupServices(ctx context.Context, t *harness.TestContext) error {
	cluster := fmt.Sprintf("compat-svc-%s", t.RunID)
	_, err := g.cl().CreateCluster(ctx, &ecs.CreateClusterInput{
		ClusterName: aws.String(cluster),
	})
	if err != nil {
		return err
	}
	t.Set("svc_cluster", cluster)

	family := fmt.Sprintf("compat-svc-%s", t.RunID)
	resp, err := g.cl().RegisterTaskDefinition(ctx, &ecs.RegisterTaskDefinitionInput{
		Family:                  aws.String(family),
		NetworkMode:             ecstypes.NetworkModeAwsvpc,
		RequiresCompatibilities: []ecstypes.Compatibility{ecstypes.CompatibilityFargate},
		Cpu:                     aws.String("256"),
		Memory:                  aws.String("512"),
		ContainerDefinitions: []ecstypes.ContainerDefinition{
			{
				Name:      aws.String("app"),
				Image:     aws.String("public.ecr.aws/nginx/nginx:latest"),
				Essential: aws.Bool(true),
			},
		},
	})
	if err != nil {
		return err
	}
	t.Set("svc_taskdef_arn", *resp.TaskDefinition.TaskDefinitionArn)
	return nil
}

func (g *ecsGroup) teardownServices(ctx context.Context, t *harness.TestContext) error {
	svcName := t.GetString("svc_name")
	cluster := t.GetString("svc_cluster")
	if svcName != "" && cluster != "" {
		g.cl().UpdateService(ctx, &ecs.UpdateServiceInput{ //nolint:errcheck
			Cluster:      aws.String(cluster),
			Service:      aws.String(svcName),
			DesiredCount: aws.Int32(0),
		})
		g.cl().DeleteService(ctx, &ecs.DeleteServiceInput{ //nolint:errcheck
			Cluster: aws.String(cluster),
			Service: aws.String(svcName),
		})
	}
	if arn := t.GetString("svc_taskdef_arn"); arn != "" {
		g.cl().DeregisterTaskDefinition(ctx, &ecs.DeregisterTaskDefinitionInput{ //nolint:errcheck
			TaskDefinition: aws.String(arn),
		})
	}
	if cluster != "" {
		g.cl().DeleteCluster(ctx, &ecs.DeleteClusterInput{Cluster: aws.String(cluster)}) //nolint:errcheck
	}
	return nil
}

func (g *ecsGroup) CreateService(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	taskDef := t.GetString("svc_taskdef_arn")
	if cluster == "" || taskDef == "" {
		return fmt.Errorf("CreateService: missing cluster or task definition from setup")
	}
	svcName := fmt.Sprintf("compat-svc-%s", t.RunID)
	resp, err := g.cl().CreateService(ctx, &ecs.CreateServiceInput{
		Cluster:        aws.String(cluster),
		ServiceName:    aws.String(svcName),
		TaskDefinition: aws.String(taskDef),
		DesiredCount:   aws.Int32(1),
		LaunchType:     ecstypes.LaunchTypeFargate,
		NetworkConfiguration: &ecstypes.NetworkConfiguration{
			AwsvpcConfiguration: &ecstypes.AwsVpcConfiguration{
				Subnets:        []string{"subnet-00000000"},
				AssignPublicIp: ecstypes.AssignPublicIpDisabled,
			},
		},
	})
	if err != nil {
		return err
	}
	if resp.Service == nil || resp.Service.ServiceArn == nil {
		return fmt.Errorf("CreateService: missing serviceArn")
	}
	t.Set("svc_name", svcName)
	return nil
}

func (g *ecsGroup) DescribeServices(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	svcName := t.GetString("svc_name")
	if svcName == "" {
		return fmt.Errorf("DescribeServices: no service from CreateService")
	}
	resp, err := g.cl().DescribeServices(ctx, &ecs.DescribeServicesInput{
		Cluster:  aws.String(cluster),
		Services: []string{svcName},
	})
	if err != nil {
		return err
	}
	if len(resp.Services) == 0 {
		return fmt.Errorf("DescribeServices: no services returned")
	}
	return nil
}

func (g *ecsGroup) ListServices(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	resp, err := g.cl().ListServices(ctx, &ecs.ListServicesInput{
		Cluster: aws.String(cluster),
	})
	if err != nil {
		return err
	}
	if len(resp.ServiceArns) == 0 {
		return fmt.Errorf("ListServices: empty serviceArns")
	}
	return nil
}

func (g *ecsGroup) UpdateService(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	svcName := t.GetString("svc_name")
	if svcName == "" {
		return fmt.Errorf("UpdateService: no service from CreateService")
	}
	resp, err := g.cl().UpdateService(ctx, &ecs.UpdateServiceInput{
		Cluster:      aws.String(cluster),
		Service:      aws.String(svcName),
		DesiredCount: aws.Int32(2),
	})
	if err != nil {
		return err
	}
	if resp.Service == nil {
		return fmt.Errorf("UpdateService: missing service in response")
	}
	return nil
}

func (g *ecsGroup) DeleteService(ctx context.Context, t *harness.TestContext) error {
	cluster := t.GetString("svc_cluster")
	svcName := t.GetString("svc_name")
	if svcName == "" {
		return nil
	}
	g.cl().UpdateService(ctx, &ecs.UpdateServiceInput{ //nolint:errcheck
		Cluster:      aws.String(cluster),
		Service:      aws.String(svcName),
		DesiredCount: aws.Int32(0),
	})
	_, err := g.cl().DeleteService(ctx, &ecs.DeleteServiceInput{
		Cluster: aws.String(cluster),
		Service: aws.String(svcName),
	})
	return err
}
