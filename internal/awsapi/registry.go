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
	ErrorProfileRPCV2CBOR
	ErrorProfileRPCV2JSON
)

// Claim is immutable routing metadata for one modeled AWS operation.
// Service uses Overcast's service key where it differs from the modeled SDK
// identity; ModelService retains that source identity for diagnostics.
type Claim struct {
	Service      string
	ModelService string
	SigningName  string
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

// ClaimRPC classifies a Smithy RPC v2 operation by the explicit service and
// operation names carried in /service/{service}/operation/{operation}. The
// index includes additive protocol traits, not only each model's canonical
// protocol.
func (r *Registry) ClaimRPC(protocol Protocol, serviceShape, operation string) (Claim, bool) {
	if (protocol != ProtocolRPCV2CBOR && protocol != ProtocolRPCV2JSON) || serviceShape == "" || operation == "" {
		return Claim{}, false
	}
	if separator := strings.LastIndexAny(serviceShape, ".#"); separator >= 0 {
		serviceShape = serviceShape[separator+1:]
	}
	i := sort.Search(len(rpcOperations), func(i int) bool {
		return compareRPCKey(rpcOperations[i], protocol, serviceShape, operation) >= 0
	})
	if i == len(rpcOperations) || compareRPCKey(rpcOperations[i], protocol, serviceShape, operation) != 0 {
		return Claim{}, false
	}
	op := rpcOperations[i]
	profile := ErrorProfileRPCV2CBOR
	if protocol == ProtocolRPCV2JSON {
		profile = ErrorProfileRPCV2JSON
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

func compareRPCKey(op rpcOperation, protocol Protocol, serviceShape, operation string) int {
	if op.Protocol != protocol {
		return strings.Compare(string(op.Protocol), string(protocol))
	}
	if op.ServiceShape != serviceShape {
		return strings.Compare(op.ServiceShape, serviceShape)
	}
	return strings.Compare(op.Operation, operation)
}

func compareRPCOperation(left, right rpcOperation) int {
	return compareRPCKey(left, right.Protocol, right.ServiceShape, right.Operation)
}

// ClaimREST classifies a modeled REST JSON or REST XML HTTP binding. Literal
// edges take precedence over labels, matching Smithy's URI binding semantics.
// It walks generated static tables only: no model parsing, map construction,
// or whole-manifest scan occurs on the request path.
func (r *Registry) ClaimREST(method, path string) (Claim, bool) {
	return r.ClaimRESTQuery(method, path, "")
}

// ClaimRESTQuery classifies a modeled REST binding including any literal
// query components declared by Smithy (for example ?operation=tag-resource).
// Query matching is allocation-free and accepts additional client parameters.
func (r *Registry) ClaimRESTQuery(method, path, rawQuery string) (Claim, bool) {
	if path == "" || path[0] != '/' {
		return Claim{}, false
	}

	operationIndex, ok := restTrieMatch(0, method, path, rawQuery, 1)
	if !ok {
		return Claim{}, false
	}

	op := restOperations[operationIndex]
	profile := ErrorProfileJSON
	if op.Protocol == ProtocolRESTXML {
		profile = ErrorProfileXML
	}
	return Claim{
		Service:      overcastService(op.ModelService),
		ModelService: op.ModelService,
		SigningName:  op.SigningName,
		Operation:    op.Operation,
		Protocol:     op.Protocol,
		ErrorProfile: profile,
		Ambiguous:    op.Ambiguous,
	}, true
}

// restTrieMatch walks one path without allocating a strings.Split result. A
// Smithy greedy label can be followed by literal segments, so it tries each
// possible number of consumed segments only after literal and simple-label
// branches fail. That preserves URI precedence while supporting bindings such
// as /mrap/instances/{Name+}/policy.
func restTrieMatch(node int, method, path, rawQuery string, start int) (int, bool) {
	if start == len(path) {
		current := restTrieNodes[node]
		for i := current.OperationStart; i < current.OperationEnd; i++ {
			if restOperationMatches(restOperations[i], method, rawQuery) {
				return i, true
			}
		}
		return 0, false
	}
	end := strings.IndexByte(path[start:], '/')
	if end < 0 {
		end = len(path)
	} else {
		end += start
	}
	nextStart := end
	if nextStart < len(path) {
		nextStart++
	}
	current := restTrieNodes[node]
	if literal := restLiteralNode(current, path[start:end]); literal >= 0 {
		if matched, ok := restTrieMatch(literal, method, path, rawQuery, nextStart); ok {
			return matched, true
		}
	}
	if current.Parameter >= 0 {
		if matched, ok := restTrieMatch(current.Parameter, method, path, rawQuery, nextStart); ok {
			return matched, true
		}
	}
	if current.Greedy < 0 {
		return 0, false
	}
	for greedyStart := nextStart; ; {
		if matched, ok := restTrieMatch(current.Greedy, method, path, rawQuery, greedyStart); ok {
			return matched, true
		}
		if greedyStart == len(path) {
			break
		}
		separator := strings.IndexByte(path[greedyStart:], '/')
		if separator < 0 {
			greedyStart = len(path)
		} else {
			greedyStart += separator + 1
		}
	}
	return 0, false
}

func restOperationMatches(op restOperation, method, rawQuery string) bool {
	if op.Method != method {
		return false
	}
	for required := op.Query; required != ""; {
		end := strings.IndexByte(required, '&')
		part := required
		if end >= 0 {
			part = required[:end]
			required = required[end+1:]
		} else {
			required = ""
		}
		if !rawQueryContains(rawQuery, part) {
			return false
		}
	}
	return true
}

func rawQueryContains(rawQuery, required string) bool {
	for rawQuery != "" {
		end := strings.IndexByte(rawQuery, '&')
		part := rawQuery
		if end >= 0 {
			part = rawQuery[:end]
			rawQuery = rawQuery[end+1:]
		} else {
			rawQuery = ""
		}
		// Net/http and SDK serializers may render a valueless Smithy query
		// literal as either "?acl" or "?acl=". Preserve exact matching for
		// literals that declare a value.
		if part == required || (!strings.Contains(required, "=") && part == required+"=") {
			return true
		}
	}
	return false
}

func restLiteralNode(node restTrieNode, segment string) int {
	i := sort.Search(node.LiteralEnd-node.LiteralStart, func(i int) bool {
		return restTrieEdges[node.LiteralStart+i].Segment >= segment
	})
	if i == node.LiteralEnd-node.LiteralStart {
		return -1
	}
	edge := restTrieEdges[node.LiteralStart+i]
	if edge.Segment != segment {
		return -1
	}
	return edge.Node
}

// Claim classifies the model-backed request forms that can be distinguished
// without decoding an AWS JSON body. REST bindings are exposed separately via
// ClaimREST because their late fallback must preserve S3's legitimate paths.
// For an AWS
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
