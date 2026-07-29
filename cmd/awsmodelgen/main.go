// Command awsmodelgen converts pinned AWS Smithy JSON AST models into compact
// static operation metadata. It is an update-time tool; the emulator never
// reads model files at startup or while serving requests.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

func main() {
	modelsDir := flag.String("models", "", "directory containing Smithy JSON AST files")
	output := flag.String("output", "internal/awsapi/manifest.gen.go", "generated Go file")
	check := flag.Bool("check", false, "check that -output matches generated content without writing")
	revision := flag.String("source-revision", "", "pinned upstream model revision")
	inventoryOutput := flag.String("inventory-output", "", "optional deterministic JSON inventory output")
	baselineInventory := flag.String("baseline-inventory", "", "optional prior JSON inventory to compare")
	summaryOutput := flag.String("summary-output", "", "optional Markdown change summary output")
	versionFile := flag.String("version-file", "", "optional provenance VERSION file to update")
	modelDate := flag.String("model-date", "", "upstream commit date used with -version-file")
	flag.Parse()
	if *modelsDir == "" || *revision == "" {
		fmt.Fprintln(os.Stderr, "awsmodelgen: -models and -source-revision are required")
		os.Exit(2)
	}
	if (*baselineInventory == "") != (*summaryOutput == "") {
		fmt.Fprintln(os.Stderr, "awsmodelgen: -baseline-inventory and -summary-output must be used together")
		os.Exit(2)
	}
	if (*versionFile == "") != (*modelDate == "") {
		fmt.Fprintln(os.Stderr, "awsmodelgen: -version-file and -model-date must be used together")
		os.Exit(2)
	}
	if err := verifyModelRevision(*modelsDir, *revision); err != nil {
		fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
		os.Exit(1)
	}
	operations, err := loadOperations(*modelsDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
		os.Exit(1)
	}
	contents, err := renderManifest(operations, *revision)
	if err != nil {
		fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
		os.Exit(1)
	}
	if err := writeOrCheckManifest(*output, contents, *check); err != nil {
		fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
		os.Exit(1)
	}
	if *inventoryOutput != "" || *baselineInventory != "" {
		inventory := buildModelInventory(*revision, operations)
		if *inventoryOutput != "" {
			if err := writeModelInventory(*inventoryOutput, inventory); err != nil {
				fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
				os.Exit(1)
			}
		}
		if *baselineInventory != "" {
			baseline, err := readModelInventory(*baselineInventory)
			if err != nil {
				fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
				os.Exit(1)
			}
			if err := writeInventorySummary(*summaryOutput, baseline, inventory); err != nil {
				fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
				os.Exit(1)
			}
		}
	}
	if *versionFile != "" {
		if err := updateModelVersion(*versionFile, *revision, *modelDate, ManifestDigest(contents)); err != nil {
			fmt.Fprintf(os.Stderr, "awsmodelgen: %v\n", err)
			os.Exit(1)
		}
	}
}

func writeOrCheckManifest(path string, contents []byte, check bool) error {
	if !check {
		if err := os.WriteFile(path, contents, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		return nil
	}
	current, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read generated manifest %s: %w", path, err)
	}
	if !bytes.Equal(current, contents) {
		return fmt.Errorf("%s is stale; run make generate-aws-operations with the pinned AWS model checkout", path)
	}
	return nil
}

type model struct {
	Shapes map[string]shape `json:"shapes"`
}

type shape struct {
	Type                 string                     `json:"type"`
	Version              string                     `json:"version"`
	Operations           []reference                `json:"operations"`
	CollectionOperations []reference                `json:"collectionOperations"`
	Resources            []reference                `json:"resources"`
	Create               *reference                 `json:"create"`
	Put                  *reference                 `json:"put"`
	Read                 *reference                 `json:"read"`
	Update               *reference                 `json:"update"`
	Delete               *reference                 `json:"delete"`
	List                 *reference                 `json:"list"`
	Traits               map[string]json.RawMessage `json:"traits"`
}

