use std::collections::HashMap;
use std::sync::Arc;

use crate::clients::AwsClients;
use crate::groups::ServiceGroup;
use crate::harness::{TestContext, TestFn};

pub struct EventBridgeGroup {
    clients: Arc<AwsClients>,
}

impl EventBridgeGroup {
    pub fn new(clients: Arc<AwsClients>) -> Self {
        Self { clients }
    }
}

impl ServiceGroup for EventBridgeGroup {
    fn name(&self) -> &'static str {
        "eventbridge"
    }

    fn impls(&self) -> HashMap<String, TestFn> {
        let mut impls: HashMap<String, TestFn> = HashMap::new();

        // ── eventbridge-patterns ───────────────────────────────────────────
        //
        // TestEventPattern creates nothing, so this group needs no setup or
        // teardown: both tests send the same event and differ only in the
        // pattern they match it against.

        let clients = self.clients.clone();
        impls.insert(
            "eventbridge-patterns:TestEventPattern".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let event = sample_event(&ctx);
                    let pattern = r#"{"source":["compat.eventbridge-patterns"],"detail-type":["order.created"]}"#;
                    let response = clients
                        .eventbridge()
                        .test_event_pattern()
                        .event_pattern(pattern)
                        .event(event.as_str())
                        .send()
                        .await
                        .map_err(crate::harness::sdk_error)?;
                    if !response.result() {
                        return Err(format!(
                            "TestEventPattern: expected Result=true for pattern {pattern} against event {event}"
                        ));
                    }
                    Ok(())
                })
            }),
        );

        let clients = self.clients.clone();
        impls.insert(
            "eventbridge-patterns:TestEventPatternNoMatch".to_string(),
            Arc::new(move |ctx: TestContext| {
                let clients = clients.clone();
                Box::pin(async move {
                    let event = sample_event(&ctx);
                    let pattern = r#"{"source":["compat.eventbridge-patterns.other"]}"#;
                    let response = clients
                        .eventbridge()
                        .test_event_pattern()
                        .event_pattern(pattern)
                        .event(event.as_str())
                        .send()
                        .await
                        .map_err(crate::harness::sdk_error)?;
                    // `Result` is a plain bool in this SDK, so a response that
                    // omitted the member entirely would also read as false —
                    // the matching test above is what pins the member as
                    // actually present and actually answered.
                    if response.result() {
                        return Err(format!(
                            "TestEventPatternNoMatch: expected Result=false for pattern {pattern} against event {event}"
                        ));
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

/// The event both pattern tests match against, serialised the way
/// `TestEventPattern` wants it: the wire member is a JSON *string*, not a
/// nested object, so the value is built first and rendered once.
fn sample_event(ctx: &TestContext) -> String {
    serde_json::json!({
        "id": ctx.run_id.as_ref(),
        "detail-type": "order.created",
        "source": "compat.eventbridge-patterns",
        "account": "000000000000",
        "time": "2026-01-01T00:00:00Z",
        "region": ctx.region.as_ref(),
        "resources": [],
        "detail": { "orderId": "1" }
    })
    .to_string()
}
