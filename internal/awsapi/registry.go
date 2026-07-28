package awsapi

import (
	"net/http"
	"sort"
	"strings"
)

// ErrorProfile identifies the AWS error envelope an unsupported operation
// expects. It is derived from the modeled wire protocol, with the router
// retaining responsibility for writing the response.
type ErrorProfile uint8

const (
	ErrorProfileJSON ErrorProfile = iota + 1
	ErrorProfileQueryXML
	ErrorProfileEC2QueryXML
	ErrorProfileXML
)

// Claim is immutable routing metadata for one modeled AWS operation.
// Service uses Overcast's service key where it differs from the modeled SDK
// identity; ModelService retains that source identity for diagnostics.
type Claim struct {
	Service      string
	ModelService string
	Operation    string
	Protocol     Protocol
	ErrorProfile ErrorProfile
	Ambiguous    bool
}

// Registry classifies modeled, non-S3 operations that no service handler has
// claimed. Its generated indexes are sorted static data, so a lookup performs
// no model parsing, I/O, startup allocation, or linear manifest scan.
type Registry struct{}

// NewRegistry returns the immutable generated operation registry.
func NewRegistry() *Registry { return &Registry{} }

// ClaimTarget classifies a full X-Amz-Target value such as
// "DynamoDB_20120810.ListTables".
func (r *Registry) ClaimTarget(target string) (Claim, bool) {
	i := sort.Search(len(targetOperations), func(i int) bool {
		return targetOperations[i].Target >= target
	})
	if i == len(targetOperations) || targetOperations[i].Target != target {
		return Claim{}, false
	}
	op := targetOperations[i]
	return Claim{
		Service:      overcastService(op.ModelService),
		ModelService: op.ModelService,
		Operation:    op.Operation,
		Protocol:     op.Protocol,
		ErrorProfile: ErrorProfileJSON,
		Ambiguous:    op.Ambiguous,
	}, true
}

// ClaimQuery classifies a fully specified AWS Query operation. Version is
// required because action names are shared across services.
func (r *Registry) ClaimQuery(version, action string) (Claim, bool) {
	if version == "" || action == "" {
		return Claim{}, false
	}
	i := sort.Search(len(queryOperations), func(i int) bool {
		op := queryOperations[i]
		return op.Version > version || (op.Version == version && op.Operation >= action)
	})
	if i == len(queryOperations) {
		return Claim{}, false
	}
	op := queryOperations[i]
	if op.Version != version || op.Operation != action {
		return Claim{}, false
	}
	profile := ErrorProfileQueryXML
	if op.Protocol == ProtocolEC2Query {
		profile = ErrorProfileEC2QueryXML
	}
	return Claim{
		Service:      overcastService(op.ModelService),
		ModelService: op.ModelService,
		Operation:    op.Operation,
		Protocol:     op.Protocol,
		ErrorProfile: profile,
		Ambiguous:    op.Ambiguous,
	}, true
}

// Claim classifies the model-backed request forms that can be distinguished
// without decoding an AWS JSON body or guessing at an S3 path. REST and RPC
// bindings are deliberately added by the generated trie in A3. For an AWS
// Query request it calls ParseForm, which consumes the request body; callers
// must therefore invoke it only where Query form parsing is already intended.
func (r *Registry) Claim(req *http.Request) (Claim, bool) {
	if target := strings.TrimSpace(req.Header.Get("X-Amz-Target")); target != "" {
		return r.ClaimTarget(target)
	}
	if !strings.Contains(req.Header.Get("Content-Type"), "application/x-www-form-urlencoded") {
		return Claim{}, false
	}
	if err := req.ParseForm(); err != nil {
		return Claim{}, false
	}
	return r.ClaimQuery(req.FormValue("Version"), req.FormValue("Action"))
}

// overcastService translates the small set of modeled SDK identities whose
// normalized names differ from the router's established service keys. Keep
// this explicit: a Smithy service name is not automatically an Overcast key.
func overcastService(modelService string) string {
	i := sort.Search(len(serviceAliases), func(i int) bool {
		return serviceAliases[i].ModelService >= modelService
	})
	if i < len(serviceAliases) && serviceAliases[i].ModelService == modelService {
		return serviceAliases[i].OvercastService
	}
	return modelService
}
