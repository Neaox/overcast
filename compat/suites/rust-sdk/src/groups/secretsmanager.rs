use std::collections::HashMap;
use std::sync::Arc;

use aws_sdk_secretsmanager::types::FilterNameStringType;

use crate::clients::AwsClients;
use crate::groups::ServiceGroup;
use crate::harness::{TestContext, TestFn};

pub struct SecretsManagerGroup {
    clients: Arc<AwsClients>,
}

impl SecretsManagerGroup {
    pub fn new(clients: Arc<AwsClients>) -> Self {
        Self { clients }
    }
}

impl ServiceGroup for SecretsManagerGroup {
    fn impls(&self) -> HashMap<String, TestFn> {
        let mut impls: HashMap<String, TestFn> = HashMap::new();

        let clients = self.clients.clone();
        impls.insert(
            "CreateSecret".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-sm-crud-create", ctx.run_id.as_ref());
                    let response = clients
                        .secretsmanager()
                        .create_secret()
                        .name(&name)
                        .secret_string("create-test-value")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let arn = response.arn().unwrap_or_default();
                    if arn.is_empty() {
                        return Err("CreateSecret: ARN missing".to_string());
                    }
                    let _ = clients
                        .secretsmanager()
                        .delete_secret()
                        .secret_id(&name)
                        .force_delete_without_recovery(true)
                        .send()
                        .await;
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetSecretValue".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    let response = clients
                        .secretsmanager()
                        .get_secret_value()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let secret_string = response.secret_string().unwrap_or_default();
                    (secret_string == "initial-value")
                        .then_some(())
                        .ok_or_else(|| {
                            format!(
                                "GetSecretValue: expected 'initial-value', got '{}'",
                                secret_string
                            )
                        })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DescribeSecret".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    let response = clients
                        .secretsmanager()
                        .describe_secret()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let secret_name = response.name().unwrap_or_default();
                    (secret_name == name)
                        .then_some(())
                        .ok_or_else(|| "DescribeSecret: name mismatch".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "PutSecretValue".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    let response = clients
                        .secretsmanager()
                        .put_secret_value()
                        .secret_id(&name)
                        .secret_string("updated-value")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.version_id().unwrap_or_default().is_empty() {
                        return Err("PutSecretValue: versionId missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListSecretVersionIds".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    let response = clients
                        .secretsmanager()
                        .list_secret_version_ids()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    (response.versions().len() >= 2)
                        .then_some(())
                        .ok_or_else(|| "ListSecretVersionIds: expected >= 2 versions".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "UpdateSecret".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    clients
                        .secretsmanager()
                        .update_secret()
                        .secret_id(&name)
                        .description("updated desc")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .secretsmanager()
                        .describe_secret()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let desc = response.description().unwrap_or_default();
                    (desc == "updated desc")
                        .then_some(())
                        .ok_or_else(|| {
                            format!(
                                "UpdateSecret: expected description 'updated desc', got '{}'",
                                desc
                            )
                        })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "TagResource".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    let tag_project = aws_sdk_secretsmanager::types::Tag::builder()
                        .key("project")
                        .value("overcast")
                        .build();
                    let tag_env = aws_sdk_secretsmanager::types::Tag::builder()
                        .key("env")
                        .value("test")
                        .build();
                    clients
                        .secretsmanager()
                        .tag_resource()
                        .secret_id(&name)
                        .tags(tag_project)
                        .tags(tag_env)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .secretsmanager()
                        .describe_secret()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let tags = response.tags();
                    let has_project = tags.iter().any(|tag| {
                        tag.key().unwrap_or_default() == "project"
                            && tag.value().unwrap_or_default() == "overcast"
                    });
                    has_project
                        .then_some(())
                        .ok_or_else(|| "TagResource: project=overcast tag not found".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "UntagResource".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    clients
                        .secretsmanager()
                        .untag_resource()
                        .secret_id(&name)
                        .tag_keys("env")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .secretsmanager()
                        .describe_secret()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let tags = response.tags();
                    let has_env = tags
                        .iter()
                        .any(|tag| tag.key().unwrap_or_default() == "env");
                    (!has_env)
                        .then_some(())
                        .ok_or_else(|| "UntagResource: env tag still present".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetRandomPassword".to_string(),
            Arc::new(move |_ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let response = clients
                        .secretsmanager()
                        .get_random_password()
                        .password_length(20)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let password = response.random_password().unwrap_or_default();
                    (password.len() >= 20)
                        .then_some(())
                        .ok_or_else(|| {
                            format!(
                                "GetRandomPassword: expected length >= 20, got {}",
                                password.len()
                            )
                        })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "BatchGetSecretValue".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    let filter = aws_sdk_secretsmanager::types::Filter::builder()
                        .key(FilterNameStringType::Name)
                        .values(name.clone())
                        .build();
                    let response = clients
                        .secretsmanager()
                        .batch_get_secret_value()
                        .filters(filter)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let values = response.secret_values();
                    (!values.is_empty())
                        .then_some(())
                        .ok_or_else(|| {
                            "BatchGetSecretValue: no secret values returned".to_string()
                        })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListSecrets".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    let response = clients
                        .secretsmanager()
                        .list_secrets()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .secret_list()
                        .iter()
                        .any(|item| item.name().unwrap_or_default() == name);
                    found.then_some(()).ok_or_else(|| {
                        format!(
                            "ListSecrets: secret {} not found (runId={})",
                            name,
                            ctx.run_id
                        )
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DeleteSecret".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smCrudSecret")
                        .ok_or_else(|| "smCrudSecret not set".to_string())?;
                    clients
                        .secretsmanager()
                        .delete_secret()
                        .secret_id(&name)
                        .force_delete_without_recovery(true)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .secretsmanager()
                        .list_secrets()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .secret_list()
                        .iter()
                        .any(|item| item.name().unwrap_or_default() == name);
                    (!found).then_some(()).ok_or_else(|| {
                        format!(
                            "DeleteSecret: secret {} still present after deletion",
                            name
                        )
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "RotateSecret".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smRotateSecret")
                        .ok_or_else(|| "smRotateSecret not set".to_string())?;
                    let response = clients
                        .secretsmanager()
                        .rotate_secret()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.arn().unwrap_or_default().is_empty() {
                        return Err("RotateSecret: ARN missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "CancelRotateSecret".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = ctx
                        .get("smRotateSecret")
                        .ok_or_else(|| "smRotateSecret not set".to_string())?;
                    let response = clients
                        .secretsmanager()
                        .cancel_rotate_secret()
                        .secret_id(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.arn().unwrap_or_default().is_empty() {
                        return Err("CancelRotateSecret: ARN missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        impls
    }

    fn setups(&self) -> HashMap<String, TestFn> {
        let mut setups: HashMap<String, TestFn> = HashMap::new();

        let clients = self.clients.clone();
        setups.insert(
            "secretsmanager-crud".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-sm-crud", ctx.run_id.as_ref());
                    clients
                        .secretsmanager()
                        .create_secret()
                        .name(&name)
                        .secret_string("initial-value")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    ctx.set("smCrudSecret", name);
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "secretsmanager-rotate".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-sm-rotate", ctx.run_id.as_ref());
                    clients
                        .secretsmanager()
                        .create_secret()
                        .name(&name)
                        .secret_string("rotate-test-value")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    ctx.set("smRotateSecret", name);
                    Ok(())
                })
            }),
        );

        setups
    }

    fn teardowns(&self) -> HashMap<String, TestFn> {
        let mut teardowns: HashMap<String, TestFn> = HashMap::new();

        let clients = self.clients.clone();
        teardowns.insert(
            "secretsmanager-crud".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(name) = ctx.get("smCrudSecret") {
                        let _ = clients
                            .secretsmanager()
                            .delete_secret()
                            .secret_id(&name)
                            .force_delete_without_recovery(true)
                            .send()
                            .await;
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "secretsmanager-rotate".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    if let Some(name) = ctx.get("smRotateSecret") {
                        let _ = clients
                            .secretsmanager()
                            .delete_secret()
                            .secret_id(&name)
                            .force_delete_without_recovery(true)
                            .send()
                            .await;
                    }
                    Ok(())
                })
            }),
        );

        teardowns
    }
}
