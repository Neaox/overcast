~! [router/cli] `POST /_overcast/debug/reset` moved to `POST /_overcast/reset` and no longer needs OVERCAST_DEBUG
  migration: call /_overcast/reset (and /_overcast/reset/{service}); the debug-gated path is gone
+ [cli] `overcast reset [service]` wipes emulated state, with a TTY confirmation prompt
+ [cli] `overcast config` shows the running daemon effective config (needs OVERCAST_DEBUG)
