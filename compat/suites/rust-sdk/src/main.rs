mod clients;
mod groups;
mod harness;
mod registry;

use std::collections::{HashMap, HashSet};
use std::sync::Arc;

use clients::AwsClients;
use groups::{
    dynamodb::DynamoDbGroup, kms::KmsGroup, lambda::LambdaGroup, s3::S3Group,
    secretsmanager::SecretsManagerGroup, sns::SnsGroup, sqs::SqsGroup, ssm::SsmGroup,
    sts::StsGroup, ServiceGroup,
};
use registry::build_groups;

#[tokio::main]
async fn main() {
    let suite = "rust-sdk";
    let endpoint = env_or("OVERCAST_ENDPOINT", "http://localhost:4566");
    let region = env_or("OVERCAST_DEFAULT_REGION", "us-east-1");
    let skip_docker = std::env::var("OVERCAST_COMPAT_SKIP_DOCKER").ok().as_deref() == Some("1");

    let clients = Arc::new(AwsClients::new(endpoint.clone(), region.clone()).await);
    let service_groups: Vec<Box<dyn ServiceGroup>> = vec![
        Box::new(S3Group::new(clients.clone())),
        Box::new(SqsGroup::new(clients.clone())),
        Box::new(DynamoDbGroup::new(clients.clone())),
        Box::new(SnsGroup::new(clients.clone())),
        Box::new(LambdaGroup::new(clients.clone())),
        Box::new(StsGroup::new(clients.clone())),
        Box::new(KmsGroup::new(clients.clone())),
        Box::new(SecretsManagerGroup::new(clients.clone())),
        Box::new(SsmGroup::new(clients.clone())),
    ];

    let mut impls = HashMap::new();
    let mut setups = HashMap::new();
    let mut teardowns = HashMap::new();

    for group in service_groups {
        impls.extend(group.impls());
        setups.extend(group.setups());
        teardowns.extend(group.teardowns());
    }

    let mut capabilities = HashSet::new();
    if !skip_docker {
        capabilities.insert(String::from("docker"));
    }

    let mut all_groups = match build_groups(suite, &impls, &setups, &teardowns, &capabilities) {
        Ok(groups) => groups,
        Err(err) => {
            eprintln!("[rust-sdk] failed to load registry: {err}");
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
