// A stand-in for the AWS SDK for Go v2, so the emitter's tests can resolve
// field types without the module cache or a network fetch.
//
// The module path is the SDK's own, which is what makes
// `github.com/aws/aws-sdk-go-v2/service/widgets` resolve here: the emitter
// derives that import path from the scenario's SDK id and does not know it is
// looking at a fixture. Only the shapes the fixture recipe reaches are
// declared, plus the handful this package exists to refuse — see
// service/widgets/api.go.
//
// It is a module of its own so the root module never builds it: `go build
// ./...` stops at a nested go.mod, and a directory named testdata is skipped
// besides.
module github.com/aws/aws-sdk-go-v2

go 1.25
