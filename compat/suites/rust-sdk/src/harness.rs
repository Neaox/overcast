use std::collections::HashMap;
use std::future::Future;
use std::pin::Pin;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Instant;

use serde::{Deserialize, Serialize};
use tokio::io::{AsyncBufReadExt, BufReader};
use tokio::sync::Semaphore;

pub type TestFuture = Pin<Box<dyn Future<Output = Result<(), String>> + Send>>;
pub type TestFn = Arc<dyn Fn(TestContext) -> TestFuture + Send + Sync>;

#[derive(Clone)]
pub struct TestContext {
    pub endpoint: Arc<String>,
    pub region: Arc<String>,
    pub run_id: Arc<String>,
    state: Arc<Mutex<HashMap<String, String>>>,
}

impl TestContext {
    pub fn new(endpoint: String, region: String, run_id: String) -> Self {
        Self {
            endpoint: Arc::new(endpoint),
            region: Arc::new(region),
            run_id: Arc::new(run_id),
            state: Arc::new(Mutex::new(HashMap::new())),
        }
    }

    pub fn set(&self, key: &str, value: String) {
        if let Ok(mut state) = self.state.lock() {
            state.insert(key.to_string(), value);
        }
    }

    pub fn get(&self, key: &str) -> Option<String> {
        self.state
            .lock()
            .ok()
            .and_then(|state| state.get(key).cloned())
    }

    pub fn log(&self, msg: &str) {
        eprintln!("[rust-sdk] {msg}");
    }
}

#[derive(Clone)]
pub struct TestCase {
    pub name: String,
    pub op: Option<String>,
    pub skip: Option<String>,
    pub depends: Vec<String>,
    pub fn_: TestFn,
}

#[derive(Clone)]
pub struct TestGroup {
    pub suite: String,
    pub service: String,
    pub name: String,
    pub tests: Vec<TestCase>,
    pub setup: Option<TestFn>,
    pub teardown: Option<TestFn>,
}

#[derive(Serialize)]
struct RunStartEvent<'a> {
    event: &'a str,
    suite: &'a str,
    started_at: String,
    endpoint: &'a str,
    version: &'a str,
    total_tests: usize,
}

#[derive(Serialize)]
struct TestResultEvent<'a> {
    event: &'a str,
    suite: &'a str,
    service: &'a str,
    group: &'a str,
    test: &'a str,
    status: &'a str,
    duration_ms: u128,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

#[derive(Serialize)]
struct RunEndEvent<'a> {
    event: &'a str,
    suite: &'a str,
    passed: usize,
    failed: usize,
    skipped: usize,
    unimplemented: usize,
    duration_ms: u128,
}

pub async fn run_suite(suite: &str, endpoint: &str, region: &str, groups: Vec<TestGroup>) {
    let started = Instant::now();
    emit(&RunStartEvent {
        event: "run_start",
        suite,
        started_at: chrono_like_now(),
        endpoint,
        version: "1",
        total_tests: groups.iter().map(|group| group.tests.len()).sum(),
    });

    let slots = std::env::var("OVERCAST_COMPAT_PARALLEL_SLOTS")
        .ok()
        .and_then(|value| value.parse::<usize>().ok())
        .filter(|value| *value > 0)
        .unwrap_or(8);
    let semaphore = Arc::new(Semaphore::new(slots));

    let mut handles = Vec::new();
    for group in groups {
        let permit = semaphore
            .clone()
            .acquire_owned()
            .await
            .expect("semaphore closed");
        let endpoint = endpoint.to_string();
        let region = region.to_string();
        let suite = suite.to_string();
        handles.push(tokio::spawn(async move {
            let _permit = permit;
            run_group(&suite, &endpoint, &region, group).await
        }));
    }

    let mut passed = 0;
    let mut failed = 0;
    let mut skipped = 0;
    let mut unimplemented = 0;

    for handle in handles {
        if let Ok((group_passed, group_failed, group_skipped, group_unimplemented)) = handle.await {
            passed += group_passed;
            failed += group_failed;
            skipped += group_skipped;
            unimplemented += group_unimplemented;
        }
    }

    emit(&RunEndEvent {
        event: "run_end",
        suite,
        passed,
        failed,
        skipped,
        unimplemented,
        duration_ms: started.elapsed().as_millis(),
    });
}

