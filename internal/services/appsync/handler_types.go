package appsync

// handler_types.go — Type definition CRUD handlers.
//
// Implemented:
//   - CreateType  POST   /v1/apis/{apiId}/types
//   - GetType     GET    /v1/apis/{apiId}/types/{typeName}
//   - ListTypes   GET    /v1/apis/{apiId}/types
//   - UpdateType  POST   /v1/apis/{apiId}/types/{typeName}
//   - DeleteType  DELETE /v1/apis/{apiId}/types/{typeName}
//
// Types represent GraphQL type definitions within an API. The Types API allows
// programmatic creation and retrieval of types, complementing schema upload.
// When a schema is uploaded, types are automatically extracted from it. Types
// can also be created independently via CreateType.

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// typeNameRegex extracts the type name from an SDL type definition.
// Matches: type Foo, input Foo, enum Foo, union Foo, interface Foo, scalar Foo.
var typeNameRegex = regexp.MustCompile(`(?m)^\s*(?:type|input|enum|union|interface|scalar)\s+(\w+)`)

// extractTypeName parses the type name from an SDL definition fragment.
func extractTypeName(definition string) string {
	m := typeNameRegex.FindStringSubmatch(definition)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

// CreateType handles POST /v1/apis/{apiId}/types.
func (h *Handler) CreateType(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	var req struct {
		Definition string `json:"definition"`
		Format     string `json:"format"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	if req.Definition == "" {
		protocol.WriteJSONError(w, r, badRequestError("definition is required"))
		return
	}
	if req.Format == "" {
		req.Format = "SDL"
	}

	name := extractTypeName(req.Definition)
	if name == "" {
		protocol.WriteJSONError(w, r, badRequestError("could not extract type name from definition"))
		return
	}

	td := &TypeDefinition{
		Name:       name,
		Definition: req.Definition,
		Format:     req.Format,
		Arn:        protocol.ARN(h.region(r), h.cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/types/%s", apiID, name)),
	}

	if err := h.store.PutType(r.Context(), apiID, td); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"type": td})
}

// GetType handles GET /v1/apis/{apiId}/types/{typeName}.
func (h *Handler) GetType(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")

	// First check the store for explicitly created types.
	td, err := h.store.GetType(r.Context(), apiID, typeName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	// If not found in store, check the parsed schema for schema-derived types.
	if td == nil {
		td = h.getSchemaType(r.Context(), apiID, typeName)
	}

	if td == nil {
		protocol.WriteJSONError(w, r, notFoundError("Type "+typeName+" not found."))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"type": td})
}

// ListTypes handles GET /v1/apis/{apiId}/types.
func (h *Handler) ListTypes(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	// Collect explicitly created types from the store.
	stored, err := h.store.ListTypes(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	// Build a set of stored type names for deduplication.
	seen := make(map[string]bool, len(stored))
	for _, td := range stored {
		seen[td.Name] = true
	}

	// Add schema-derived types that aren't already in the store.
	schemaTypes := h.getSchemaTypes(r.Context(), apiID)
	for _, st := range schemaTypes {
		if !seen[st.Name] {
			stored = append(stored, st)
			seen[st.Name] = true
		}
	}

	writeListJSON(w, r, "types", stored)
}

// UpdateType handles POST /v1/apis/{apiId}/types/{typeName}.
func (h *Handler) UpdateType(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")

	var req struct {
		Definition string `json:"definition"`
		Format     string `json:"format"`
	}
	if !serviceutil.DecodeJSON(w, r, &req) {
		return
	}

	// Load existing (from store or schema).
	existing, err := h.store.GetType(r.Context(), apiID, typeName)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		existing = h.getSchemaType(r.Context(), apiID, typeName)
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError("Type "+typeName+" not found."))
		return
	}

	if req.Definition != "" {
		existing.Definition = req.Definition
	}
	if req.Format != "" {
		existing.Format = req.Format
	}

	if err := h.store.PutType(r.Context(), apiID, existing); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"type": existing})
}

// DeleteType handles DELETE /v1/apis/{apiId}/types/{typeName}.
func (h *Handler) DeleteType(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	typeName := chi.URLParam(r, "typeName")

	if err := h.store.DeleteType(r.Context(), apiID, typeName); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{})
}

// ─── Schema-derived type helpers ─────────────────────────────────────────────

// getSchemaType extracts a single type definition from the parsed schema cache.
func (h *Handler) getSchemaType(ctx context.Context, apiID, typeName string) *TypeDefinition {
	parsed := h.sp.Get(apiID)
	if parsed == nil {
		stored, err := h.store.GetSchema(ctx, apiID)
		if err != nil || stored == nil {
			return nil
		}
		reparsed, err := h.sp.Parse(stored.Definition)
		if err != nil {
			return nil
		}
		h.sp.Put(apiID, reparsed)
		parsed = reparsed
	}

	for _, name := range parsed.TypeNames {
		if name == typeName {
			return h.buildTypeDefinition(ctx, apiID, parsed, name)
		}
	}
	return nil
}

// getSchemaTypes extracts all user-defined types from the parsed schema.
func (h *Handler) getSchemaTypes(ctx context.Context, apiID string) []*TypeDefinition {
	parsed := h.sp.Get(apiID)
	if parsed == nil {
		stored, err := h.store.GetSchema(ctx, apiID)
		if err != nil || stored == nil {
			return nil
		}
		reparsed, err := h.sp.Parse(stored.Definition)
		if err != nil {
			return nil
		}
		h.sp.Put(apiID, reparsed)
		parsed = reparsed
	}

	types := make([]*TypeDefinition, 0, len(parsed.TypeNames))
	for _, name := range parsed.TypeNames {
		types = append(types, h.buildTypeDefinition(ctx, apiID, parsed, name))
	}
	return types
}

// buildTypeDefinition constructs a TypeDefinition for a schema-derived type.
func (h *Handler) buildTypeDefinition(ctx context.Context, apiID string, parsed *ParsedSchema, typeName string) *TypeDefinition {
	// Build a minimal SDL definition from the raw schema.
	// For schema-derived types, the definition is extracted from the raw SDL.
	def := extractTypeDefinitionFromSDL(string(parsed.Raw), typeName)

	return &TypeDefinition{
		Name:       typeName,
		Definition: def,
		Format:     "SDL",
		Arn:        protocol.ARN(h.regionCtx(ctx), h.cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/types/%s", apiID, typeName)),
	}
}

// extractTypeDefinitionFromSDL extracts a single type definition block from raw SDL.
func extractTypeDefinitionFromSDL(sdl, typeName string) string {
	// Look for "type TypeName", "input TypeName", etc.
	keywords := []string{"type", "input", "enum", "union", "interface", "scalar"}
	for _, kw := range keywords {
		prefix := kw + " " + typeName
		idx := strings.Index(sdl, prefix)
		if idx < 0 {
			continue
		}
		// Find the end of this type block (closing brace or next type).
		rest := sdl[idx:]
		braceStart := strings.Index(rest, "{")
		if braceStart < 0 {
			// Scalar or union without braces — take until newline.
			nl := strings.Index(rest, "\n")
			if nl < 0 {
				return rest
			}
			return rest[:nl]
		}
		depth := 0
		for i, ch := range rest {
			switch ch {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return rest[:i+1]
				}
			}
		}
		return rest
	}
	return typeName
}
