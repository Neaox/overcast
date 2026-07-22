package appsync

// schema_parser.go — SchemaParser implementation using gqlparser.
//
// Parses and validates GraphQL SDL schemas. Used by StartSchemaCreation to
// reject invalid SDL and by the execution engine for query validation.

import (
	"fmt"
	"strings"
	"sync"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

const appSyncSchemaDefinitions = `
scalar AWSDate
scalar AWSTime
scalar AWSDateTime
scalar AWSTimestamp
scalar AWSEmail
scalar AWSJSON
scalar AWSPhone
scalar AWSURL
scalar AWSIPAddress

directive @aws_api_key on FIELD_DEFINITION | OBJECT
directive @aws_iam on FIELD_DEFINITION | OBJECT
directive @aws_oidc on FIELD_DEFINITION | OBJECT
directive @aws_cognito_user_pools(cognito_groups: [String]) on FIELD_DEFINITION | OBJECT
directive @aws_lambda on FIELD_DEFINITION | OBJECT
directive @aws_auth(cognito_groups: [String]) on FIELD_DEFINITION
directive @aws_subscribe(mutations: [String]) on FIELD_DEFINITION
`

// TODO(priority:P2): confirm real AWS StartSchemaCreation behavior for AppSync directive semantics, especially config-aware @aws_auth compatibility with additional authorization modes.

var appSyncSchemaPrelude = appSyncSchemaPreludeSource()

// schemaParser implements SchemaParser using gqlparser/v2.
type schemaParser struct {
	// cache holds parsed schemas keyed by apiId.
	// Protected by mu for concurrent request safety.
	mu    sync.RWMutex
	cache map[string]*ParsedSchema
}

// newSchemaParser returns a ready-to-use SchemaParser.
func newSchemaParser() *schemaParser {
	return &schemaParser{cache: make(map[string]*ParsedSchema)}
}

// Parse validates and parses raw SDL bytes into a ParsedSchema.
func (p *schemaParser) Parse(sdl []byte) (*ParsedSchema, error) {
	src := &ast.Source{
		Name:  "schema.graphql",
		Input: string(sdl),
	}

	schema, gqlErr := gqlparser.LoadSchema(appSyncSchemaPrelude, src)
	if gqlErr != nil {
		return nil, fmt.Errorf("schema validation failed: %w", gqlErr)
	}

	// AppSync requires a Query type.
	if schema.Query == nil {
		return nil, fmt.Errorf("schema must define a Query type")
	}
	if err := validateAppSyncSchemaDefinitions(schema); err != nil {
		return nil, err
	}

	return parsedSchemaFromAST(sdl, schema), nil
}

// Merge combines multiple SDL sources into a single merged schema.
func (p *schemaParser) Merge(schemas [][]byte) (*ParsedSchema, error) {
	sources := make([]*ast.Source, 0, len(schemas)+1)
	sources = append(sources, appSyncSchemaPrelude)
	for i, sdl := range schemas {
		sources = append(sources, &ast.Source{
			Name:  fmt.Sprintf("source_%d.graphql", i),
			Input: string(sdl),
		})
	}

	schema, gqlErr := gqlparser.LoadSchema(sources...)
	if gqlErr != nil {
		return nil, fmt.Errorf("schema merge failed: %w", gqlErr)
	}
	if err := validateAppSyncSchemaDefinitions(schema); err != nil {
		return nil, err
	}

	// Reconstruct merged SDL by concatenating sources.
	mergedLen := len(schemas)
	for _, sdl := range schemas {
		mergedLen += len(sdl)
	}
	merged := make([]byte, 0, mergedLen)
	for _, sdl := range schemas {
		merged = append(merged, sdl...)
		merged = append(merged, '\n')
	}

	return parsedSchemaFromAST(merged, schema), nil
}

func appSyncSchemaPreludeSource() *ast.Source {
	return &ast.Source{
		Name:  "appsync-prelude.graphql",
		Input: appSyncSchemaDefinitions,
	}
}

func validateAppSyncSchemaDefinitions(schema *ast.Schema) error {
	for name, def := range schema.Types {
		if def.Kind == ast.Scalar {
			if !isAppSyncBuiltInScalar(name) {
				return fmt.Errorf("custom scalar %s is not supported", name)
			}
			continue
		}
		if def.Kind == ast.Object && strings.HasPrefix(name, "AWS") {
			return fmt.Errorf("custom type %s cannot use the reserved AWS prefix", name)
		}
	}
	return nil
}

func isAppSyncBuiltInScalar(name string) bool {
	switch name {
	case "ID", "String", "Int", "Float", "Boolean",
		"AWSDate", "AWSTime", "AWSDateTime", "AWSTimestamp", "AWSEmail",
		"AWSJSON", "AWSPhone", "AWSURL", "AWSIPAddress":
		return true
	default:
		return false
	}
}

func parsedSchemaFromAST(raw []byte, schema *ast.Schema) *ParsedSchema {
	parsed := &ParsedSchema{
		Raw:       raw,
		Opaque:    schema,
		TypeNames: make([]string, 0, len(schema.Types)),
	}
	for name := range schema.Types {
		parsed.TypeNames = append(parsed.TypeNames, name)
	}
	if schema.Query != nil {
		parsed.QueryType = schema.Query.Name
	}
	if schema.Mutation != nil {
		parsed.MutationType = schema.Mutation.Name
	}
	if schema.Subscription != nil {
		parsed.SubscriptionType = schema.Subscription.Name
	}
	return parsed
}

// Get returns a cached ParsedSchema for the given API, or nil.
func (p *schemaParser) Get(apiID string) *ParsedSchema {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.cache[apiID]
}

// Put stores a ParsedSchema in the cache.
func (p *schemaParser) Put(apiID string, schema *ParsedSchema) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cache[apiID] = schema
}

// Evict removes a cached schema.
func (p *schemaParser) Evict(apiID string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.cache, apiID)
}