async fn run_group(
    suite: &str,
    endpoint: &str,
    region: &str,
    group: TestGroup,
) -> (usize, usize, usize, usize) {
    let run_id = std::env::var("OVERCAST_COMPAT_RUN_ID").unwrap_or_else(|_| "local".to_string());
    let context = TestContext::new(endpoint.to_string(), region.to_string(), run_id);
    let mut passed = 0;
    let mut failed = 0;
    let mut skipped = 0;
    let mut unimplemented = 0;

    if let Some(setup) = group.setup.clone() {
        if let Err(err) = setup(context.clone()).await {
            let reason = format!("setup failed: {err}");
            for test in &group.tests {
                emit(&TestResultEvent {
                    event: "test_result",
                    suite,
                    service: &group.service,
                    group: &group.name,
                    test: &test.name,
                    status: "skip",
                    duration_ms: 0,
                    error: Some(reason.clone()),
                });
                skipped += 1;
            }
            run_teardown(&group, context).await;
            return (passed, failed, skipped, unimplemented);
        }
    }

    let mut blocked = std::collections::HashSet::new();
    for test in &group.tests {
        if let Some(skip) = &test.skip {
            emit(&TestResultEvent {
                event: "test_result",
                suite,
                service: &group.service,
                group: &group.name,
                test: &test.name,
                status: "skip",
                duration_ms: 0,
                error: Some(skip.clone()),
            });
            skipped += 1;
            blocked.insert(test.name.clone());
            continue;
        }

        let failed_deps: Vec<_> = test
            .depends
            .iter()
            .filter(|dependency| blocked.contains(*dependency))
            .cloned()
            .collect();
        if !failed_deps.is_empty() {
            emit(&TestResultEvent {
                event: "test_result",
                suite,
                service: &group.service,
                group: &group.name,
                test: &test.name,
                status: "skip",
                duration_ms: 0,
                error: Some(format!("dependency failed: {}", failed_deps.join(", "))),
            });
            skipped += 1;
            blocked.insert(test.name.clone());
            continue;
        }

        let started = Instant::now();
        match (test.fn_)(context.clone()).await {
            Ok(()) => {
                emit(&TestResultEvent {
                    event: "test_result",
                    suite,
                    service: &group.service,
                    group: &group.name,
                    test: &test.name,
                    status: "pass",
                    duration_ms: started.elapsed().as_millis(),
                    error: None,
                });
                passed += 1;
            }
            Err(err) => {
                let status = if is_unimplemented(&err) {
                    "unimplemented"
                } else {
                    "fail"
                };
                emit(&TestResultEvent {
                    event: "test_result",
                    suite,
                    service: &group.service,
                    group: &group.name,
                    test: &test.name,
                    status,
                    duration_ms: started.elapsed().as_millis(),
                    error: Some(err.clone()),
                });
                if status == "unimplemented" {
                    unimplemented += 1;
                } else {
                    failed += 1;
                }
                blocked.insert(test.name.clone());
            }
        }
    }

    run_teardown(&group, context).await;
    (passed, failed, skipped, unimplemented)
}

async fn run_teardown(group: &TestGroup, context: TestContext) {
    if let Some(teardown) = group.teardown.clone() {
        if let Err(err) = teardown(context).await {
            eprintln!("[rust-sdk] teardown failed for {}: {}", group.name, err);
        }
    }
}

pub fn is_unimplemented(err: &str) -> bool {
    let err = err.to_ascii_lowercase();
    err.contains("501")
        || err.contains("notimplemented")
        || err.contains("unknownoperationexception")
        || err.contains("unknown action")
        || err.contains("not implemented")
}

fn emit<T: Serialize>(value: &T) {
    println!("{}", serde_json::to_string(value).expect("serialize event"));
}

fn chrono_like_now() -> String {
    use std::time::{SystemTime, UNIX_EPOCH};

    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default();
    format!("{}.{:09}Z", now.as_secs(), now.subsec_nanos())
}

// ── Interactive mode ──────────────────────────────────────────────────────────

#[derive(Serialize)]
struct BuildingEvent<'a> {
    event: &'a str,
    suite: &'a str,
    message: &'a str,
}

#[derive(Serialize)]
struct ReadyEvent<'a> {
    event: &'a str,
    suite: &'a str,
    total_tests: usize,
}

