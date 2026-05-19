use std::collections::HashMap;
use std::sync::Arc;

use crate::clients::AwsClients;
use crate::groups::ServiceGroup;
use crate::harness::{TestContext, TestFn};

pub struct StsGroup {
    clients: Arc<AwsClients>,
}

impl StsGroup {
    pub fn new(clients: Arc<AwsClients>) -> Self {
        Self { clients }
    }
}

impl ServiceGroup for StsGroup {
    fn impls(&self) -> HashMap<String, TestFn> {
        let mut impls: HashMap<String, TestFn> = HashMap::new();
        let clients = self.clients.clone();
        impls.insert(
            "GetCallerIdentity".to_string(),
            Arc::new(move |_ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let response = clients
                        .sts()
                        .get_caller_identity()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    if response.account().unwrap_or_default().is_empty() {
                        return Err("GetCallerIdentity: account missing".to_string());
                    }
                    if response.arn().unwrap_or_default().is_empty() {
                        return Err("GetCallerIdentity: arn missing".to_string());
                    }
                    if response.user_id().unwrap_or_default().is_empty() {
                        return Err("GetCallerIdentity: userId missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetSessionToken".to_string(),
            Arc::new(move |_ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let response = clients
                        .sts()
                        .get_session_token()
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let credentials = response
                        .credentials()
                        .ok_or_else(|| "GetSessionToken: credentials missing".to_string())?;
                    if credentials.access_key_id().is_empty() {
                        return Err("GetSessionToken: accessKeyId missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "GetFederationToken".to_string(),
            Arc::new(move |_ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let response = clients
                        .sts()
                        .get_federation_token()
                        .name("compat-user")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let credentials = response
                        .credentials()
                        .ok_or_else(|| "GetFederationToken: credentials missing".to_string())?;
                    if credentials.access_key_id().is_empty() {
                        return Err("GetFederationToken: accessKeyId missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "AssumeRole".to_string(),
            Arc::new(move |_ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let response = clients
                        .sts()
                        .assume_role()
                        .role_arn("arn:aws:iam::000000000000:role/compat-role")
                        .role_session_name("rust-sdk-compat")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let credentials = response
                        .credentials()
                        .ok_or_else(|| "AssumeRole: credentials missing".to_string())?;
                    if credentials.access_key_id().is_empty() {
                        return Err("AssumeRole: accessKeyId missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "AssumeRoleWithWebIdentity".to_string(),
            Arc::new(move |_ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let response = clients
                        .sts()
                        .assume_role_with_web_identity()
                        .role_arn("arn:aws:iam::000000000000:role/compat-role")
                        .role_session_name("rust-sdk-compat")
                        .web_identity_token("fake-web-identity-token")
                        .send()
                        .await
                        .map_err(|err| err.to_string())?;
                    let credentials = response.credentials().ok_or_else(|| {
                        "AssumeRoleWithWebIdentity: credentials missing".to_string()
                    })?;
                    if credentials.access_key_id().is_empty() {
                        return Err("AssumeRoleWithWebIdentity: accessKeyId missing".to_string());
                    }
                    Ok(())
                })
            }),
        );

        impls
    }

    fn setups(&self) -> HashMap<String, TestFn> {
        HashMap::new()
    }

    fn teardowns(&self) -> HashMap<String, TestFn> {
        HashMap::new()
    }
}
