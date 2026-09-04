package awsapi

import (
	"sort"
	"strings"
	"sync"
)

// Protocol identifies the wire protocol declared by an AWS Smithy service model.
type Protocol string

const (
	ProtocolUnknown   Protocol = ""
	ProtocolAWSJSON10 Protocol = "awsJson1_0"
	ProtocolAWSJSON11 Protocol = "awsJson1_1"
	ProtocolAWSQuery  Protocol = "awsQuery"
	ProtocolEC2Query  Protocol = "ec2Query"
	ProtocolRESTJSON  Protocol = "restJson1"
	ProtocolRESTXML   Protocol = "restXml"
	ProtocolRPCV2CBOR Protocol = "rpcv2Cbor"
	ProtocolRPCV2JSON Protocol = "rpcv2Json"
)

// ProtocolSet retains every recognized protocol trait carried by a Smithy
// service. It is a compact build-time-generated bitset, not runtime model data.
type ProtocolSet uint16

const (
	ProtocolsAWSJSON10 ProtocolSet = 1 << iota
	ProtocolsAWSJSON11
	ProtocolsAWSQuery
	ProtocolsEC2Query
	ProtocolsRESTJSON
	ProtocolsRESTXML
	ProtocolsRPCV2CBOR
	ProtocolsRPCV2JSON
)

// Operation is static routing metadata generated from the pinned AWS Smithy models.
// It deliberately contains no request or response shapes: service packages remain
// responsible for implemented behavior. The generated corpus is private so it
// cannot be mutated outside this package.
type Operation struct {
	Service      string
	ServiceShape string
	SDKID        string
	APIVersion   string
	Name         string
	Protocol     Protocol
	Protocols    ProtocolSet
	// TargetPrefix is the X-Amz-Target service shape name, including its trailing
	// dot (for example "DynamoDB_20120810."). It is empty for non-AWS JSON APIs.
	TargetPrefix string
	HTTPMethod   string
	URI          string
}

// operationKey is one (Overcast service key, operation name) pair. It is a
// comparable struct rather than a joined string so the index needs no
// separator that an operation name could contain.
type operationKey struct {
	service string
	name    string
}

// knownOperations indexes the model corpus by resolved Overcast service key,
// built once on first use and never mutated afterwards.
//
// It cannot be a binary search over manifest: manifest is sorted by the
// *modeled* service, and the aliases overcastService applies ("logs" →
// "cloudwatch-logs", "streams.dynamodb" → "dynamodbstreams") reorder the keys
// it would have to be sorted by. Indexing on the resolved key is what makes
// the aliased services answerable at all.
//
// sync.OnceValue rather than a package initialiser so a binary that never
// refuses an operation — every build that is not a running emulator — pays
// nothing for it.
var knownOperations = sync.OnceValue(func() map[operationKey]struct{} {
	index := make(map[operationKey]struct{}, len(manifest))
	for _, op := range manifest {
		index[operationKey{service: overcastService(op.Service), name: op.Name}] = struct{}{}
	}
	return index
})

// KnownOperation reports whether the pinned AWS models carry an operation of
// this name for the established Overcast service key — the question a service
// asks when every handler lookup has already missed and it must choose
// between "AWS has no such operation" (400) and "Overcast has not implemented
// it" (501).
//
// It is a single map lookup against an index built once, so unlike the linear
// walk it replaced it belongs on a request path — BenchmarkKnownOperationMiss
// against BenchmarkCorpusScanMiss is the difference. service must be an
// Overcast service key (IsServiceKey), not a modeled identity that aliases to
// one; a key the models do not carry answers false for every name, which is
// why serviceutil validates the keys its callers pass at init.
//
// Like HasOperation it answers only "is this a real AWS operation name?". Use
// Operations when the question is where AWS serves it.
func KnownOperation(service, name string) bool {
	_, ok := knownOperations()[operationKey{service: service, name: name}]
	return ok
}

// HasOperation reports whether the immutable model corpus contains an
// operation for the established Overcast service key. It is the build-tool
// spelling of KnownOperation and delegates to it: capgen reads better asking
// whether the corpus "has" a capability's operation, and a build tool has no
// reason to care that the answer comes from an index.
//
// It answers only "is this a real AWS operation name?". Use Operations when
// the question is where AWS serves it — see that function's comment for why
// the difference matters.
func HasOperation(service, name string) bool { return KnownOperation(service, name) }