type reference struct {
	Target string `json:"target"`
}

type serviceTrait struct {
	SDKID string `json:"sdkId"`
}

type sigV4Trait struct {
	Name string `json:"name"`
}

type httpTrait struct {
	Method string `json:"method"`
	URI    string `json:"uri"`
}

type operation struct {
	Service, ServiceShape, SDKID, APIVersion, Name, Protocol, TargetPrefix, SigningName, HTTPMethod, URI string
	Protocols                                                                                            []string
}

func generateManifest(modelsDir, revision string) ([]byte, error) {
	operations, err := loadOperations(modelsDir)
	if err != nil {
		return nil, err
	}
	return renderManifest(operations, revision)
}

func loadOperations(modelsDir string) ([]operation, error) {
	var operations []operation
	err := filepath.WalkDir(modelsDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		parsed, err := parseModel(path)
		if err != nil {
			return err
		}
		operations = append(operations, parsed...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read models: %w", err)
	}
	if len(operations) == 0 {
		return nil, fmt.Errorf("no service operations found in %s", modelsDir)
	}
	sort.SliceStable(operations, func(i, j int) bool {
		if operations[i].Service != operations[j].Service {
			return operations[i].Service < operations[j].Service
		}
		if operations[i].Name != operations[j].Name {
			return operations[i].Name < operations[j].Name
		}
		if operations[i].APIVersion != operations[j].APIVersion {
			return operations[i].APIVersion < operations[j].APIVersion
		}
		return operations[i].URI < operations[j].URI
	})
	return operations, nil
}

func renderManifest(operations []operation, revision string) ([]byte, error) {
	var out bytes.Buffer
	out.WriteString("// Code generated by cmd/awsmodelgen; DO NOT EDIT.\n")
	out.WriteString("package awsapi\n\n")
	fmt.Fprintf(&out, "const SourceRevision = %q\n\n", revision)
	out.WriteString("var manifest = []Operation{\n")
	for _, op := range operations {
		fmt.Fprintf(&out, "\t{Service: %q, ServiceShape: %q, SDKID: %q, APIVersion: %q, Name: %q, Protocol: Protocol%s, Protocols: %s, TargetPrefix: %q, HTTPMethod: %q, URI: %q},\n", op.Service, op.ServiceShape, op.SDKID, op.APIVersion, op.Name, op.Protocol, protocolSetExpression(op.Protocols), op.TargetPrefix, op.HTTPMethod, op.URI)
	}
	out.WriteString("}\n")
	writeRegistryIndexes(&out, operations)
	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return nil, fmt.Errorf("format generated manifest: %w", err)
	}
	return formatted, nil
}

// writeRegistryIndexes emits the immutable lookup indexes used by the router.
// REST bindings are compiled into a segment trie at generation time so runtime
// matching remains independent of the total modeled operation count.
func writeRegistryIndexes(out *bytes.Buffer, operations []operation) {
	targets := make([]operation, 0, len(operations))
	queries := make([]operation, 0, len(operations))
	rest := make([]operation, 0, len(operations))
	var rpc []rpcIndexInput
	for _, op := range operations {
		if op.TargetPrefix != "" && (op.Protocol == "AWSJSON10" || op.Protocol == "AWSJSON11") {
			targets = append(targets, op)
		}
		if op.Protocol == "AWSQuery" || op.Protocol == "EC2Query" {
			queries = append(queries, op)
		}
		if (op.Protocol == "RESTJSON" || op.Protocol == "RESTXML") && op.HTTPMethod != "" && op.URI != "" && op.Service != "s3" {
			rest = append(rest, op)
		}
		for _, protocol := range []string{"RPCV2CBOR", "RPCV2JSON"} {
			if slices.Contains(op.Protocols, protocol) {
				rpc = append(rpc, rpcIndexInput{operation: op, protocol: protocol})
			}
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		left, right := targets[i].TargetPrefix+targets[i].Name, targets[j].TargetPrefix+targets[j].Name
		if left != right {
			return left < right
		}
		return targets[i].Service < targets[j].Service
	})
	sort.Slice(queries, func(i, j int) bool {
		if queries[i].APIVersion != queries[j].APIVersion {
			return queries[i].APIVersion < queries[j].APIVersion
		}
		if queries[i].Name != queries[j].Name {
			return queries[i].Name < queries[j].Name
		}
		return queries[i].Service < queries[j].Service
	})
	writeTargetIndex(out, targets)
	writeQueryIndex(out, queries)
	writeRESTTrie(out, rest)
	writeRPCIndex(out, rpc)
}

