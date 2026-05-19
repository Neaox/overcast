# Performance baselines

Frozen `go test -bench` snapshots committed at the start of each phase
of the [Smithy wire-protocol plan](../plans/smithy.md). Subsequent PRs
compare against these via [`benchstat`](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat).

## How to compare

```bash
# Run the new numbers on your branch
go test -bench=. -benchmem -run='^$' -benchtime=2s \
  ./internal/protocol/codec/ ./internal/protocol/op/ \
  | tee /tmp/new.txt

# Compare against the committed baseline
benchstat docs/perf-baselines/phase0-codec-baseline.txt /tmp/new.txt
```

## Tolerances (see [smithy.md §9.4](../plans/smithy.md))

| Bench                             | ns/op | allocs/op                                                                                |
| --------------------------------- | ----- | ---------------------------------------------------------------------------------------- |
| `BenchmarkCodec_*`                | ±5 %  | ±5 %                                                                                     |
| `BenchmarkIdentifier_Walk`        | ±5 %  | **0** (must stay zero)                                                                   |
| `BenchmarkDispatcher_TypedInvoke` | ±5 %  | dispatcher overhead must stay zero (i.e. allocs come from the codec, not the dispatcher) |

Regressions outside tolerance block the PR. See [smithy.md §9.7](../plans/smithy.md)
for the escape valve when a regression is intrinsic.

## Files

- `phase0-codec-baseline.txt` — initial codec + dispatcher baseline
  (Phase 0, AMD Ryzen 9 5900X, Go 1.24, no codec migrations yet).
