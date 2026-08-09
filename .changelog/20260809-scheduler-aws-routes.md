* [scheduler] every EventBridge Scheduler operation is served at AWS's own path, so an unmodified SDK, CDK construct or `aws scheduler …` call reaches it instead of answering 501
- [scheduler] the emulator-only `/_scheduler/*` path prefix
  migration: use AWS's paths — `/schedules/{Name}` with `GroupName` in the body on create/update and `?groupName` on get/delete, `/schedules`, `/schedule-groups/{Name}`, `/schedule-groups`, and `/tags/{ResourceArn}`
~ [scheduler] `CreateSchedule` and `CreateScheduleGroup` answer 200, the status code their AWS models bind, in place of 201
