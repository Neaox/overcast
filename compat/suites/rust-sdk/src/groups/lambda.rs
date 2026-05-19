use std::collections::HashMap;
use std::sync::Arc;

use aws_sdk_lambda::primitives::Blob;
use aws_sdk_lambda::types::{
    FunctionCode, InvocationType, LayerVersionContentInput, ResponseStreamingInvocationType,
    Runtime,
};

use crate::clients::AwsClients;
use crate::groups::ServiceGroup;
use crate::harness::{TestContext, TestFn};

pub struct LambdaGroup {
    clients: Arc<AwsClients>,
}

impl LambdaGroup {
    pub fn new(clients: Arc<AwsClients>) -> Self {
        Self { clients }
    }
}

impl ServiceGroup for LambdaGroup {
    fn impls(&self) -> HashMap<String, TestFn> {
        let mut impls: HashMap<String, TestFn> = HashMap::new();

        // ── lambda-crud ────────────────────────────────────────────────────

        let clients = self.clients.clone();
        impls.insert(
            "CreateFunction".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn", ctx.run_id.as_ref());
                    let role = "arn:aws:iam::000000000000:role/lambda-exec";
                    let response = clients
                        .lambda()
                        .create_function()
                        .function_name(&name)
                        .runtime(Runtime::Nodejs18x)
                        .handler("index.handler")
                        .role(role)
                        .code(
                            FunctionCode::builder()
                                .zip_file(Blob::new(dummy_zip()))
                                .build(),
                        )
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.function_arn().unwrap_or_default().is_empty() {
                        return Err("CreateFunction: FunctionArn missing".to_string());
                    }
                    if response.function_name().unwrap_or_default() != name {
                        return Err(format!(
                            "CreateFunction: expected FunctionName={name}, got {}",
                            response.function_name().unwrap_or_default()
                        ));
                    }
                    if response.code_sha256().unwrap_or_default().is_empty() {
                        return Err("CreateFunction: CodeSha256 missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetFunction".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .get_function()
                        .function_name(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let config = response
                        .configuration()
                        .ok_or_else(|| "GetFunction: Configuration missing".to_string())?;
                    if config.function_arn().unwrap_or_default().is_empty() {
                        return Err("GetFunction: FunctionArn missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListFunctions".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .list_functions()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .functions()
                        .iter()
                        .any(|f| f.function_name().unwrap_or_default() == name);
                    found
                        .then_some(())
                        .ok_or_else(|| format!("ListFunctions: {name} not found"))
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "UpdateFunctionCode".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .update_function_code()
                        .function_name(&name)
                        .zip_file(Blob::new(dummy_zip()))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.function_arn().unwrap_or_default().is_empty() {
                        return Err("UpdateFunctionCode: FunctionArn missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "UpdateFunctionConfiguration".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn", ctx.run_id.as_ref());
                    let mut env_vars = HashMap::new();
                    env_vars.insert("LOG_LEVEL".to_string(), "debug".to_string());
                    let response = clients
                        .lambda()
                        .update_function_configuration()
                        .function_name(&name)
                        .timeout(30)
                        .memory_size(256)
                        .environment(
                            aws_sdk_lambda::types::Environment::builder()
                                .set_variables(Some(env_vars))
                                .build(),
                        )
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.timeout().unwrap_or_default() != 30 {
                        return Err(format!(
                            "UpdateFunctionConfiguration: expected Timeout=30, got {}",
                            response.timeout().unwrap_or_default()
                        ));
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DeleteFunction".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn", ctx.run_id.as_ref());
                    clients
                        .lambda()
                        .delete_function()
                        .function_name(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .lambda()
                        .list_functions()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .functions()
                        .iter()
                        .any(|f| f.function_name().unwrap_or_default() == name);
                    (!found)
                        .then_some(())
                        .ok_or_else(|| format!("DeleteFunction: {name} still present after delete"))
                })
            }),
        );

        // ── lambda-invoke ──────────────────────────────────────────────────

        let clients = self.clients.clone();
        impls.insert(
            "InvokeDryRun".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-invoke", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .invoke()
                        .function_name(&name)
                        .invocation_type(InvocationType::DryRun)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.status_code() != 204 {
                        return Err(format!(
                            "InvokeDryRun: expected StatusCode=204, got {}",
                            response.status_code()
                        ));
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "InvokeSync".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-invoke", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .invoke()
                        .function_name(&name)
                        .invocation_type(InvocationType::RequestResponse)
                        .payload(Blob::new(b"{\"test\":true}".to_vec()))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.status_code() != 200 {
                        return Err(format!(
                            "InvokeSync: expected StatusCode=200, got {}",
                            response.status_code()
                        ));
                    }
                    if response.function_error().is_some() {
                        let body = response
                            .payload()
                            .map(|p| String::from_utf8_lossy(p.as_ref()).to_string())
                            .unwrap_or_default();
                        return Err(format!(
                            "InvokeSync: function error: {} — {body}",
                            response.function_error().unwrap_or_default()
                        ));
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "InvokeAsync".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-invoke", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .invoke()
                        .function_name(&name)
                        .invocation_type(InvocationType::Event)
                        .payload(Blob::new(b"{}".to_vec()))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.status_code() != 202 {
                        return Err(format!(
                            "InvokeAsync: expected StatusCode=202, got {}",
                            response.status_code()
                        ));
                    }
                    Ok(())
                })
            }),
        );

        // ── lambda-invoke-stream ───────────────────────────────────────────

        let clients = self.clients.clone();
        impls.insert(
            "InvokeWithResponseStream".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-stream", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .invoke_with_response_stream()
                        .function_name(&name)
                        .invocation_type(ResponseStreamingInvocationType::RequestResponse)
                        .payload(Blob::new(b"{\"test\":true}".to_vec()))
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.status_code() != 200 {
                        return Err(format!(
                            "InvokeWithResponseStream: expected StatusCode=200, got {}",
                            response.status_code()
                        ));
                    }
                    let mut stream = response.event_stream;
                    let mut chunk_count = 0usize;
                    let mut completed = false;
                    loop {
                        use aws_sdk_lambda::types::InvokeWithResponseStreamResponseEvent;
                        match stream.recv().await {
                            Ok(Some(InvokeWithResponseStreamResponseEvent::PayloadChunk(
                                _chunk,
                            ))) => {
                                chunk_count += 1;
                            }
                            Ok(Some(InvokeWithResponseStreamResponseEvent::InvokeComplete(
                                _complete,
                            ))) => {
                                completed = true;
                                break;
                            }
                            Ok(None) | Err(_) => break,
                            _ => {}
                        }
                    }
                    if !completed {
                        return Err(
                            "InvokeWithResponseStream: expected InvokeComplete event".to_string()
                        );
                    }
                    if chunk_count == 0 {
                        return Err(
                            "InvokeWithResponseStream: expected at least one payload chunk"
                                .to_string(),
                        );
                    }
                    Ok(())
                })
            }),
        );

        // ── lambda-aliases ─────────────────────────────────────────────────

        let clients = self.clients.clone();
        impls.insert(
            "PublishVersion".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .publish_version()
                        .function_name(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.version().unwrap_or_default().is_empty() {
                        return Err("PublishVersion: Version missing".to_string());
                    }
                    ctx.set(
                        "_lambdaVersion",
                        response.version().unwrap_or_default().to_string(),
                    );
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListVersionsByFunction".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .list_versions_by_function()
                        .function_name(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let count = response.versions().len();
                    (count > 0).then_some(()).ok_or_else(|| {
                        "ListVersionsByFunction: expected at least one version".to_string()
                    })
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "CreateAlias".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let version = ctx.get("_lambdaVersion").unwrap_or_else(|| "1".to_string());
                    let response = clients
                        .lambda()
                        .create_alias()
                        .function_name(&name)
                        .name("live")
                        .function_version(&version)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.alias_arn().unwrap_or_default().is_empty() {
                        return Err("CreateAlias: AliasArn missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetAlias".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .get_alias()
                        .function_name(&name)
                        .name("live")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.name().unwrap_or_default() != "live" {
                        return Err(format!(
                            "GetAlias: expected Name=live, got {}",
                            response.name().unwrap_or_default()
                        ));
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListAliases".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .list_aliases()
                        .function_name(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .aliases()
                        .iter()
                        .any(|a| a.name().unwrap_or_default() == "live");
                    found
                        .then_some(())
                        .ok_or_else(|| "ListAliases: expected alias 'live'".to_string())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "UpdateAlias".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .update_alias()
                        .function_name(&name)
                        .name("live")
                        .description("production alias")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.description().unwrap_or_default() != "production alias" {
                        return Err(format!(
                            "UpdateAlias: expected Description=\"production alias\", got {}",
                            response.description().unwrap_or_default()
                        ));
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DeleteAlias".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    clients
                        .lambda()
                        .delete_alias()
                        .function_name(&name)
                        .name("live")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let response = clients
                        .lambda()
                        .list_aliases()
                        .function_name(&name)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .aliases()
                        .iter()
                        .any(|a| a.name().unwrap_or_default() == "live");
                    (!found).then_some(()).ok_or_else(|| {
                        "DeleteAlias: alias 'live' still present after delete".to_string()
                    })
                })
            }),
        );

        // ── lambda-layers ──────────────────────────────────────────────────

        let clients = self.clients.clone();
        impls.insert(
            "PublishLayerVersion".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let layer_name = format!("{}-layer", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .publish_layer_version()
                        .layer_name(&layer_name)
                        .content(
                            LayerVersionContentInput::builder()
                                .zip_file(Blob::new(dummy_zip()))
                                .build(),
                        )
                        .compatible_runtimes(Runtime::Nodejs18x)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.layer_version_arn().unwrap_or_default().is_empty() {
                        return Err("PublishLayerVersion: LayerVersionArn missing".to_string());
                    }
                    ctx.set("_layerVersion", response.version().to_string());
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "ListLayers".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let layer_name = format!("{}-layer", ctx.run_id.as_ref());
                    let response = clients
                        .lambda()
                        .list_layers()
                        .compatible_runtime(Runtime::Nodejs18x)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let found = response
                        .layers()
                        .iter()
                        .any(|l| l.layer_name().unwrap_or_default() == layer_name);
                    found
                        .then_some(())
                        .ok_or_else(|| format!("ListLayers: {layer_name} not found"))
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "DeleteLayerVersion".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let layer_name = format!("{}-layer", ctx.run_id.as_ref());
                    let version = ctx
                        .get("_layerVersion")
                        .and_then(|v| v.parse::<i64>().ok())
                        .unwrap_or(1);
                    clients
                        .lambda()
                        .delete_layer_version()
                        .layer_name(&layer_name)
                        .version_number(version)
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
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
            "lambda-invoke".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-invoke", ctx.run_id.as_ref());
                    let role = "arn:aws:iam::000000000000:role/lambda-exec";
                    clients
                        .lambda()
                        .create_function()
                        .function_name(&name)
                        .runtime(Runtime::Nodejs18x)
                        .handler("index.handler")
                        .role(role)
                        .timeout(30)
                        .code(
                            FunctionCode::builder()
                                .zip_file(Blob::new(dummy_zip()))
                                .build(),
                        )
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    wait_function_active(&clients, &name, 30).await?;
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "lambda-invoke-stream".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-stream", ctx.run_id.as_ref());
                    let role = "arn:aws:iam::000000000000:role/lambda-exec";
                    clients
                        .lambda()
                        .create_function()
                        .function_name(&name)
                        .runtime(Runtime::Nodejs18x)
                        .handler("index.handler")
                        .role(role)
                        .timeout(30)
                        .code(
                            FunctionCode::builder()
                                .zip_file(Blob::new(dummy_zip()))
                                .build(),
                        )
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    wait_function_active(&clients, &name, 30).await?;
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        setups.insert(
            "lambda-aliases".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let role = "arn:aws:iam::000000000000:role/lambda-exec";
                    clients
                        .lambda()
                        .create_function()
                        .function_name(&name)
                        .runtime(Runtime::Nodejs18x)
                        .handler("index.handler")
                        .role(role)
                        .code(
                            FunctionCode::builder()
                                .zip_file(Blob::new(dummy_zip()))
                                .build(),
                        )
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
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
            "lambda-crud".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn", ctx.run_id.as_ref());
                    let _ = clients
                        .lambda()
                        .delete_function()
                        .function_name(&name)
                        .send()
                        .await;
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "lambda-invoke".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-invoke", ctx.run_id.as_ref());
                    let _ = clients
                        .lambda()
                        .delete_function()
                        .function_name(&name)
                        .send()
                        .await;
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "lambda-invoke-stream".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-stream", ctx.run_id.as_ref());
                    let _ = clients
                        .lambda()
                        .delete_function()
                        .function_name(&name)
                        .send()
                        .await;
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "lambda-aliases".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let name = format!("{}-fn-alias", ctx.run_id.as_ref());
                    let _ = clients
                        .lambda()
                        .delete_function()
                        .function_name(&name)
                        .send()
                        .await;
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        teardowns.insert(
            "lambda-layers".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let layer_name = format!("{}-layer", ctx.run_id.as_ref());
                    let version = ctx
                        .get("_layerVersion")
                        .and_then(|v| v.parse::<i64>().ok())
                        .unwrap_or(1);
                    let _ = clients
                        .lambda()
                        .delete_layer_version()
                        .layer_name(&layer_name)
                        .version_number(version)
                        .send()
                        .await;
                    Ok(())
                })
            }),
        );

        teardowns
    }
}