func writeTargetIndex(out *bytes.Buffer, operations []operation) {
	out.WriteString("\nvar targetOperations = []targetOperation{\n")
	var collisions []registryCollision
	for start := 0; start < len(operations); {
		target := operations[start].TargetPrefix + operations[start].Name
		end := start + 1
		for end < len(operations) && operations[end].TargetPrefix+operations[end].Name == target {
			end++
		}
		op, services := operations[start], collisionServices(operations[start:end])
		ambiguous := len(services) > 1
		if ambiguous {
			op.Service = ""
			collisions = append(collisions, registryCollision{Key: target, Services: services})
			fmt.Fprintf(out, "\t{Target: %q, ModelService: %q, Operation: %q, Protocol: Protocol%s, Ambiguous: true},\n", target, op.Service, op.Name, op.Protocol)
		} else {
			fmt.Fprintf(out, "\t{Target: %q, ModelService: %q, Operation: %q, Protocol: Protocol%s},\n", target, op.Service, op.Name, op.Protocol)
		}
		start = end
	}
	out.WriteString("}\n")
	writeCollisionIndex(out, "targetCollisions", collisions)
}

func writeQueryIndex(out *bytes.Buffer, operations []operation) {
	out.WriteString("\nvar queryOperations = []queryOperation{\n")
	var collisions []registryCollision
	for start := 0; start < len(operations); {
		key := operations[start].APIVersion + "\x00" + operations[start].Name
		end := start + 1
		for end < len(operations) && operations[end].APIVersion+"\x00"+operations[end].Name == key {
			end++
		}
		op, services := operations[start], collisionServices(operations[start:end])
		ambiguous := len(services) > 1
		if ambiguous {
			op.Service = ""
			collisions = append(collisions, registryCollision{Key: key, Services: services})
			fmt.Fprintf(out, "\t{Version: %q, Operation: %q, ModelService: %q, Protocol: Protocol%s, Ambiguous: true},\n", op.APIVersion, op.Name, op.Service, op.Protocol)
		} else {
			fmt.Fprintf(out, "\t{Version: %q, Operation: %q, ModelService: %q, Protocol: Protocol%s},\n", op.APIVersion, op.Name, op.Service, op.Protocol)
		}
		start = end
	}
	out.WriteString("}\n")
	writeCollisionIndex(out, "queryCollisions", collisions)
}

type registryCollision struct {
	Key      string
	Services []string
}

func collisionServices(operations []operation) []string {
	services := make([]string, 0, len(operations))
	for _, op := range operations {
		if len(services) == 0 || services[len(services)-1] != op.Service {
			services = append(services, op.Service)
		}
	}
	return services
}

func writeCollisionIndex(out *bytes.Buffer, name string, collisions []registryCollision) {
	fmt.Fprintf(out, "\nvar %s = []operationCollision{\n", name)
	for _, collision := range collisions {
		fmt.Fprintf(out, "\t{Key: %q, Services: []string{%s}},\n", collision.Key, quotedStrings(collision.Services))
	}
	out.WriteString("}\n")
}

type rpcIndexInput struct {
	operation
	protocol string
}

