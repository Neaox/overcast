* [web] the map's Lambda environment cards report invocation time honestly — the
  running and last-invocation durations were read straight off a ref during
  render, so a card only picked up a new timing when its 200 ms countdown
  happened to re-render it anyway
* [web] an environment or SQS message that disappears fades out reliably — the
  ghost tracker kept its bookkeeping in refs and mutated them mid-render, so a
  render React discarded could swallow the departure and the row would simply
  vanish
* [web] clearing the global search box keeps it clear — a search issued just
  before the clear could still land and refill the results
