package appsync

// schema_parser.go — SchemaParser implementation using gqlparser.
//
// Parses and validates GraphQL SDL schemas. Used by StartSchemaCreation to
// reject invalid SDL and by the execution engine for query validation.

import (
	"fmt"
	"sync"

	"github.com/vektah/gqlparser/v2"
	"github.com/vektah/gqlparser/v2/ast"
)

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

	schema, gqlErr := gqlparser.LoadSchema(src)
	if gqlErr != nil {
		return nil, fmt.Errorf("schema validation failed: %w", gqlErr)
	}

	// AppSync requires a Query type.
	if schema.Query == nil {
		return nil, fmt.Errorf("schema must define a Query type")
	}

	parsed := &ParsedSchema{
		Raw:    sdl,
		Opaque: schema,
	}

	// Extract type names.
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

	return parsed, nil
}

// Merge combines multiple SDL sources into a single merged schema.
func (p *schemaParser) Merge(schemas [][]byte) (*ParsedSchema, error) {
	sources := make([]*ast.Source, len(schemas))
	for i, sdl := range schemas {
		sources[i] = &ast.Source{
			Name:  fmt.Sprintf("source_%d.graphql", i),
			Input: string(sdl),
		}
	}

	schema, gqlErr := gqlparser.LoadSchema(sources...)
	if gqlErr != nil {
		return nil, fmt.Errorf("schema merge failed: %w", gqlErr)
	}

	// Reconstruct merged SDL by concatenating sources.
	var merged []byte
	for _, sdl := range schemas {
		merged = append(merged, sdl...)
		merged = append(merged, '\n')
	}

	parsed := &ParsedSchema{
		Raw:    merged,
		Opaque: schema,
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

	return parsed, nil
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