func writeRPCIndex(out *bytes.Buffer, operations []rpcIndexInput) {
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].protocol != operations[j].protocol {
			return operations[i].protocol < operations[j].protocol
		}
		if operations[i].ServiceShape != operations[j].ServiceShape {
			return operations[i].ServiceShape < operations[j].ServiceShape
		}
		if operations[i].Name != operations[j].Name {
			return operations[i].Name < operations[j].Name
		}
		return operations[i].Service < operations[j].Service
	})

	out.WriteString("\nvar rpcOperations = []rpcOperation{\n")
	var collisions []registryCollision
	for start := 0; start < len(operations); {
		end := start + 1
		for end < len(operations) &&
			operations[end].protocol == operations[start].protocol &&
			operations[end].ServiceShape == operations[start].ServiceShape &&
			operations[end].Name == operations[start].Name {
			end++
		}
		op := operations[start]
		services := rpcCollisionServices(operations[start:end])
		ambiguous := len(services) > 1
		if ambiguous {
			op.Service = ""
			key := rpcProtocolValue(op.protocol) + "\x00" + op.ServiceShape + "\x00" + op.Name
			collisions = append(collisions, registryCollision{Key: key, Services: services})
			fmt.Fprintf(out, "\t{Protocol: Protocol%s, ServiceShape: %q, Operation: %q, ModelService: %q, Ambiguous: true},\n", op.protocol, op.ServiceShape, op.Name, op.Service)
		} else {
			fmt.Fprintf(out, "\t{Protocol: Protocol%s, ServiceShape: %q, Operation: %q, ModelService: %q},\n", op.protocol, op.ServiceShape, op.Name, op.Service)
		}
		start = end
	}
	out.WriteString("}\n")
	writeCollisionIndex(out, "rpcCollisions", collisions)
}

func rpcCollisionServices(operations []rpcIndexInput) []string {
	services := make([]string, 0, len(operations))
	for _, op := range operations {
		if len(services) == 0 || services[len(services)-1] != op.Service {
			services = append(services, op.Service)
		}
	}
	return services
}

func rpcProtocolValue(protocol string) string {
	switch protocol {
	case "RPCV2CBOR":
		return "rpcv2Cbor"
	case "RPCV2JSON":
		return "rpcv2Json"
	default:
		return ""
	}
}

type restTrieBuildNode struct {
	literals   map[string]*restTrieBuildNode
	parameter  *restTrieBuildNode
	greedy     *restTrieBuildNode
	operations []operation
}

type restIndexOperation struct {
	Method       string
	Query        string
	ModelService string
	SigningName  string
	Operation    string
	Protocol     string
	Ambiguous    bool
}

