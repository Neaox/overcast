package rds

// handler_stubs.go contains every RDS handler that is not yet implemented.
// Each method returns HTTP 501 Not Implemented with x-emulator-unsupported: true.
//
// Convention: when an operation is implemented, move its method body out of this
// file and into handler.go (or handler_<group>.go for large feature groups).
// handler.go is the authoritative inventory of what actually works.

import (
	"net/http"

	"github.com/Neaox/overcast/internal/protocol"
)

// ── Snapshot operations ──────────────────────────────────────────────────────

// CreateDBSnapshot creates a snapshot of a DB instance.
func (h *Handler) CreateDBSnapshot(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// DeleteDBSnapshot deletes a DB snapshot.
func (h *Handler) DeleteDBSnapshot(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// DescribeDBSnapshots returns information about DB snapshots.
func (h *Handler) DescribeDBSnapshots(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// RestoreDBInstanceFromDBSnapshot restores a DB instance from a snapshot.
func (h *Handler) RestoreDBInstanceFromDBSnapshot(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// CreateDBClusterSnapshot creates a snapshot of an Aurora DB cluster.
func (h *Handler) CreateDBClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// DeleteDBClusterSnapshot deletes a DB cluster snapshot.
func (h *Handler) DeleteDBClusterSnapshot(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// DescribeDBClusterSnapshots returns information about DB cluster snapshots.
func (h *Handler) DescribeDBClusterSnapshots(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// ── Other common operations ──────────────────────────────────────────────────

// RebootDBInstance reboots a DB instance.
func (h *Handler) RebootDBInstance(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// DescribeDBLogFiles returns a list of DB log files for the DB instance.
func (h *Handler) DescribeDBLogFiles(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// DownloadDBLogFilePortion downloads all or a portion of a specified log file.
func (h *Handler) DownloadDBLogFilePortion(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// AddTagsToResource adds metadata tags to an Amazon RDS resource.
func (h *Handler) AddTagsToResource(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// RemoveTagsFromResource removes metadata tags from an Amazon RDS resource.
func (h *Handler) RemoveTagsFromResource(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}

// ListTagsForResource lists all tags on an Amazon RDS resource.
func (h *Handler) ListTagsForResource(w http.ResponseWriter, r *http.Request) {
	protocol.NotImplementedQueryXML(w, r)
}
