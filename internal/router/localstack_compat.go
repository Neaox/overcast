package router

import (
	"net/http"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/middleware"
)

// localstack_compat.go — the rest of LocalStack's operational namespace.
//
// health_compat.go serves /_localstack/health, which is what a container
// healthcheck polls. This file covers the other three paths under
// /_localstack/ that a migrated setup actually calls, and they are cheaper
// than health was: each one already has an Overcast endpoint with the same
// contract, so the alias is a second route onto the same handler rather than
// a translation.
//
//	/_localstack/init          -> /_overcast/init
//	/_localstack/init/{stage}  -> /_overcast/init/{stage}
//	/_localstack/state/reset   -> /_overcast/reset
//
// The init pair needs no translation at all. Overcast's init-hook status
// endpoint was built to LocalStack's contract in the first place — the same
// {"completed": {stage: bool}, "scripts": [{stage, name, state}]} body, the
// same BOOT/START/READY/SHUTDOWN stage names, the same
// UNKNOWN/RUNNING/SUCCESSFUL/ERROR script states — so the two paths can share
// one handler byte for byte. That is worth stating rather than assuming:
// TestLocalStackInitAliasIsByteIdentical fails if the two ever diverge, which
// is the only thing keeping "alias" from quietly becoming "approximation".
//
// Reset is the one shape difference. LocalStack answers a bare 200 with no
// body; Overcast answers {"status":"reset"}. Adding a field nobody reads
// cannot break a caller — a body is only a problem if something parses it and
// finds what it expected missing — so the alias returns Overcast's body rather
// than an emptier one, and anything checking only the status code is
// unaffected.
//
// What is deliberately NOT aliased here, and why, in one place so the omission
// is a decision rather than an oversight:
//
//   - /_localstack/diagnose, /_localstack/config, /_localstack/usage,
//     /_localstack/plugins: each dumps LocalStack's own internals in a shape
//     that only means something for LocalStack. Overcast's nearest equivalents
//     (/_overcast/debug/config, /_overcast/debug/state) carry different
//     fields, and a caller that parses one would get a body full of nulls
//     rather than an answer. The 404 names them instead.
//   - /_localstack/state/save and /_localstack/state/load: snapshot
//     save/restore. Persistence here is incremental rather than
//     snapshot-based, so there is no moment to save and nothing to load — see
//     OVERCAST_STATE in the configuration reference.

// newLocalStackInitHandler serves GET /_localstack/init and
// /_localstack/init/{stage} through Overcast's own handler, unchanged.
//
// canonical is initStatusHandler or initStageStatusHandler. Passing the
// handler rather than the runner is what makes the two paths provably one
// implementation: there is no second code path that could drift.
func newLocalStackInitHandler(canonical http.HandlerFunc, path string, hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hinter.hint(path,
			zap.String("use", "/_overcast/init"),
			zap.String("note", "LocalStack's init-hook status endpoint; Overcast's answers in the same shape"))
		canonical(w, r)
	}
}

// newLocalStackResetHandler serves POST /_localstack/state/reset through
// Overcast's own reset handler.
//
// LocalStack answers this with an empty 200 and Overcast with
// {"status":"reset"}; see this file's doc comment for why the extra field is
// safe and an emptier body would not be an improvement.
func newLocalStackResetHandler(canonical http.HandlerFunc, hinter *aliasHinter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hinter.hint(localStackResetPath,
			zap.String("use", "/_overcast/reset"),
			zap.String("note", "LocalStack's state-reset endpoint; Overcast's clears the same state"))
		canonical(w, r)
	}
}

// The compatibility paths this file registers, named once so the routes, the
// hint lines and the 404 map cannot disagree about their spelling.
const (
	localStackInitPath      = middleware.LocalStackPrefix + "init"
	localStackInitStagePath = localStackInitPath + "/{stage}"
	localStackResetPath     = middleware.LocalStackPrefix + "state/reset"
)
