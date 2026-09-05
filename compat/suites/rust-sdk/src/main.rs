mod clients;
mod groups;
mod harness;
mod registry;

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use clients::AwsClients;
use groups::{
    dynamodb::DynamoDbGroup, eventbridge::EventBridgeGroup, kms::KmsGroup, lambda::LambdaGroup,
    s3::S3Group, secretsmanager::SecretsManagerGroup, sns::SnsGroup, sqs::SqsGroup, ssm::SsmGroup,
    sts::StsGroup, ServiceGroup,
};
use registry::build_groups;

#[tokio::main]
async fn main() {
    let suite = "rust-sdk";
    // 127.0.0.1, not localhost: on a dual-stack host "localhost" resolves to
    // ::1 first while the container publishes IPv4 only, so every new
    // connection pays a ~2s IPv6-then-IPv4 fallback.
    let endpoint = env_or("OVERCAST_ENDPOINT", "http://127.0.0.1:4566");
    let region = env_or("OVERCAST_DEFAULT_REGION", "us-east-1");
    let skip_docker = std::env::var("OVERCAST_COMPAT_SKIP_DOCKER").ok().as_deref() == Some("1");

    let clients = Arc::new(AwsClients::new(endpoint.clone(), region.clone()).await);
    let service_groups = all_service_groups(clients);

    let mut setups = HashMap::new();
    let mut teardowns = HashMap::new();

    for group in &service_groups {
        setups.extend(group.setups());
        teardowns.extend(group.teardowns());
    }

    // The impls go through merge_impls rather than extend: a key two service
    // files both register would otherwise lose one implementation with nothing
    // said about it.
    let impls = match registry::merge_impls(
        service_groups
            .iter()
            .map(|group| (group.name(), group.impls())),
        suite,
    ) {
        Ok(impls) => impls,
        Err(err) => {
            eprintln!("{err}");
            std::process::exit(1);
        }
    };

    let mut capabilities = HashSet::new();
    if !skip_docker {
        capabilities.insert(String::from("docker"));
    }

    // No scenario backend yet: rust-sdk has no interpreter for the generated
    // registry's scenario groups, so a generated group scoped to this suite
    // reports a loud failure rather than a skip. See registry::ScenarioBackend.
    let mut all_groups = match build_groups(suite, &impls, &setups, &teardowns, &capabilities, None)
    {
        Ok(groups) => groups,
        Err(err) => {
            // Covers both a registry that will not load and unusable impl
            // registrations (see validate_impls); the latter already carries
            // its own "[rust-sdk] N unusable impl registration(s)" heading.
            if err.contains("unusable impl registration") {
                eprintln!("{err}");
            } else {
                eprintln!("[rust-sdk] failed to load registry: {err}");
            }
            std::process::exit(1);
        }
    };

    let filter_services = split_filter(std::env::var("OVERCAST_COMPAT_SERVICE").ok());
    let filter_groups = split_filter(std::env::var("OVERCAST_COMPAT_GROUPS").ok());
    let filter_tests = split_filter(std::env::var("OVERCAST_COMPAT_TESTS").ok());
    let filter_pairs = split_filter(std::env::var("OVERCAST_COMPAT_TEST_PAIRS").ok());

    if !filter_services.is_empty() {
        all_groups.retain(|group| filter_services.contains(&group.service));
    }
    if !filter_groups.is_empty() {
        all_groups.retain(|group| filter_groups.contains(&group.name));
    }
    if !filter_tests.is_empty() {
        all_groups = all_groups
            .into_iter()
            .filter_map(|mut group| {
                group.tests.retain(|test| filter_tests.contains(&test.name));
                if group.tests.is_empty() {
                    None
                } else {
                    Some(group)
                }
            })
            .collect();
    }
    if !filter_pairs.is_empty() {
        all_groups = all_groups
            .into_iter()
            .filter_map(|mut group| {
                group
                    .tests
                    .retain(|test| filter_pairs.contains(&format!("{}:{}", group.name, test.name)));
                if group.tests.is_empty() {
                    None
                } else {
                    Some(group)
                }
            })
            .collect();
    }

    let is_interactive = std::env::var("OVERCAST_COMPAT_INTERACTIVE").ok().as_deref() == Some("1");

    if is_interactive {
        harness::run_interactive(suite, &endpoint, &region, all_groups).await;
    } else {
        harness::run_suite(suite, &endpoint, &region, all_groups).await;
    }
}

