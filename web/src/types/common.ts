/**
 * Server-side types the console consumes — generated, not written.
 *
 * Everything here is rendered by `cmd/tsgen` from the Go structs the BFF
 * actually marshals (see api.gen.ts's header and cmd/tsgen/main.go's
 * manifest), so a Go field rename fails `make check-ts` in CI instead of
 * shipping a UI that reads `undefined`. To change one of these types, change
 * the Go struct and run `make generate-ts`; to expose a new server type, add
 * it to the manifest in cmd/tsgen/main.go. Do not write server types by hand
 * in this file — that is the drift class the generator exists to close
 * (docs/plans/dev-bff-consolidation.md § B2).
 */
export type * from "./api.gen"
