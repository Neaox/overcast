+ [lambda] `platform.runtimeDone` and `platform.report` carry `errorType: Runtime.ExitError` when the runtime exited; timeout and handler-error records stay nameless, as AWS's own examples do
+ [lambda] telemetry deliveries are batched per the subscription's buffering configuration — maxItems, maxBytes and timeoutMs with AWS's defaults and limits — instead of one POST per record
+ [lambda] a subscriber whose batch was lost is now told: the next batch opens with a `platform.logsDropped` event carrying the dropped counts, in AWS's documented shape
