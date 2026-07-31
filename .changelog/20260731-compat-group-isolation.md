---
section: Fixed
area: compat
---

- [compat] the #388 "created-then-not-found" flake family was three suite-side group-isolation bugs, not an emulator race: dotnet's `sns-topics` teardown swept sibling groups' live topics via an over-broad name prefix, and the cli SES and EventBridge groups each shared one identity/bus that sibling groups' teardowns deleted mid-run (suite groups run in 8 parallel slots). Each group now owns its resources; nine `compat/flaky.json` entries deleted, and the remaining cdk entry moved to its own issue (#435)