func writeRESTTrie(out *bytes.Buffer, operations []operation) {
	root := &restTrieBuildNode{}
	for _, op := range operations {
		node := root
		path, _ := splitRESTURI(op.URI)
		if len(path) > 1 {
			path = strings.TrimSuffix(path, "/")
		}
		trimmedURI := strings.TrimPrefix(path, "/")
		var segments []string
		if trimmedURI != "" {
			segments = strings.Split(trimmedURI, "/")
		}
		for _, segment := range segments {
			if isGreedyLabel(segment) {
				if node.greedy == nil {
					node.greedy = &restTrieBuildNode{}
				}
				node = node.greedy
				continue
			}
			if isLabel(segment) {
				if node.parameter == nil {
					node.parameter = &restTrieBuildNode{}
				}
				node = node.parameter
				continue
			}
			if node.literals == nil {
				node.literals = make(map[string]*restTrieBuildNode)
			}
			if node.literals[segment] == nil {
				node.literals[segment] = &restTrieBuildNode{}
			}
			node = node.literals[segment]
		}
		node.operations = append(node.operations, op)
	}

	type flattenedNode struct {
		node  *restTrieBuildNode
		index int
	}
	flattened := []flattenedNode{{node: root, index: 0}}
	for i := 0; i < len(flattened); i++ {
		node := flattened[i].node
		keys := sortedKeys(node.literals)
		for _, key := range keys {
			flattened = append(flattened, flattenedNode{node: node.literals[key], index: len(flattened)})
		}
		if node.parameter != nil {
			flattened = append(flattened, flattenedNode{node: node.parameter, index: len(flattened)})
		}
		if node.greedy != nil {
			flattened = append(flattened, flattenedNode{node: node.greedy, index: len(flattened)})
		}
	}
	indexes := make(map[*restTrieBuildNode]int, len(flattened))
	for _, entry := range flattened {
		indexes[entry.node] = entry.index
	}

	var edges []struct {
		segment string
		node    int
	}
	var indexedOperations []restIndexOperation
	var collisions []registryCollision
	out.WriteString("\nvar restTrieNodes = []restTrieNode{\n")
	for _, entry := range flattened {
		node := entry.node
		literalStart := len(edges)
		for _, key := range sortedKeys(node.literals) {
			edges = append(edges, struct {
				segment string
				node    int
			}{key, indexes[node.literals[key]]})
		}
		literalEnd := len(edges)
		parameter, greedy := -1, -1
		if node.parameter != nil {
			parameter = indexes[node.parameter]
		}
		if node.greedy != nil {
			greedy = indexes[node.greedy]
		}
		operationStart := len(indexedOperations)
		sort.Slice(node.operations, func(i, j int) bool {
			if node.operations[i].HTTPMethod != node.operations[j].HTTPMethod {
				return node.operations[i].HTTPMethod < node.operations[j].HTTPMethod
			}
			_, leftQuery := splitRESTURI(node.operations[i].URI)
			_, rightQuery := splitRESTURI(node.operations[j].URI)
			if leftQuery != rightQuery {
				// A literal query binding is more specific than the same path
				// and method without one, so emit it first.
				return leftQuery > rightQuery
			}
			if node.operations[i].Service != node.operations[j].Service {
				return node.operations[i].Service < node.operations[j].Service
			}
			return node.operations[i].Name < node.operations[j].Name
		})
		for start := 0; start < len(node.operations); {
			end := start + 1
			_, query := splitRESTURI(node.operations[start].URI)
			for end < len(node.operations) && sameRESTBinding(node.operations[start], node.operations[end]) {
				end++
			}
			op, services := node.operations[start], collisionServices(node.operations[start:end])
			ambiguous := len(services) > 1
			if ambiguous {
				op.Service = ""
				op.SigningName = ""
				collisions = append(collisions, registryCollision{Key: normalizedRESTBinding(op), Services: services})
			}
			indexedOperations = append(indexedOperations, restIndexOperation{Method: op.HTTPMethod, Query: query, ModelService: op.Service, SigningName: op.SigningName, Operation: op.Name, Protocol: protocolConstant(op.Protocol), Ambiguous: ambiguous})
			start = end
		}
		fmt.Fprintf(out, "\t{LiteralStart: %d, LiteralEnd: %d, Parameter: %d, Greedy: %d, OperationStart: %d, OperationEnd: %d},\n", literalStart, literalEnd, parameter, greedy, operationStart, len(indexedOperations))
	}
	out.WriteString("}\n\nvar restTrieEdges = []restTrieEdge{\n")
	for _, edge := range edges {
		fmt.Fprintf(out, "\t{Segment: %q, Node: %d},\n", edge.segment, edge.node)
	}
	out.WriteString("}\n\nvar restOperations = []restOperation{\n")
	for _, op := range indexedOperations {
		if op.Ambiguous {
			fmt.Fprintf(out, "\t{Method: %q, Query: %q, ModelService: %q, SigningName: %q, Operation: %q, Protocol: %s, Ambiguous: true},\n", op.Method, op.Query, op.ModelService, op.SigningName, op.Operation, op.Protocol)
		} else {
			fmt.Fprintf(out, "\t{Method: %q, Query: %q, ModelService: %q, SigningName: %q, Operation: %q, Protocol: %s},\n", op.Method, op.Query, op.ModelService, op.SigningName, op.Operation, op.Protocol)
		}
	}
	out.WriteString("}\n")
	writeCollisionIndex(out, "restCollisions", collisions)
}

