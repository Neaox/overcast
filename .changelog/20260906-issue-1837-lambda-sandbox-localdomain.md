+ [lambda] `sandbox.localdomain` resolves to the execution environment's own loopback inside a Lambda sandbox, as it does on AWS.
  An extension subscribing the destination AWS documents — `http://sandbox.localdomain:<port>` — receives its records; before, the subscription was accepted and every delivery died in the resolver.
  The container carries an /etc/hosts entry for the name, so the runtime, the function and every extension resolve it too, not only the telemetry path.
