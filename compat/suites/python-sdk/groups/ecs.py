"""
groups/ecs.py — ECS compatibility test implementations for the Python suite.
"""

from __future__ import annotations
from lib.harness import TestContext
from lib.clients import make_clients


def _ecs(ctx: TestContext):
    return make_clients(ctx.endpoint, ctx.region)._get("ecs")


# ── ecs-clusters ──────────────────────────────────────────────────────────────

def CreateCluster(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster_name = f"compat-{ctx.run_id}"
    resp = ecs.create_cluster(clusterName=cluster_name)
    cluster = resp.get("cluster", {})
    if not cluster.get("clusterArn"):
        raise AssertionError("CreateCluster: missing clusterArn")
    ctx["ecs_cluster_name"] = cluster_name


def DescribeClusters(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster_name = ctx.get("ecs_cluster_name")
    if not cluster_name:
        raise AssertionError("DescribeClusters: no cluster from CreateCluster")
    resp = ecs.describe_clusters(clusters=[cluster_name])
    if not resp.get("clusters"):
        raise AssertionError("DescribeClusters: no clusters returned")


def ListClusters(ctx: TestContext) -> None:
    _ecs(ctx).list_clusters()


def RegisterTaskDefinition(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    family = f"compat-{ctx.run_id}"
    resp = ecs.register_task_definition(
        family=family,
        networkMode="awsvpc",
        requiresCompatibilities=["FARGATE"],
        cpu="256",
        memory="512",
        containerDefinitions=[
            {
                "name": "app",
                "image": "public.ecr.aws/nginx/nginx:latest",
                "essential": True,
            },
        ],
    )
    td = resp.get("taskDefinition", {})
    if not td.get("taskDefinitionArn"):
        raise AssertionError("RegisterTaskDefinition: missing taskDefinitionArn")
    ctx["ecs_taskdef_arn"] = td["taskDefinitionArn"]


def ListTaskDefinitions(ctx: TestContext) -> None:
    _ecs(ctx).list_task_definitions()


def DeregisterTaskDefinition(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    arn = ctx.get("ecs_taskdef_arn")
    if not arn:
        return
    ecs.deregister_task_definition(taskDefinition=arn)


def DeleteCluster(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    name = ctx.get("ecs_cluster_name")
    if not name:
        return
    ecs.delete_cluster(cluster=name)


# ── ImplMap ───────────────────────────────────────────────────────────────────

IMPLS = {
    "CreateCluster": CreateCluster,
    "DescribeClusters": DescribeClusters,
    "ListClusters": ListClusters,
    "RegisterTaskDefinition": RegisterTaskDefinition,
    "ListTaskDefinitions": ListTaskDefinitions,
    "DeregisterTaskDefinition": DeregisterTaskDefinition,
    "DeleteCluster": DeleteCluster,
    "RunTask": lambda ctx: RunTask(ctx),
    "DescribeTasks": lambda ctx: DescribeTasks(ctx),
    "ListTasks": lambda ctx: ListTasks(ctx),
    "StopTask": lambda ctx: StopTask(ctx),
    "CreateService": lambda ctx: CreateServiceFn(ctx),
    "DescribeServices": lambda ctx: DescribeServicesFn(ctx),
    "ListServices": lambda ctx: ListServicesFn(ctx),
    "UpdateService": lambda ctx: UpdateServiceFn(ctx),
    "DeleteService": lambda ctx: DeleteServiceFn(ctx),
}

SETUP = {
    "ecs-tasks": lambda ctx: _setup_tasks(ctx),
    "ecs-services": lambda ctx: _setup_services(ctx),
}
TEARDOWN = {
    "ecs-clusters": lambda ctx: _teardown_cluster(ctx),
    "ecs-tasks": lambda ctx: _teardown_tasks(ctx),
    "ecs-services": lambda ctx: _teardown_services(ctx),
}


def _teardown_cluster(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    taskdef_arn = ctx.get("ecs_taskdef_arn")
    if taskdef_arn:
        try:
            ecs.deregister_task_definition(taskDefinition=taskdef_arn)
        except Exception:
            pass
    cluster_name = ctx.get("ecs_cluster_name")
    if cluster_name:
        try:
            ecs.delete_cluster(cluster=cluster_name)
        except Exception:
            pass


# ── ecs-tasks ─────────────────────────────────────────────────────────────────

def _setup_tasks(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = f"compat-task-{ctx.run_id}"
    ecs.create_cluster(clusterName=cluster)
    ctx["task_cluster"] = cluster

    family = f"compat-task-{ctx.run_id}"
    resp = ecs.register_task_definition(
        family=family,
        networkMode="awsvpc",
        requiresCompatibilities=["FARGATE"],
        cpu="256",
        memory="512",
        containerDefinitions=[
            {"name": "app", "image": "public.ecr.aws/nginx/nginx:latest", "essential": True},
        ],
    )
    ctx["task_taskdef_arn"] = resp["taskDefinition"]["taskDefinitionArn"]


def _teardown_tasks(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    task_arn = ctx.get("task_arn")
    cluster = ctx.get("task_cluster")
    if task_arn and cluster:
        try:
            ecs.stop_task(cluster=cluster, task=task_arn, reason="teardown")
        except Exception:
            pass
    taskdef = ctx.get("task_taskdef_arn")
    if taskdef:
        try:
            ecs.deregister_task_definition(taskDefinition=taskdef)
        except Exception:
            pass
    if cluster:
        try:
            ecs.delete_cluster(cluster=cluster)
        except Exception:
            pass


def RunTask(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("task_cluster")
    taskdef = ctx.get("task_taskdef_arn")
    if not cluster or not taskdef:
        raise AssertionError("RunTask: missing cluster or task definition from setup")
    resp = ecs.run_task(
        cluster=cluster,
        taskDefinition=taskdef,
        launchType="FARGATE",
        networkConfiguration={
            "awsvpcConfiguration": {
                "subnets": ["subnet-00000000"],
                "assignPublicIp": "DISABLED",
            }
        },
    )
    tasks = resp.get("tasks", [])
    if not tasks:
        raise AssertionError("RunTask: no tasks returned")
    ctx["task_arn"] = tasks[0]["taskArn"]


def DescribeTasks(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("task_cluster")
    task_arn = ctx.get("task_arn")
    if not task_arn:
        raise AssertionError("DescribeTasks: no task from RunTask")
    resp = ecs.describe_tasks(cluster=cluster, tasks=[task_arn])
    if not resp.get("tasks"):
        raise AssertionError("DescribeTasks: no tasks returned")


def ListTasks(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("task_cluster")
    resp = ecs.list_tasks(cluster=cluster)
    if not resp.get("taskArns"):
        raise AssertionError("ListTasks: empty taskArns")


def StopTask(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("task_cluster")
    task_arn = ctx.get("task_arn")
    if not task_arn:
        raise AssertionError("StopTask: no task from RunTask")
    resp = ecs.stop_task(cluster=cluster, task=task_arn, reason="compat test cleanup")
    task = resp.get("task", {})
    if task.get("desiredStatus") != "STOPPED":
        raise AssertionError(
            f"StopTask: expected desiredStatus STOPPED, got {task.get('desiredStatus')}"
        )


# ── ecs-services ──────────────────────────────────────────────────────────────

def _setup_services(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = f"compat-svc-{ctx.run_id}"
    ecs.create_cluster(clusterName=cluster)
    ctx["svc_cluster"] = cluster

    family = f"compat-svc-{ctx.run_id}"
    resp = ecs.register_task_definition(
        family=family,
        networkMode="awsvpc",
        requiresCompatibilities=["FARGATE"],
        cpu="256",
        memory="512",
        containerDefinitions=[
            {"name": "app", "image": "public.ecr.aws/nginx/nginx:latest", "essential": True},
        ],
    )
    ctx["svc_taskdef_arn"] = resp["taskDefinition"]["taskDefinitionArn"]


def _teardown_services(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    svc_name = ctx.get("svc_name")
    cluster = ctx.get("svc_cluster")
    if svc_name and cluster:
        try:
            ecs.update_service(cluster=cluster, service=svc_name, desiredCount=0)
        except Exception:
            pass
        try:
            ecs.delete_service(cluster=cluster, service=svc_name)
        except Exception:
            pass
    taskdef = ctx.get("svc_taskdef_arn")
    if taskdef:
        try:
            ecs.deregister_task_definition(taskDefinition=taskdef)
        except Exception:
            pass
    if cluster:
        try:
            ecs.delete_cluster(cluster=cluster)
        except Exception:
            pass


def CreateServiceFn(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("svc_cluster")
    taskdef = ctx.get("svc_taskdef_arn")
    if not cluster or not taskdef:
        raise AssertionError("CreateService: missing cluster or task definition from setup")
    svc_name = f"compat-svc-{ctx.run_id}"
    resp = ecs.create_service(
        cluster=cluster,
        serviceName=svc_name,
        taskDefinition=taskdef,
        desiredCount=1,
        launchType="FARGATE",
        networkConfiguration={
            "awsvpcConfiguration": {
                "subnets": ["subnet-00000000"],
                "assignPublicIp": "DISABLED",
            }
        },
    )
    svc = resp.get("service", {})
    if not svc.get("serviceArn"):
        raise AssertionError("CreateService: missing serviceArn")
    ctx["svc_name"] = svc_name


def DescribeServicesFn(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("svc_cluster")
    svc_name = ctx.get("svc_name")
    if not svc_name:
        raise AssertionError("DescribeServices: no service from CreateService")
    resp = ecs.describe_services(cluster=cluster, services=[svc_name])
    if not resp.get("services"):
        raise AssertionError("DescribeServices: no services returned")


def ListServicesFn(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("svc_cluster")
    resp = ecs.list_services(cluster=cluster)
    if not resp.get("serviceArns"):
        raise AssertionError("ListServices: empty serviceArns")


def UpdateServiceFn(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("svc_cluster")
    svc_name = ctx.get("svc_name")
    if not svc_name:
        raise AssertionError("UpdateService: no service from CreateService")
    resp = ecs.update_service(cluster=cluster, service=svc_name, desiredCount=2)
    if not resp.get("service"):
        raise AssertionError("UpdateService: missing service in response")


def DeleteServiceFn(ctx: TestContext) -> None:
    ecs = _ecs(ctx)
    cluster = ctx.get("svc_cluster")
    svc_name = ctx.get("svc_name")
    if not svc_name:
        return
    try:
        ecs.update_service(cluster=cluster, service=svc_name, desiredCount=0)
    except Exception:
        pass
    ecs.delete_service(cluster=cluster, service=svc_name)
