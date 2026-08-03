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
    validate_impls(&registry, impls, suite)?;
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
    test_name_owners(registry)
        .into_iter()
        .filter(|(_, groups)| groups.len() > 1)
        .map(|(name, _)| name.to_string())
        .collect()
}

/// Maps each registry test name to the sorted groups that declare it.
fn test_name_owners(registry: &RegistryRoot) -> std::collections::BTreeMap<&str, Vec<&str>> {
    let mut owners: std::collections::BTreeMap<&str, Vec<&str>> = Default::default();
    for group in &registry.groups {
        for test in &group.tests {
            let groups = owners.entry(test.name.as_str()).or_default();
            if !groups.contains(&group.name.as_str()) {
                groups.push(group.name.as_str());
            }
        }
    }
    for groups in owners.values_mut() {
        groups.sort_unstable();
    }
    owners
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

/// Rejects impl keys that cannot be bound to exactly one registry test.
///
/// This used to be a stderr warning nobody read, while the test the key was
/// meant to implement quietly fell back to another group's implementation and
/// reported a pass. Two registrations are refused:
///
/// - a key matching no registry entry — a typo, a stale name, or the wrong
///   separator (every suite uses `group:test`; `group/test` is not accepted);
/// - a bare key for a test name that several groups declare, which cannot say
///   which group it implements.
fn validate_impls(
    registry: &RegistryRoot,
    impls: &HashMap<String, TestFn>,
    suite: &str,
) -> Result<(), String> {
    let owners = test_name_owners(registry);
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

    let mut keys: Vec<&String> = impls.keys().collect();
    keys.sort();

    let mut problems = Vec::new();
    for name in keys {
        if !names.contains(name) {
            let mut msg = format!("impl \"{name}\" matches no registry entry");
            if name.contains('/') {
                // The Java suite used "group/test" until the separator was
                // unified; a key copied from it resolves to nothing here.
                msg.push_str(&format!(
                    " (group-qualified keys use \":\", not \"/\" — did you mean \"{}\"?)",
                    name.replacen('/', ":", 1)
                ));
            }
            problems.push(msg);
        } else if let Some(groups) = owners.get(name.as_str()) {
            if groups.len() > 1 {
                // Naming every candidate rather than guessing one: only the
                // author knows which group this implementation is for, and
                // binding it to the wrong one is what this check prevents.
                let candidates: Vec<String> = groups
                    .iter()
                    .map(|group| format!("\"{group}:{name}\""))
                    .collect();
                problems.push(format!(
                    "impl \"{name}\" is ambiguous: groups {groups:?} all declare a test named                      \"{name}\" — qualify it with the group it implements, one of: {}",
                    candidates.join(", ")
                ));
            }
        }
    }

    if problems.is_empty() {
        return Ok(());
    }
    Err(format!(
        "[{suite}] {} unusable impl registration(s):
  - {}",
        problems.len(),
        problems.join("
  - ")
    ))
}

#[cfg(test)]
mod tests {
    use super::*;

    fn noop() -> TestFn {
        std::sync::Arc::new(|_| Box::pin(async { Ok(()) }))
    }

    fn test_entry(name: &str) -> RegistryTest {
        RegistryTest {
            name: name.to_string(),
            op: None,
            skip: None,
            requires: Vec::new(),
            depends: Vec::new(),
        }
    }

    /// Two unrelated groups declaring a test of the same name, plus a name owned
    /// by exactly one group — the shape that made a mis-binding possible.
    fn two_groups_one_name() -> RegistryRoot {
        RegistryRoot {
            groups: vec![
                RegistryGroup {
                    service: "iam".to_string(),
                    name: "iam-users".to_string(),
                    tests: vec![test_entry("ListUsers"), test_entry("CreateUser")],
                },
                RegistryGroup {
                    service: "cognito".to_string(),
                    name: "cognito-userpools".to_string(),
                    tests: vec![test_entry("ListUsers")],
                },
            ],
        }
    }

    fn impls_with(keys: &[&str]) -> HashMap<String, TestFn> {
        keys.iter().map(|k| (k.to_string(), noop())).collect()
    }

    fn err_of(keys: &[&str]) -> String {
        validate_impls(&two_groups_one_name(), &impls_with(keys), "rust-sdk")
            .expect_err("expected the registration to be refused")
    }

    #[test]
    fn rejects_key_written_with_the_old_slash_separator() {
        let err = err_of(&["iam-users/CreateUser"]);
        assert!(err.contains("iam-users/CreateUser"), "{err}");
        assert!(err.contains("matches no registry entry"), "{err}");
        // The message must point at the colon form, since that is the fix.
        assert!(err.contains("iam-users:CreateUser"), "{err}");
    }

    #[test]
    fn rejects_keys_naming_an_unknown_group_or_test() {
        assert!(err_of(&["iam-usres:CreateUser"]).contains("iam-usres:CreateUser"));
        assert!(err_of(&["CreateUsr"]).contains("CreateUsr"));
    }

    #[test]
    fn rejects_bare_key_for_a_name_several_groups_declare() {
        let err = err_of(&["ListUsers"]);
        assert!(err.contains("ambiguous"), "{err}");
        assert!(err.contains("iam-users"), "{err}");
        assert!(err.contains("cognito-userpools"), "{err}");
    }

    #[test]
    fn accepts_resolvable_keys() {
        let impls = impls_with(&[
            "CreateUser", // bare, single owner
            "iam-users:ListUsers",
            "cognito-userpools:ListUsers",
        ]);
        validate_impls(&two_groups_one_name(), &impls, "rust-sdk").expect("should be accepted");
    }

    fn build(keys: &[&str]) -> Vec<TestGroup> {
        let registry = two_groups_one_name();
        let ambiguous = ambiguous_test_names(&registry);
        let impls = impls_with(keys);
        registry
            .groups
            .into_iter()
            .map(|group| TestGroup {
                suite: "rust-sdk".to_string(),
                service: group.service.clone(),
                name: group.name.clone(),
                tests: group
                    .tests
                    .iter()
                    .map(|test| {
                        build_test_case(
                            "rust-sdk",
                            &group.name,
                            test.clone(),
                            &impls,
                            &HashSet::new(),
                            &ambiguous,
                        )
                    })
                    .collect(),
                setup: None,
                teardown: None,
            })
            .collect()
    }

    fn skip_of(groups: &[TestGroup], group: &str, test: &str) -> Option<String> {
        groups
            .iter()
            .find(|g| g.name == group)
            .and_then(|g| g.tests.iter().find(|t| t.name == test))
            .unwrap_or_else(|| panic!("no test {group}/{test} in built groups"))
            .skip
            .clone()
    }

    /// Defence in depth: even bypassing validation, no cross-group binding.
    #[test]
    fn refuses_cross_group_bare_fallback() {
        let groups = build(&["ListUsers"]);
        assert_eq!(
            skip_of(&groups, "cognito-userpools", "ListUsers").as_deref(),
            Some("not yet implemented in rust-sdk test suite")
        );
        assert!(skip_of(&groups, "iam-users", "ListUsers").is_some());
    }

    #[test]
    fn binds_qualified_key_to_its_group_only() {
        let groups = build(&["iam-users:ListUsers"]);
        assert!(skip_of(&groups, "iam-users", "ListUsers").is_none());
        assert!(skip_of(&groups, "cognito-userpools", "ListUsers").is_some());
    }

    #[test]
    fn allows_unambiguous_bare_fallback() {
        let groups = build(&["CreateUser"]);
        assert!(skip_of(&groups, "iam-users", "CreateUser").is_none());
    }

    #[test]
    fn tracks_owners_of_each_name() {
        let registry = two_groups_one_name();
        assert!(ambiguous_test_names(&registry).contains("ListUsers"));
        assert!(!ambiguous_test_names(&registry).contains("CreateUser"));
        assert_eq!(
            test_name_owners(&registry).get("ListUsers"),
            Some(&vec!["cognito-userpools", "iam-users"])
        );
    }
}
