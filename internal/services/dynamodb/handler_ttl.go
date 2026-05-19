package dynamodb

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/serviceutil"
)

const ttlSweepInterval = 1 * time.Hour

// startTTLSweeper starts a background goroutine that periodically scans
// TTL-enabled tables and deletes expired items (where the TTL attribute
// value is a Unix epoch timestamp in the past).
//
// Real AWS deletes expired items within 48 hours. The emulator sweeps
// once per hour — close to production behaviour and cheap. Tests use a
// mock clock so the interval has no effect on test speed.
func (h *Handler) startTTLSweeper(ctx context.Context) {
	ticker := h.clk.Ticker(ttlSweepInterval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				h.sweepExpiredItems(ctx)
			}
		}
	}()
}

// sweepExpiredItems scans all TTL-enabled tables across all regions and deletes expired items.
func (h *Handler) sweepExpiredItems(ctx context.Context) {
	pairs, err := h.store.scanAllTables(ctx)
	if err != nil {
		h.log.Error("ttl: scan all tables", zap.Error(err))
		return
	}

	now := h.clk.Now().Unix()

	for _, kv := range pairs {
		var table Table
		if err := json.Unmarshal([]byte(kv.Value), &table); err != nil {
			_, name := serviceutil.SplitRegionKey(kv.Key)
			h.log.Error("ttl: unmarshal table", zap.String("key", name), zap.Error(err))
			continue
		}
		if !table.ttlEnabled() {
			continue
		}
		h.sweepTable(ctx, &table, now)
	}
}

// sweepTable deletes items whose TTL attribute has expired (value > 0 and
// <= now). Uses the TTL-aware scan so only expired items are returned from
// the store, avoiding a full table scan in the SQL backend.
func (h *Handler) sweepTable(ctx context.Context, table *Table, nowUnix int64) {
	ttlAttr := table.TTL.AttributeName
	items, aerr := h.store.scanExpiredTTL(ctx, table.TableName, ttlAttr, nowUnix)
	if aerr != nil {
		h.log.Error("ttl: scan expired items", zap.String("table", table.TableName), zap.Error(aerr))
		return
	}

	for _, item := range items {
		// Capture old image before deleting for stream records.
		var oldItem Item
		if table.streamEnabled() {
			oldItem = item
		}

		if aerr := h.store.deleteItem(ctx, table, item); aerr != nil {
			h.log.Error("ttl: delete expired item",
				zap.String("table", table.TableName),
				zap.Error(aerr),
			)
			continue
		}

		if table.streamEnabled() && oldItem != nil {
			h.publishDeleteStreamRecord(ctx, table, extractKeys(table, item), oldItem)
		}

		h.log.Debug("ttl: expired item deleted",
			zap.String("table", table.TableName),
		)
	}
}

// parseTTLValue extracts a Unix epoch timestamp from a DynamoDB attribute value.
// The attribute must be of type N (Number). Returns (value, true) on success.
func parseTTLValue(av attrValue) (int64, bool) {
	nVal, ok := av["N"]
	if !ok {
		return 0, false
	}
	s, ok := nVal.(string)
	if !ok {
		return 0, false
	}
	// Parse as float64 first (AWS allows decimal epoch values), then truncate.
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return int64(math.Trunc(f)), true
}
