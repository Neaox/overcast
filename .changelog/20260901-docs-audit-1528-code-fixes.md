* [appconfigdata] `GetLatestConfiguration` now returns the `Version-Label` header when the hosted configuration version carries a label.
  the AppConfig control plane already stored the label; the data plane omitted it and claimed the control plane did not
~ [eventbridge] reported as partial tier rather than inert in `/_overcast/health`; capability notes now list ECS and event-bus targets.
  `PutEvents` fans out to targets with retries and dead-lettering, which was never inert; `docs/services/eventbridge.md` already said partial
