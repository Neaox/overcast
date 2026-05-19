package elasticache

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// handler_stubs.go — all unimplemented ElastiCache operations.
// When implementing an operation, move its handler out of this file
// and into handler.go (or the appropriate handler_<group>.go).

// TODO(priority:P3): Implement RebootCacheCluster.
func (h *Handler) RebootCacheCluster(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// TODO(priority:P3): Implement DescribeCacheEngineVersions — can return a
// static list of supported redis/valkey versions.
func (h *Handler) DescribeCacheEngineVersions(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}