func splitRESTURI(uri string) (path, query string) {
	if i := strings.IndexByte(uri, '?'); i >= 0 {
		return uri[:i], uri[i+1:]
	}
	return uri, ""
}

func sameRESTBinding(left, right operation) bool {
	_, leftQuery := splitRESTURI(left.URI)
	_, rightQuery := splitRESTURI(right.URI)
	return left.HTTPMethod == right.HTTPMethod && leftQuery == rightQuery
}

func normalizedRESTBinding(op operation) string {
	path, query := splitRESTURI(op.URI)
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		switch {
		case isGreedyLabel(segment):
			segments[i] = "{+}"
		case isLabel(segment):
			segments[i] = "{}"
		}
	}
	key := op.HTTPMethod + " " + strings.Join(segments, "/")
	if query != "" {
		key += "?" + query
	}
	return key
}

func isLabel(segment string) bool {
	return strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}
func isGreedyLabel(segment string) bool { return isLabel(segment) && strings.HasSuffix(segment, "+}") }

func sortedKeys(values map[string]*restTrieBuildNode) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func protocolConstant(protocol string) string { return "Protocol" + protocol }

func quotedStrings(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = fmt.Sprintf("%q", value)
	}
	return strings.Join(quoted, ", ")
}

func parseModel(path string) ([]operation, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	var parsed model
	if err := json.Unmarshal(contents, &parsed); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	var out []operation
	for shapeID, svc := range parsed.Shapes {
		if svc.Type != "service" {
			continue
		}
		rawTrait, ok := svc.Traits["aws.api#service"]
		if !ok {
			return nil, fmt.Errorf("%s: service %s has no aws.api#service trait", path, shapeID)
		}
		var trait serviceTrait
		if err := json.Unmarshal(rawTrait, &trait); err != nil {
			return nil, fmt.Errorf("parse service trait in %s: %w", path, err)
		}
		protocols := modelProtocols(svc.Traits)
		protocol := modelProtocol(svc.Traits)
		targetPrefix := targetPrefixForService(shapeID, protocols)
		signingName := signingNameForService(svc.Traits)
		refs, err := serviceOperationReferences(parsed.Shapes, shapeID, svc)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		for _, ref := range refs {
			op, ok := parsed.Shapes[ref.Target]
			if !ok || op.Type != "operation" {
				return nil, fmt.Errorf("%s: service %s references missing operation %s", path, shapeID, ref.Target)
			}
			var http httpTrait
			if raw, ok := op.Traits["smithy.api#http"]; ok {
				if err := json.Unmarshal(raw, &http); err != nil {
					return nil, fmt.Errorf("parse HTTP trait for %s: %w", ref.Target, err)
				}
			}
			out = append(out, operation{
				Service: strings.ToLower(strings.ReplaceAll(trait.SDKID, " ", "-")), ServiceShape: shapeName(shapeID), SDKID: trait.SDKID,
				APIVersion: svc.Version, Name: shapeName(ref.Target), Protocol: protocol, Protocols: protocols,
				TargetPrefix: targetPrefix, SigningName: signingName, HTTPMethod: http.Method, URI: http.URI,
			})
		}
	}
	return out, nil
}

func signingNameForService(traits map[string]json.RawMessage) string {
	raw, ok := traits["aws.auth#sigv4"]
	if !ok {
		return ""
	}
	var trait sigV4Trait
	if err := json.Unmarshal(raw, &trait); err != nil {
		return ""
	}
	return trait.Name
}

