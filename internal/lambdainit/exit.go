package lambdainit

// The init's own exit codes. Anything else the process exits with came from the
// runtime child, and is passed through unchanged so the host's existing
// container-exit handling sees exactly what it would have seen without an init
// in the tree:
//
//	<n>      the runtime child exited with status <n>
//	128+<s>  the runtime child was killed by signal <s> (SIGTERM -> 143), as a shell reports it
//	2        the init cannot do its job: OVERCAST_RUNTIME_API unset, no child
//	         command, or the Runtime API proxy could not listen
//	127      the runtime child could not be started, the value a shell uses for
//	         "command not found"
const (
	exitConfig     = 2
	exitCannotExec = 127
)
