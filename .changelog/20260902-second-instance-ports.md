~ [lambda/ses] a second Overcast on one host keeps Lambda and Inbox capture: busy default ports 9001 and 1025 fall back to ephemeral ones.
  `LAMBDA_RUNTIME_API_PORT` and `OVERCAST_SMTP_PORT` accept `0` for an ephemeral port; any other value is pinned
  a pinned port that cannot bind is a startup warning naming the variable, and `/_overcast/health` reports the failed listener, its bind error and the fix under `listeners`
* [ses] an email send no longer hangs when the SMTP capture server failed to bind; it fails at once with the reason and the variable to change.