#[derive(Serialize)]
struct TestStartEvent<'a> {
    event: &'a str,
    suite: &'a str,
    service: &'a str,
    group: &'a str,
    test: &'a str,
}

#[derive(Serialize)]
struct CancelledEvent<'a> {
    event: &'a str,
    suite: &'a str,
    batch_id: &'a str,
    group: &'a str,
    test: &'a str,
    reason: &'a str,
}

#[derive(Serialize)]
struct BatchCompleteEvent<'a> {
    event: &'a str,
    suite: &'a str,
    batch_id: &'a str,
    passed: usize,
    failed: usize,
    skipped: usize,
    unimplemented: usize,
    cancelled: usize,
    duration_ms: u128,
}

#[derive(Deserialize)]
struct StdinCommand {
    command: String,
    #[serde(default)]
    batch_id: Option<String>,
    #[serde(default)]
    tests: Option<Vec<TestRef>>,
    #[serde(default)]
    group: Option<String>,
    #[serde(default)]
    test: Option<String>,
}

#[derive(Deserialize)]
struct TestRef {
    group: String,
    tests: Option<Vec<String>>,
}

type CancellationMap = Arc<Mutex<HashMap<String, Arc<AtomicBool>>>>;

pub async fn run_interactive(
    suite: &str,
    endpoint: &str,
    region: &str,
    all_groups: Vec<TestGroup>,
) {
    emit(&BuildingEvent {
        event: "building",
        suite,
        message: "Loading registry and building test groups...",
    });

    let total_tests: usize = all_groups.iter().map(|g| g.tests.len()).sum();
    emit(&ReadyEvent {
        event: "ready",
        suite,
        total_tests,
    });

    let slots = std::env::var("OVERCAST_COMPAT_PARALLEL_SLOTS")
        .ok()
        .and_then(|v| v.parse::<usize>().ok())
        .filter(|v| *v > 0)
        .unwrap_or(8);
    let semaphore = Arc::new(Semaphore::new(slots));

    let cancellation_flags: CancellationMap = Arc::new(Mutex::new(HashMap::new()));

    // Build lookup map: group name → TestGroup
    let group_map: HashMap<String, TestGroup> = all_groups
        .into_iter()
        .map(|g| (g.name.clone(), g))
        .collect();
    let group_map = Arc::new(group_map);

    let stdin = tokio::io::stdin();
    let mut reader = BufReader::new(stdin).lines();

    while let Ok(Some(line)) = reader.next_line().await {
        let trimmed = line.trim().to_string();
        if trimmed.is_empty() {
            continue;
        }

        let cmd: StdinCommand = match serde_json::from_str(&trimmed) {
            Ok(c) => c,
            Err(e) => {
                eprintln!("[rust-sdk] invalid JSON on stdin: {trimmed} ({e})");
                continue;
            }
        };

        match cmd.command.as_str() {
            "run" => {
                handle_run(
                    cmd,
                    suite,
                    endpoint,
                    region,
                    group_map.clone(),
                    semaphore.clone(),
                    cancellation_flags.clone(),
                );
            }
            "cancel" => {
                handle_cancel(&cmd, &cancellation_flags);
            }
            "shutdown" => {
                // Cancel all in-flight work and exit.
                if let Ok(flags) = cancellation_flags.lock() {
                    for flag in flags.values() {
                        flag.store(true, Ordering::SeqCst);
                    }
                }
                return;
            }
            "ping" => {
                // Respond with pong + currently running test (if any).
                let rt = cancellation_flags
                    .lock()
                    .map(|flags| {
                        flags
                            .iter()
                            .find(|(_, flag)| !flag.load(Ordering::SeqCst))
                            .map(|(k, _)| k.clone())
                            .unwrap_or_default()
                    })
                    .unwrap_or_default();
                let ev = serde_json::json!({
                    "event": "pong",
                    "suite": suite,
                    "running_test": rt,
                });
                println!("{}", ev);
            }
            other => {
                eprintln!("[rust-sdk] unknown command: {other}");
            }
        }
    }
}