fn dummy_zip() -> Vec<u8> {
    use base64::Engine;
    let b64 = "UEsDBBQAAAAAAAAAAAAKhksPNQAAADUAAAAIAAAAaW5kZXguanNleHBvcnRzLmhhbmRsZXI9YXN5bmMoKT0+KHtzdGF0dXNDb2RlOjIwMCxib2R5OiJvayJ9KVBLAQIUABQAAAAAAAAAAAAKhksPNQAAADUAAAAIAAAAAAAAAAAAAAAAAAAAAABpbmRleC5qc1BLBQYAAAAAAQABADYAAABbAAAAAAA=";
    base64::engine::general_purpose::STANDARD
        .decode(b64)
        .unwrap_or_default()
}

async fn wait_function_active(
    clients: &AwsClients,
    name: &str,
    max_attempts: u32,
) -> Result<(), String> {
    for _ in 0..max_attempts {
        let response = clients
            .lambda()
            .get_function()
            .function_name(name)
            .send()
            .await
            .map_err(|err| err.to_string())?;
        let state = response.configuration().and_then(|c| c.state().cloned());
        match state {
            Some(s) if s.as_str() == "Active" => return Ok(()),
            Some(s) if s.as_str() != "Pending" => return Ok(()),
            _ => {}
        }
        tokio::time::sleep(std::time::Duration::from_millis(200)).await;
    }
    Err(format!(
        "Function {name} did not become Active after {max_attempts} attempts"
    ))
}
