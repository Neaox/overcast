use std::collections::{HashMap, HashSet};
use std::fs;

use serde::Deserialize;

use crate::harness::{TestCase, TestFn, TestGroup};

#[derive(Deserialize)]
struct RegistryRoot {
    groups: Vec<RegistryGroup>,
}

#[derive(Deserialize)]
struct RegistryGroup {
    service: String,
    name: String,
    tests: Vec<RegistryTest>,
}

#[derive(Clone, Deserialize)]
struct RegistryTest {
    name: String,
    op: Option<String>,
    skip: Option<String>,
    #[serde(default)]
    requires: Vec<String>,
    #[serde(default)]
    depends: Vec<String>,
}

pub fn build_groups(
    suite: &str,
    impls: &HashMap<String, TestFn>,
    setups: &HashMap<String, TestFn>,
    teardowns: &HashMap<String, TestFn>,
    capabilities: &HashSet<String>,
) -> Result<Vec<TestGroup>, String> {
    let registry = load()?;
    validate_impls(&registry, impls, suite);
    let ambiguous = ambiguous_test_names(&registry);

    Ok(registry
        .groups
        .into_iter()
        .map(|group| TestGroup {
            suite: suite.to_string(),
            service: group.service.clone(),
            name: group.name.clone(),
            tests: topo_sort(group.tests)
                .into_iter()
                .map(|test| build_test_case(suite, &group.name, test, impls, capabilities, &ambiguous))
                .collect(),
            setup: setups.get(&group.name).cloned(),
            teardown: teardowns.get(&group.name).cloned(),
        })
        .collect())
}

/// Test names that appear in more than one registry group.
///
/// A bare-name implementation cannot serve these: `CreateFunction` belongs to
/// both `lambda-crud` and `appsync-functions`, so falling back to the bare name
/// runs whichever group registered it last. That is how rust-sdk — which has no
/// AppSync implementation at all — reported results for `appsync-functions`,
/// answering with Lambda's CreateFunction. A suite must register the
/// group-qualified key for these; anything else is not implemented.
fn ambiguous_test_names(registry: &RegistryRoot) -> HashSet<String> {
    let mut seen: HashMap<&str, &str> = HashMap::new();
    let mut ambiguous = HashSet::new();
    for group in &registry.groups {
        for test in &group.tests {
            match seen.get(test.name.as_str()) {
                Some(first) if *first != group.name.as_str() => {
                    ambiguous.insert(test.name.clone());
                }
                Some(_) => {}
                None => {
                    seen.insert(test.name.as_str(), group.name.as_str());
                }
            }
        }
    }
    ambiguous
}

fn build_test_case(
    suite: &str,
    group_name: &str,
    test: RegistryTest,
    impls: &HashMap<String, TestFn>,
    capabilities: &HashSet<String>,
    ambiguous: &HashSet<String>,
) -> TestCase {
    let noop: TestFn = std::sync::Arc::new(|_| Box::pin(async { Ok(()) }));

    if let Some(skip) = test.skip.clone() {
        return TestCase {
            name: test.name,
            op: test.op,
            skip: Some(skip),
            depends: test.depends,
            fn_: noop,
        };
    }

    if !test.requires.is_empty()
        && test
            .requires
            .iter()
            .any(|required| !capabilities.contains(required))
    {
        return TestCase {
            name: test.name,
            op: test.op,
            skip: Some(format!(
                "requires {} (not available in this environment)",
                test.requires.join(", ")
            )),
            depends: test.depends,
            fn_: noop,
        };
    }

    // A name shared by several groups may only be resolved by its qualified
    // key — a bare-name match would run another group's implementation.
    let qualified = format!("{}:{}", group_name, test.name);
    let bare = if ambiguous.contains(&test.name) {
        None
    } else {
        impls.get(&test.name).cloned()
    };
    let implementation = impls
        .get(&qualified)
        .cloned()
        .or(bare)
        .unwrap_or_else(|| noop.clone());
    let implemented = impls.contains_key(&qualified)
        || (!ambiguous.contains(&test.name) && impls.contains_key(&test.name));
    let skip = if implemented {
        None
    } else {
        Some(format!("not yet implemented in {suite} test suite"))
    };

    TestCase {
        name: test.name,
        op: test.op,
        skip,
        depends: test.depends,
        fn_: implementation,
    }
}

fn load() -> Result<RegistryRoot, String> {
    let path =
        std::env::var("OVERCAST_REGISTRY_PATH").unwrap_or_else(|_| "../registry.json".to_string());
    let body = fs::read_to_string(&path).map_err(|err| format!("read {path}: {err}"))?;
    serde_json::from_str(&body).map_err(|err| format!("parse {path}: {err}"))
}

fn topo_sort(tests: Vec<RegistryTest>) -> Vec<RegistryTest> {
    let by_name: HashMap<_, _> = tests
        .iter()
        .map(|test| (test.name.clone(), test.clone()))
        .collect();
    let mut visited = HashSet::new();
    let mut visiting = HashSet::new();
    let mut sorted = Vec::with_capacity(tests.len());

    for test in &tests {
        visit(
            &test.name,
            &by_name,
            &mut visited,
            &mut visiting,
            &mut sorted,
        );
    }

    sorted
}

fn visit(
    name: &str,
    by_name: &HashMap<String, RegistryTest>,
    visited: &mut HashSet<String>,
    visiting: &mut HashSet<String>,
    sorted: &mut Vec<RegistryTest>,
) {
    if visited.contains(name) || visiting.contains(name) {
        return;
    }

    let Some(test) = by_name.get(name) else {
        return;
    };

    visiting.insert(name.to_string());
    for dependency in &test.depends {
        visit(dependency, by_name, visited, visiting, sorted);
    }
    visiting.remove(name);
    visited.insert(name.to_string());
    sorted.push(test.clone());
}

fn validate_impls(registry: &RegistryRoot, impls: &HashMap<String, TestFn>, suite: &str) {
    let names: HashSet<_> = registry
        .groups
        .iter()
        .flat_map(|group| {
            group
                .tests
                .iter()
                .flat_map(|test| [test.name.clone(), format!("{}:{}", group.name, test.name)])
        })
        .collect();

    for name in impls.keys() {
        if !names.contains(name) {
            eprintln!("[{suite}] WARNING: impl {name} is not in registry.json and will never run");
        }
    }
}
