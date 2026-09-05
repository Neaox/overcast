package lambdadocker_test

// The Lambda suite is two packages so its halves run concurrently: this one
// holds everything that starts a container, tests/integration/lambda drives
// the control plane over HTTP. The wire types and request helpers both halves
// use live in tests/helpers/lambdafixture, once; this file binds them back to
// the short names the tests here already read with.
//
// Nothing but a binding belongs in this file. A helper only this half uses
// stays file-scoped where it is used.

import (
	"net/http"
	"testing"

	"github.com/overcast-sh/overcast/tests/helpers/lambdafixture"
)

type (
	createFunctionReq      = lambdafixture.CreateFunctionReq
	imageConfigReq         = lambdafixture.ImageConfigReq
	lambdaCode             = lambdafixture.FunctionCode
	functionConfiguration  = lambdafixture.FunctionConfiguration
	publishLayerVersionReq = lambdafixture.PublishLayerVersionReq
	layerContent           = lambdafixture.LayerContent
	layerVersionResponse   = lambdafixture.LayerVersionResponse
	functionUrlConfigResp  = lambdafixture.FunctionURLConfigResp
)

var (
	lambdaURL      = lambdafixture.URL
	layerURL       = lambdafixture.LayerURL
	lambdaURLv2021 = lambdafixture.URLv2021
	streamingURL   = lambdafixture.StreamingURL
	doJSON         = lambdafixture.DoJSON
	invokeFunction = lambdafixture.InvokeFunction

	// The in-container init is build output and its embed is baked at compile
	// time, so this binary builds it for itself: TestMain does that up front,
	// and requireLambdaInit reports a build failure against the test that
	// needed it. repoRoot is the module root the build ran from.
	requireLambdaInit = lambdafixture.RequireInit
	repoRoot          = lambdafixture.RepoRoot
)

// decodeJSON is generic, so it cannot be bound as a value like the others.
func decodeJSON[T any](t *testing.T, resp *http.Response, out *T) {
	t.Helper()
	lambdafixture.DecodeJSON(t, resp, out)
}
