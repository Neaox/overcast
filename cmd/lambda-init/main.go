// Command lambda-init is the process Overcast runs as PID 1 inside a Lambda
// execution environment. It launches the container's own ENTRYPOINT + CMD as
// its child, owns that child's stdout and stderr, proxies the Runtime API, and
// ships every line to the host with the invocation it belongs to.
//
// It is not something a user runs. Overcast embeds a Linux build of it (see
// internal/services/lambda/initbin) and copies it into each function container
// at /var/overcast/init. Build the embedded artefacts with `make lambda-init`.
//
// All of the behaviour lives in internal/lambdainit so that it can be tested
// in-process; this file only translates between the process and that package.
package main

import (
	"os"

	"github.com/overcast-sh/overcast/internal/lambdainit"
)

func main() {
	os.Exit(lambdainit.Main(os.Args, os.Environ()))
}
