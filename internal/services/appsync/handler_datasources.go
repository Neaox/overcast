package appsync

// handler_datasources.go — data source CRUD handlers.
//
// Implemented:
//   - CreateDataSource  POST   /v1/apis/{apiId}/datasources
//   - GetDataSource     GET    /v1/apis/{apiId}/datasources/{name}
//   - ListDataSources   GET    /v1/apis/{apiId}/datasources
//   - UpdateDataSource  POST   /v1/apis/{apiId}/datasources/{name}
//   - DeleteDataSource  DELETE /v1/apis/{apiId}/datasources/{name}

import (
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// TODO(priority:P2): validate data source type matches provided config:
//   AMAZON_DYNAMODB → requires dynamodbConfig (tableName, region)
//   AWS_LAMBDA      → requires lambdaConfig (lambdaFunctionArn)
//   HTTP            → requires httpConfig (endpoint)
//   AMAZON_OPENSEARCH_SERVICE → requires openSearchServiceConfig
//   RELATIONAL_DATABASE → requires relationalDatabaseConfig
//   AMAZON_EVENTBRIDGE → requires eventBridgeConfig
//   NONE            → no config required

// CreateDataSource handles POST /v1/apis/{apiId}/datasources.
func (h *Handler) CreateDataSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	var ds DataSource
	if !serviceutil.DecodeJSON(w, r, &ds) {
		return
	}

	if ds.Name == "" {
		protocol.WriteJSONError(w, r, badRequestError("name is required"))
		return
	}

	// Check for duplicate name.
	existing, err := h.store.GetDataSource(r.Context(), apiID, ds.Name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing != nil {
		protocol.WriteJSONError(w, r, conflictError(fmt.Sprintf("Data source %s already exists.", ds.Name)))
		return
	}

	ds.ApiId = apiID
	ds.DataSourceArn = protocol.ARN(h.region(r), h.cfg.AccountID, "appsync", fmt.Sprintf("apis/%s/datasources/%s", apiID, ds.Name))

	if storeErr := h.store.PutDataSource(r.Context(), apiID, &ds); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	h.publish(r, events.AppSyncDataSourceCreated, events.ResourcePayload{Name: ds.Name, ARN: ds.DataSourceArn})

	writeJSON(w, r, http.StatusCreated, map[string]any{"dataSource": &ds})
}

// GetDataSource handles GET /v1/apis/{apiId}/datasources/{name}.
func (h *Handler) GetDataSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	name := chi.URLParam(r, "name")

	ds, err := h.store.GetDataSource(r.Context(), apiID, name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if ds == nil {
		protocol.WriteJSONError(w, r, notFoundError(fmt.Sprintf("Data source %s not found.", name)))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"dataSource": ds})
}

// ListDataSources handles GET /v1/apis/{apiId}/datasources.
func (h *Handler) ListDataSources(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")

	sources, err := h.store.ListDataSources(r.Context(), apiID)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"dataSources": sources})
}

// UpdateDataSource handles PUT /v1/apis/{apiId}/datasources/{name}.
func (h *Handler) UpdateDataSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	name := chi.URLParam(r, "name")

	existing, err := h.store.GetDataSource(r.Context(), apiID, name)
	if err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}
	if existing == nil {
		protocol.WriteJSONError(w, r, notFoundError(fmt.Sprintf("Data source %s not found.", name)))
		return
	}

	var ds DataSource
	if !serviceutil.DecodeJSON(w, r, &ds) {
		return
	}

	// Preserve server-generated fields.
	ds.Name = name
	ds.ApiId = apiID
	ds.DataSourceArn = existing.DataSourceArn

	if storeErr := h.store.PutDataSource(r.Context(), apiID, &ds); storeErr != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, storeErr))
		return
	}

	writeJSON(w, r, http.StatusOK, map[string]any{"dataSource": &ds})
}

// DeleteDataSource handles DELETE /v1/apis/{apiId}/datasources/{name}.
func (h *Handler) DeleteDataSource(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAPI(w, r); !ok {
		return
	}
	apiID := chi.URLParam(r, "apiId")
	name := chi.URLParam(r, "name")

	if err := h.store.DeleteDataSource(r.Context(), apiID, name); err != nil {
		protocol.WriteJSONError(w, r, protocol.Wrap(protocol.ErrInternalError, err))
		return
	}

	h.publish(r, events.AppSyncDataSourceDeleted, events.ResourcePayload{Name: name})

	writeJSON(w, r, http.StatusOK, map[string]any{})
}
