package ec2

// typed_tagspecifications_test.go — the gate that closes the way create-time
// tags went missing.
//
// #1196 taught nine EC2 creates to honour TagSpecification.N, and its test
// (tag_validation_test.go) invokes each *legacy handler function* directly.
// When the typed operation registry took over dispatch for those operations,
// six of them lost create-time tagging silently: their typed request struct
// never declared a TagSpecifications field at all, so the decoder had nowhere
// to put it, the handler had nothing to read, and every test in the suite
// stayed green because none of them went through the path an SDK uses.
//
// Two mechanical checks close that, in both directions:
//
//   - Every typed request struct that declares TagSpecifications must have a
//     handler that reads it and stores it. A field declared and ignored is
//     the "typed twin quietly drops part of its input" fault #754 was about.
//   - Every operation AWS models with a TagSpecifications member must have a
//     typed request struct that declares one. This is the half that actually
//     failed: the field was not ignored, it was absent, so no check that
//     starts from the declaration could have seen it.
//
// Both are static — they read the source rather than driving a request — so
// they name the operation and the missing line rather than reporting a wrong
// response body. The behavioural halves live in the parity table
// (typed_parity_mutations_dev_test.go) and over the wire
// (tests/integration/ec2/ec2_tagspecification_test.go).

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// tagSpecCreates is every operation EC2 routes to the typed registry whose
// AWS input model carries a TagSpecifications member naming the resource the
// call creates, mapped to that resource type.
//
// Derived from the pinned model (models/aws/VERSION; ec2-2016-11-15.json,
// shapes `<Operation>Request.TagSpecifications`), not from what the code
// happens to implement — which is the point: a list read off the
// implementation cannot notice the implementation is missing something.
var tagSpecCreates = map[string]string{
	"RunInstances":               "instance",
	"CreateVpc":                  "vpc",
	"CreateSubnet":               "subnet",
	"CreateSecurityGroup":        "security-group",
	"CreateKeyPair":              "key-pair",
	"CreateRouteTable":           "route-table",
	"CreateInternetGateway":      "internet-gateway",
	"CreateNetworkInterface":     "network-interface",
	"CreateVpcPeeringConnection": "vpc-peering-connection",
	"CreateNatGateway":           "natgateway",
	"CreateVpnGateway":           "vpn-gateway",
	"CreateVpcEndpoint":          "vpc-endpoint",
}

// tagSpecCreatesUnimplemented is the rest of the routed operations the model
// gives a TagSpecifications member, and why Overcast reads none of it.
//
// These are gaps, not divergences introduced by the typed registry: neither
// dispatch path has ever honoured them, so both answer alike and the parity
// tables have nothing to catch. An entry is deleted when the operation starts
// honouring the field, never widened to quiet a failure.
var tagSpecCreatesUnimplemented = map[string]string{
	"AllocateAddress": "neither dispatch path parses TagSpecification.N, so an " +
		"Elastic IP tagged at allocation comes back untagged; predates the typed registry",
	"AuthorizeSecurityGroupIngress": "tags the security-group-rule resources the call " +
		"creates, and Overcast models security group rules as fields on the group rather " +
		"than as taggable resources of their own",
	"AuthorizeSecurityGroupEgress": "as AuthorizeSecurityGroupIngress",
}

// typedRequestTypeOf names the typed request struct each operation decodes
// into. It is the one hand-written link in this file, and the checks below
// are what keep it honest: an entry naming a type that does not exist fails
// to compile the lookup, and an operation missing from it fails
// TestTypedTagSpecifications_everyTagSpecCreateDeclaresTheField.
var typedRequestTypeOf = map[string]string{
	"RunInstances":               "runInstancesReq",
	"CreateVpc":                  "createVpcReq",
	"CreateSubnet":               "createSubnetReq",
	"CreateSecurityGroup":        "createSecurityGroupReq",
	"CreateKeyPair":              "createKeyPairReq",
	"CreateRouteTable":           "createRouteTableReq",
	"CreateInternetGateway":      "createIGWReq",
	"CreateNetworkInterface":     "createNetworkInterfaceReq",
	"CreateVpcPeeringConnection": "createVPCPeeringReq",
	"CreateNatGateway":           "createNatGatewayReq",
	"CreateVpnGateway":           "createVpnGatewayReq",
	"CreateVpcEndpoint":          "createVpcEndpointReq",
}

// typedLogicSource parses the typed operation bodies once per test.
func typedLogicSource(t *testing.T) *ast.File {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "typed_logic.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse typed_logic.go: %v", err)
	}
	return file
}

// structsDeclaringTagSpecifications returns the names of every struct in the
// file with a field whose type is []ec2TagSpecification.
func structsDeclaringTagSpecifications(file *ast.File) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(file, func(n ast.Node) bool {
		spec, ok := n.(*ast.TypeSpec)
		if !ok {
			return true
		}
		structType, ok := spec.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, field := range structType.Fields.List {
			slice, ok := field.Type.(*ast.ArrayType)
			if !ok {
				continue
			}
			if ident, ok := slice.Elt.(*ast.Ident); ok && ident.Name == "ec2TagSpecification" {
				out[spec.Name.Name] = true
			}
		}
		return true
	})
	return out
}

