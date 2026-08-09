* [scheduler] every EventBridge Scheduler operation is served at AWS's own path, so an unmodified SDK, CDK construct or `aws scheduler …` call reaches it instead of answering 501
- [scheduler] the emulator-only `/_scheduler/*` path prefix
  migration: use AWS's paths — `/schedules/{Name}` with `GroupName` in the body on create/update and `?groupName` on get/delete, `/schedules`, `/schedule-groups/{Name}`, `/schedule-groups`, and `/tags/{ResourceArn}`
~ [scheduler] `CreateSchedule` and `CreateScheduleGroup` answer 200, the status code their AWS models bind, in place of 201
*! [scheduler] `CreateSchedule` and `UpdateSchedule` validate the schedule name, the schedule expression, `FlexibleTimeWindow.Mode` and `State`, answering `ValidationException` where a schedule that could never fire used to be stored
  migration: correct the offending field — a name is 1-64 characters of `[0-9a-zA-Z-_.]`, an expression must be `rate(...)`, `cron(...)` or `at(...)`, `FlexibleTimeWindow.Mode` must be `OFF` or `FLEXIBLE`, and `State` must be `ENABLED` or `DISABLED`
* [scheduler] `DeleteScheduleGroup` deletes the schedules inside the group, as AWS does; they used to be left behind, unreachable through the API and still firing on every engine tick
+ [scheduler] `ListSchedules` and `ListScheduleGroups` honour `MaxResults`, `NextToken` and `NamePrefix`, and `ListSchedules` also filters on `State`; a `NextToken` that cannot be decoded gets a `ValidationException` in place of a silent restart at the first page
* [scheduler] `StartDate` and `EndDate` survive a `CreateSchedule` made over the JSON/CBOR protocols, the path CloudFormation takes; the REST routes and that dispatch now run one implementation rather than two copies that had drifted
* [scheduler] a store failure while reading a schedule or a group is reported as `InternalError` in place of `ResourceNotFoundException`, and a single undecodable record is skipped and logged instead of disappearing from a listing without trace