// Operations returns every modeled binding an established Overcast service key
// carries for the given AWS operation name, so a build tool can assert where
// the operation is served rather than merely that it exists. A name-only check
// cannot distinguish a route AWS models from one the emulator invented, which
// is how ten services came to serve working implementations at paths and
// methods no SDK sends (#864).
//
// The result is a slice because one Overcast key can resolve to several
// modeled identities: "ses" answers for both SES v1 (Query) and SES v2
// (REST-JSON), and "apigateway" for both API Gateway v1 and v2. Callers that
// need one specific binding must select on Protocol or APIVersion.
//
// Returned values are copies; the corpus stays immutable.
func Operations(service, name string) []Operation {
	var out []Operation
	for _, op := range manifest {
		if overcastService(op.Service) == service && op.Name == name {
			out = append(out, op)
		}
	}
	return out
}

// WalkOperations visits immutable copies of every modeled operation. It is
// intended for build tools such as stub-report; request routing must use the
// generated indexes instead of scanning this complete corpus.
func WalkOperations(visit func(Operation) bool) {
	for _, op := range manifest {
		if !visit(op) {
			return
		}
	}
}

// ServiceKey maps a modeled service identity to Overcast's established key.
func ServiceKey(modelService string) string { return overcastService(modelService) }

// IsServiceKey reports whether service is an established Overcast service key —
// the name detectService answers with and IAM enforcement authorises against —
// rather than a modeled identity that aliases to one, or a string a caller
// invented.
//
// It exists for Smithy RPC v2, which is the one protocol that names its service
// in a URI label. Every other wire attaches the service to something the
// registry can resolve — a target, an (Action, Version) pair, a path — and so
// answers "is this a service?" incidentally. A label has nothing attached, and
// the router will dispatch on it before consulting the registry, so the
// classifier has to be able to ask the question directly or believe whatever
// arrives.
//
// A key is either a modeled identity that no alias moves, or the target of an
// alias. Both halves are needed: "ecs" is a modeled identity Overcast keeps,
// "apigateway" is spelled by no model at all (the models say "api-gateway" and
// "apigatewayv2"), and "cloudwatch-events" is a modeled identity that is *not*
// a key, because it aliases to eventbridge.
func IsServiceKey(service string) bool {
	if service == "" {
		return false
	}
	i := sort.SearchStrings(modelServices, service)
	if i < len(modelServices) && modelServices[i] == service && overcastService(service) == service {
		return true
	}
	// Alias targets are few and this runs only for an RPC v2 label, so a scan
	// costs less than a second generated index that could disagree with the
	// first.
	for _, alias := range serviceAliases {
		if alias.OvercastService == service {
			return true
		}
	}
	return false
}

// WalkSigningNames visits each distinct (Overcast service key, SigV4 signing
// name) pair the pinned models declare, stopping early if visit returns false.
// A service key can appear more than once: "apigateway" answers for API Gateway
// v1 and v2, and "bedrock" for the runtime and agent identities.
//
// The signing name is the service component of a SigV4 credential scope — what
// an AWS SDK actually writes into the Authorization header — and it is the
// third name a service has, alongside the modeled identity and the Overcast
// key. Exposed because middleware has to translate a scope back to a key
// before anything downstream can use it, and a hand-written table of which
// names differ would be a copy of data the models already carry.
//
// Only REST bindings declare it in the generated indexes today, which is enough
// for the classification path that needs it: the Query and JSON protocols carry
// the operation in the request itself and are claimed before the scope is read.
func WalkSigningNames(visit func(service, signingName string) bool) {
	seen := make(map[[2]string]bool, len(restOperations))
	for _, op := range restOperations {
		if op.SigningName == "" || op.ModelService == "" {
			continue
		}
		pair := [2]string{overcastService(op.ModelService), op.SigningName}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		if !visit(pair[0], pair[1]) {
			return
		}
	}
}

// signingNames is every distinct SigV4 signing name WalkSigningNames visits,
// lower-cased and built once so a lookup costs a map read rather than a scan
// of restOperations.
var signingNames = func() map[string]bool {
	names := make(map[string]bool, 128)
	WalkSigningNames(func(_, signingName string) bool {
		names[strings.ToLower(signingName)] = true
		return true
	})
	return names
}()

// IsSigningName reports whether name is a SigV4 signing name a pinned model
// actually declares for some real AWS service — as opposed to a value a
// caller invented, a typo, or Overcast's own service key where that key
// happens to differ from the name an SDK signs with (see WalkSigningNames).
// Comparison folds case: a credential scope's service component is
// conventionally lower-case, but nothing enforces that on the wire.
//
// This exists so a caller can tell "signed for a different, real AWS
// service" apart from "signed for something that isn't a service at all" —
// the distinction the router's scope-mismatch answer depends on (#887): only
// the first is evidence a genuine SDK produced the request.
func IsSigningName(name string) bool {
	return name != "" && signingNames[strings.ToLower(name)]
}