// handlersByRequestType maps a typed request struct name to the source of the
// function that takes a pointer to it.
func handlersByRequestType(file *ast.File) map[string]*ast.FuncDecl {
	out := map[string]*ast.FuncDecl{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Type.Params == nil {
			continue
		}
		for _, param := range fn.Type.Params.List {
			star, ok := param.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			if ident, ok := star.X.(*ast.Ident); ok {
				out[ident.Name] = fn
			}
		}
	}
	return out
}

// callsFunction reports whether fn's body contains a call to name.
func callsFunction(fn *ast.FuncDecl, name string) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch callee := call.Fun.(type) {
		case *ast.Ident:
			if callee.Name == name {
				found = true
			}
		case *ast.SelectorExpr:
			if callee.Sel.Name == name {
				found = true
			}
		}
		return !found
	})
	return found
}

// A typed request struct that declares TagSpecifications and never reads it
// answers 200, creates the resource, and drops the tags — the exact shape of
// failure that made a create's tags vanish without a single test going red.
func TestTypedTagSpecifications_everyDeclaredFieldIsReadAndStored(t *testing.T) {
	// Given: the typed operation source
	file := typedLogicSource(t)
	handlers := handlersByRequestType(file)

	declaring := structsDeclaringTagSpecifications(file)
	if len(declaring) == 0 {
		t.Fatal("no typed request struct declares TagSpecifications — the parse found nothing to check")
	}

	names := make([]string, 0, len(declaring))
	for name := range declaring {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, reqType := range names {
		t.Run(reqType, func(t *testing.T) {
			// When: its handler is examined
			fn, ok := handlers[reqType]
			if !ok {
				t.Fatalf("%s declares TagSpecifications but no typed handler takes a *%s", reqType, reqType)
			}

			// Then: the handler both reads the field and writes what it read
			if !callsFunction(fn, "typedTagSpecifications") {
				t.Errorf("%s declares TagSpecifications but %s never calls typedTagSpecifications — "+
					"the create-time tags are decoded and discarded", reqType, fn.Name.Name)
			}
			if !callsFunction(fn, "validateTagSpecifications") {
				t.Errorf("%s: %s does not call validateTagSpecifications — an invalid create-time tag "+
					"must refuse the call, not create the resource and drop the tag", reqType, fn.Name.Name)
			}
			if !callsFunction(fn, "putResourceTags") {
				t.Errorf("%s: %s reads TagSpecifications but never calls putResourceTags — the tags are "+
					"echoed in the response and never stored, so DescribeTags reports none", reqType, fn.Name.Name)
			}
		})
	}
}

// The half that actually broke: the field was not ignored, it was never
// declared, so the decoder had nowhere to put TagSpecification.N and no check
// starting from the declaration could see the gap. The list of operations
// that must declare one comes from the AWS model, not from the code.
func TestTypedTagSpecifications_everyTagSpecCreateDeclaresTheField(t *testing.T) {
	// Given: the typed operation source and the model-derived list
	declaring := structsDeclaringTagSpecifications(typedLogicSource(t))

	actions := make([]string, 0, len(tagSpecCreates))
	for action := range tagSpecCreates {
		actions = append(actions, action)
	}
	sort.Strings(actions)

	for _, action := range actions {
		t.Run(action, func(t *testing.T) {
			// When: the operation is routed to the typed registry
			if !ec2TypedOps[action] {
				t.Skipf("%s is not routed to the typed registry", action)
			}

			// Then: its request struct declares the field AWS models
			reqType, ok := typedRequestTypeOf[action]
			if !ok {
				t.Fatalf("%s is in tagSpecCreates with no entry in typedRequestTypeOf", action)
			}
			if !declaring[reqType] {
				t.Errorf("%s decodes into %s, which declares no TagSpecifications field — "+
					"TagSpecification.%s tags sent with the create are dropped before any handler sees them",
					action, reqType, "N")
			}
		})
	}
}

// tagSpecCreates and tagSpecCreatesUnimplemented are a partition of the
// operations AWS gives a TagSpecifications member, so an operation cannot be
// counted as covered and excused at once, and typedRequestTypeOf cannot drift
// out of step with the list it serves.
func TestTypedTagSpecifications_theListsAgree(t *testing.T) {
	for action := range tagSpecCreates {
		if reason, both := tagSpecCreatesUnimplemented[action]; both {
			t.Errorf("%s is in both tagSpecCreates and tagSpecCreatesUnimplemented (%q)", action, reason)
		}
		if _, named := typedRequestTypeOf[action]; !named {
			t.Errorf("%s is in tagSpecCreates with no entry in typedRequestTypeOf", action)
		}
		if strings.TrimSpace(tagSpecCreates[action]) == "" {
			t.Errorf("%s has an empty TagSpecification resource type", action)
		}
	}
	for action := range typedRequestTypeOf {
		if _, expected := tagSpecCreates[action]; !expected {
			t.Errorf("typedRequestTypeOf names %s, which is not in tagSpecCreates", action)
		}
	}
	for action, reason := range tagSpecCreatesUnimplemented {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is excused with no reason", action)
		}
	}
}
