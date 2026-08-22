~ [ec2] EC2's twenty-one `Describe*` operations answer from the typed operation
  registry, which the service had registered but never dispatched to. Each now
  shares one body with the legacy handler rather than carrying a second
  implementation that had fallen behind it — the typed twins decoded no filters
  at all, so every one of them would have answered with the whole region had it
  ever been reached. A differential test holds the two paths to the same bytes
  for every routed operation. No response changes: legacy answered these calls
  before and answers the identical bytes now.
* [ec2] Typed-operation errors render EC2's `<Errors><Error>` envelope rather
  than the generic Query one, restoring the wrapper deleted when typed dispatch
  was switched off.