fn handle_run(
    cmd: StdinCommand,
    suite: &str,
    endpoint: &str,
    region: &str,
    group_map: Arc<HashMap<String, TestGroup>>,
    semaphore: Arc<Semaphore>,
    cancellation_flags: CancellationMap,
) {
    let batch_id = cmd.batch_id.unwrap_or_default();
    let test_refs = cmd.tests.unwrap_or_default();

    // Resolve requested groups/tests.
    // An empty test_refs list means "run all groups" (the run-all command).
    let mut groups_to_run = Vec::new();
    if test_refs.is_empty() {
        let mut all: Vec<_> = group_map.values().cloned().collect();
        all.sort_by(|a, b| a.name.cmp(&b.name));
        groups_to_run = all;
    } else {
        for test_ref in test_refs {
            let group = match group_map.get(&test_ref.group) {
                Some(g) => g.clone(),
                None => {
                    eprintln!(
                        "[rust-sdk] unknown group in run command: {}",
                        test_ref.group
                    );
                    continue;
                }
            };

            if let Some(tests) = test_ref.tests {
                if !tests.is_empty() {
                    let requested: std::collections::HashSet<String> = tests.into_iter().collect();
                    let mut filtered = group.clone();
                    filtered.tests.retain(|t| requested.contains(&t.name));
                    groups_to_run.push(filtered);
                    continue;
                }
            }
            groups_to_run.push(group);
        }
    }

    let suite = suite.to_string();
    let endpoint = endpoint.to_string();
    let region = region.to_string();

    // Fire off the batch asynchronously so stdin reading continues.
    tokio::spawn(async move {
        let batch_start = Instant::now();
        let mut handles = Vec::new();

        for group in groups_to_run {
            let permit = semaphore
                .clone()
                .acquire_owned()
                .await
                .expect("semaphore closed");
            let suite = suite.clone();
            let endpoint = endpoint.clone();
            let region = region.clone();
            let batch_id = batch_id.clone();
            let flags = cancellation_flags.clone();
            handles.push(tokio::spawn(async move {
                let _permit = permit;
                run_group_interactive(&suite, &endpoint, &region, group, &batch_id, flags).await
            }));
        }

        let mut passed = 0;
        let mut failed = 0;
        let mut skipped = 0;
        let mut unimplemented = 0;
        let mut cancelled = 0;

        for handle in handles {
            if let Ok((p, f, s, u, c)) = handle.await {
                passed += p;
                failed += f;
                skipped += s;
                unimplemented += u;
                cancelled += c;
            }
        }

        emit(&BatchCompleteEvent {
            event: "batch_complete",
            suite: &suite,
            batch_id: &batch_id,
            passed,
            failed,
            skipped,
            unimplemented,
            cancelled,
            duration_ms: batch_start.elapsed().as_millis(),
        });
    });
}

fn handle_cancel(cmd: &StdinCommand, cancellation_flags: &CancellationMap) {
    if let (Some(group), Some(test)) = (&cmd.group, &cmd.test) {
        let key = format!("{group}:{test}");
        if let Ok(flags) = cancellation_flags.lock() {
            if let Some(flag) = flags.get(&key) {
                flag.store(true, Ordering::SeqCst);
            }
        }
    } else if let Ok(flags) = cancellation_flags.lock() {
        for flag in flags.values() {
            flag.store(true, Ordering::SeqCst);
        }
    }
}