/// Every service's `ServiceGroup`, in the same order `main` registers them.
/// Factored out so the registration test below builds the exact same set of
/// real impl/setup/teardown maps `main` does, rather than a hand-maintained
/// copy that could drift.
fn all_service_groups(clients: Arc<AwsClients>) -> Vec<Box<dyn ServiceGroup>> {
    vec![
        Box::new(S3Group::new(clients.clone())),
        Box::new(SqsGroup::new(clients.clone())),
        Box::new(DynamoDbGroup::new(clients.clone())),
        Box::new(SnsGroup::new(clients.clone())),
        Box::new(LambdaGroup::new(clients.clone())),
        Box::new(StsGroup::new(clients.clone())),
        Box::new(KmsGroup::new(clients.clone())),
        Box::new(SecretsManagerGroup::new(clients.clone())),
        Box::new(SsmGroup::new(clients.clone())),
        Box::new(EventBridgeGroup::new(clients.clone())),
    ]
}

fn env_or(name: &str, default: &str) -> String {
    std::env::var(name).unwrap_or_else(|_| default.to_string())
}

fn split_filter(value: Option<String>) -> HashSet<String> {
    value
        .unwrap_or_default()
        .split(',')
        .filter_map(|part| {
            let trimmed = part.trim();
            (!trimmed.is_empty()).then(|| trimmed.to_string())
        })
        .collect()
}

/// Registers the same shape of test go-sdk's `internal/groups/groups_test.go`
/// has: merge every service's *real* impl map through the same `merge_impls`
/// the binary uses, then resolve the result against the *real* registry
/// (`compat/suites/registry.json`, loaded the same way `build_groups` always
/// does). Until now this suite only had fixture-based loader tests in
/// `registry.rs` — synthetic registries that can't see a collision or a bare
/// key introduced in an actual service file. This test can, because merging
/// the real maps *is* the duplicate check, and validating them against the
/// real registry *is* the bare-key check (compat/AGENTS.md § Implementation
/// keys).
#[cfg(test)]
mod registration_tests {
    use super::*;

    #[tokio::test]
    async fn real_impls_resolve_against_the_real_registry_and_are_all_qualified() {
        // No network I/O: credentials and region are supplied explicitly, so
        // `AwsClients::new` only builds local SDK config — the same call
        // `main` makes before ever touching the endpoint.
        let clients = Arc::new(AwsClients::new("http://localhost:4566".to_string(), "us-east-1".to_string()).await);
        let service_groups = all_service_groups(clients);

        let mut setups = HashMap::new();
        let mut teardowns = HashMap::new();
        for group in &service_groups {
            setups.extend(group.setups());
            teardowns.extend(group.teardowns());
        }

        let impls = registry::merge_impls(
            service_groups.iter().map(|group| (group.name(), group.impls())),
            "rust-sdk",
        )
        .expect("real impl maps must merge without duplicate keys");

        // #1700: every impl key must be "<group>:<test>" — a bare key cannot
        // say which group it implements once two groups share a test name,
        // and the generated registry groups (#1113) will do exactly that.
        let bare: Vec<&String> = impls.keys().filter(|key| !key.contains(':')).collect();
        assert!(
            bare.is_empty(),
            "bare (unqualified) impl keys found, must be \"group:test\": {bare:?}"
        );

        let mut capabilities = HashSet::new();
        capabilities.insert("docker".to_string());

        // No scenario backend: this case is about the hand-written
        // registrations resolving, the same `None` main passes.
        build_groups("rust-sdk", &impls, &setups, &teardowns, &capabilities, None)
            .expect("real impl registrations must resolve against the real registry");
    }
}