func serviceOperationReferences(shapes map[string]shape, serviceID string, service shape) ([]reference, error) {
	refs := append([]reference(nil), service.Operations...)
	seenOperations := make(map[string]struct{}, len(refs))
	for _, ref := range refs {
		seenOperations[ref.Target] = struct{}{}
	}
	visitedResources := make(map[string]struct{})
	var visitResource func(reference) error
	visitResource = func(ref reference) error {
		if _, seen := visitedResources[ref.Target]; seen {
			return nil
		}
		visitedResources[ref.Target] = struct{}{}
		resource, ok := shapes[ref.Target]
		if !ok || resource.Type != "resource" {
			return fmt.Errorf("service %s references missing resource %s", serviceID, ref.Target)
		}
		operations := append([]reference{}, resource.Operations...)
		operations = append(operations, resource.CollectionOperations...)
		for _, lifecycle := range []*reference{resource.Create, resource.Put, resource.Read, resource.Update, resource.Delete, resource.List} {
			if lifecycle != nil {
				operations = append(operations, *lifecycle)
			}
		}
		for _, operation := range operations {
			if _, seen := seenOperations[operation.Target]; !seen {
				seenOperations[operation.Target] = struct{}{}
				refs = append(refs, operation)
			}
		}
		for _, nested := range resource.Resources {
			if err := visitResource(nested); err != nil {
				return err
			}
		}
		return nil
	}
	for _, resource := range service.Resources {
		if err := visitResource(resource); err != nil {
			return nil, err
		}
	}
	return refs, nil
}

func verifyModelRevision(modelsDir, revision string) error {
	cmd := exec.Command("git", "-C", filepath.Dir(modelsDir), "rev-parse", "HEAD")
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("read model checkout revision: %w", err)
	}
	if got := strings.TrimSpace(string(output)); got != revision {
		return fmt.Errorf("model checkout revision %s does not match -source-revision %s", got, revision)
	}
	return nil
}

func modelProtocol(traits map[string]json.RawMessage) string {
	protocols := modelProtocols(traits)
	if len(protocols) == 0 {
		return "Unknown"
	}
	return protocols[0]
}

func modelProtocols(traits map[string]json.RawMessage) []string {
	// Smithy permits protocol extension traits to coexist. Keep this ordered so
	// generated output is reproducible even if a model exposes more than one
	// recognized protocol. The registry phase will retain the complete trait set
	// when protocol negotiation needs it.
	var protocols []string
	for _, candidate := range []struct {
		trait    string
		protocol string
	}{
		{"aws.protocols#awsJson1_1", "AWSJSON11"},
		{"aws.protocols#awsJson1_0", "AWSJSON10"},
		{"aws.protocols#ec2Query", "EC2Query"},
		{"aws.protocols#awsQuery", "AWSQuery"},
		{"aws.protocols#restJson1", "RESTJSON"},
		{"aws.protocols#restXml", "RESTXML"},
		{"smithy.protocols#rpcv2Cbor", "RPCV2CBOR"},
		{"smithy.protocols#rpcv2Json", "RPCV2JSON"},
	} {
		if _, ok := traits[candidate.trait]; ok {
			protocols = append(protocols, candidate.protocol)
		}
	}
	return protocols
}

func hasAWSJSONProtocol(protocols []string) bool {
	return slices.Contains(protocols, "AWSJSON10") || slices.Contains(protocols, "AWSJSON11")
}

// targetPrefixOverrides covers AWS JSON targets whose legacy wire prefix cannot
// be reconstructed from the public Smithy service shape name alone.
var targetPrefixOverrides = map[string]string{
	"com.amazonaws.cloudtrail#CloudTrail_20131101": "com.amazonaws.cloudtrail.v20131101.CloudTrail_20131101.",
}

func targetPrefixForService(shapeID string, protocols []string) string {
	if !hasAWSJSONProtocol(protocols) {
		return ""
	}
	if prefix, ok := targetPrefixOverrides[shapeID]; ok {
		return prefix
	}
	return shapeName(shapeID) + "."
}

func protocolSetExpression(protocols []string) string {
	if len(protocols) == 0 {
		return "0"
	}
	sets := make([]string, len(protocols))
	for i, protocol := range protocols {
		sets[i] = "Protocols" + protocol
	}
	return strings.Join(sets, " | ")
}

func shapeName(id string) string { return id[strings.LastIndex(id, "#")+1:] }