async fn run_group_interactive(
    suite: &str,
    endpoint: &str,
    region: &str,
    group: TestGroup,
    batch_id: &str,
    cancellation_flags: CancellationMap,
) -> (usize, usize, usize, usize, usize) {
    let run_id = std::env::var("OVERCAST_COMPAT_RUN_ID").unwrap_or_else(|_| "local".to_string());
    let context = TestContext::new(endpoint.to_string(), region.to_string(), run_id);
    let mut passed = 0;
    let mut failed = 0;
    let mut skipped = 0;
    let mut unimplemented = 0;
    let mut cancelled = 0;

    // Register cancellation flags for each test.
    {
        let mut flags = cancellation_flags.lock().unwrap();
        for test in &group.tests {
            let key = format!("{}:{}", group.name, test.name);
            flags.insert(key, Arc::new(AtomicBool::new(false)));
        }
    }

    // Setup phase
    let mut setup_ok = true;
    if let Some(setup) = group.setup.clone() {
        if let Err(err) = setup(context.clone()).await {
            let reason = format!("setup failed: {err}");
            for test in &group.tests {
                emit(&TestResultEvent {
                    event: "test_result",
                    suite,
                    service: &group.service,
                    group: &group.name,
                    test: &test.name,
                    status: "skip",
                    duration_ms: 0,
                    error: Some(reason.clone()),
                });
                skipped += 1;
            }
            setup_ok = false;
        }
    }

    if setup_ok {
        let mut blocked = std::collections::HashSet::new();
        for test in &group.tests {
            let key = format!("{}:{}", group.name, test.name);
            let is_cancelled = cancellation_flags
                .lock()
                .ok()
                .and_then(|flags| flags.get(&key).map(|f| f.load(Ordering::SeqCst)))
                .unwrap_or(false);

            if is_cancelled {
                emit(&CancelledEvent {
                    event: "cancelled",
                    suite,
                    batch_id,
                    group: &group.name,
                    test: &test.name,
                    reason: "user",
                });
                cancelled += 1;
                blocked.insert(test.name.clone());
                continue;
            }

            if let Some(skip) = &test.skip {
                emit(&TestResultEvent {
                    event: "test_result",
                    suite,
                    service: &group.service,
                    group: &group.name,
                    test: &test.name,
                    status: "skip",
                    duration_ms: 0,
                    error: Some(skip.clone()),
                });
                skipped += 1;
                blocked.insert(test.name.clone());
                continue;
            }

            let failed_deps: Vec<_> = test
                .depends
                .iter()
                .filter(|dep| blocked.contains(*dep))
                .cloned()
                .collect();
            if !failed_deps.is_empty() {
                emit(&TestResultEvent {
                    event: "test_result",
                    suite,
                    service: &group.service,
                    group: &group.name,
                    test: &test.name,
                    status: "skip",
                    duration_ms: 0,
                    error: Some(format!("dependency failed: {}", failed_deps.join(", "))),
                });
                skipped += 1;
                blocked.insert(test.name.clone());
                continue;
            }

            emit(&TestStartEvent {
                event: "test_start",
                suite,
                service: &group.service,
                group: &group.name,
                test: &test.name,
            });

            let started = Instant::now();
            match (test.fn_)(context.clone()).await {
                Ok(()) => {
                    let is_cancelled_after = cancellation_flags
                        .lock()
                        .ok()
                        .and_then(|flags| flags.get(&key).map(|f| f.load(Ordering::SeqCst)))
                        .unwrap_or(false);

                    if is_cancelled_after {
                        emit(&CancelledEvent {
                            event: "cancelled",
                            suite,
                            batch_id,
                            group: &group.name,
                            test: &test.name,
                            reason: "user",
                        });
                        cancelled += 1;
                        blocked.insert(test.name.clone());
                    } else {
                        emit(&TestResultEvent {
                            event: "test_result",
                            suite,
                            service: &group.service,
                            group: &group.name,
                            test: &test.name,
                            status: "pass",
                            duration_ms: started.elapsed().as_millis(),
                            error: None,
                        });
                        passed += 1;
                    }
                }
                Err(err) => {
                    let is_cancelled_after = cancellation_flags
                        .lock()
                        .ok()
                        .and_then(|flags| flags.get(&key).map(|f| f.load(Ordering::SeqCst)))
                        .unwrap_or(false);

                    if is_cancelled_after {
                        emit(&CancelledEvent {
                            event: "cancelled",
                            suite,
                            batch_id,
                            group: &group.name,
                            test: &test.name,
                            reason: "user",
                        });
                        cancelled += 1;
                    } else {
                        let status = if is_unimplemented(&err) {
                            "unimplemented"
                        } else {
                            "fail"
                        };
                        emit(&TestResultEvent {
                            event: "test_result",
                            suite,
                            service: &group.service,
                            group: &group.name,
                            test: &test.name,
                            status,
                            duration_ms: started.elapsed().as_millis(),
                            error: Some(err.clone()),
                        });
                        if status == "unimplemented" {
                            unimplemented += 1;
                        } else {
                            failed += 1;
                        }
                    }
                    blocked.insert(test.name.clone());
                }
            }
        }
    }

    // Always run teardown and clean up cancellation flags.
    run_teardown(&group, context).await;
    {
        let mut flags = cancellation_flags.lock().unwrap();
        for test in &group.tests {
            flags.remove(&format!("{}:{}", group.name, test.name));
        }
    }

    (passed, failed, skipped, unimplemented, cancelled)
}
