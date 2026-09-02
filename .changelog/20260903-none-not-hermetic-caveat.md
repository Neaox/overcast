* [docs] `OVERCAST_VPC_EGRESS=none` is no longer documented as hermetic without qualification.
  six places promised that nothing Overcast starts reaches outside the machine. On Docker Desktop, with Overcast running outside a container, isolating the control plane would sever the Lambda Runtime API, so it stays routable and containers keep a route out — which the startup warning already said and the docs did not
  the sharpest was the Lambda "not for CI" row, which offered `none` as the way to stop local code quietly reaching production; on a Windows or macOS runner it does not
