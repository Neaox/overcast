* [ecs/cloudformation] forced ECS deployments launch replacement tasks with current Secrets Manager and SSM values, including CDK nonce-driven deployments
* [cloudformation] no-op stack updates no longer retrieve dynamic secret references for unchanged resources, matching AWS's intentionally stale resource behavior
