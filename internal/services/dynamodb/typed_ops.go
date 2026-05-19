package dynamodb

import (
	"hash/crc32"
	"net/http"

	"github.com/Neaox/overcast/internal/protocol/codec"
	"github.com/Neaox/overcast/internal/protocol/op"
)

func (h *Handler) rawOps() map[string]op.Operation {
	ops := make(map[string]op.Operation, len(h.ops))
	for name, fn := range h.ops {
		ops[name] = op.NewRaw(name, fn)
	}
	return ops
}

func (h *Handler) typedOps() map[string]op.Operation {
	ops := h.rawOps()
	ops["CreateTable"] = op.NewTyped[createTableRequest, createTableResponse](
		"CreateTable", h.createTableTyped,
	)
	ops["ListTables"] = op.NewTyped[listTablesRequest, listTablesResponse](
		"ListTables", h.listTablesTyped,
	)
	ops["DescribeTable"] = op.NewTyped[describeTableRequest, describeTableResponse](
		"DescribeTable", h.describeTableTyped,
	)
	ops["DeleteTable"] = op.NewTyped[deleteTableRequest, describeTableResponse](
		"DeleteTable", h.deleteTableTyped,
	)
	ops["UpdateTable"] = op.NewTyped[updateTableRequest, createTableResponse](
		"UpdateTable", h.updateTableTyped,
	)
	ops["PutItem"] = op.NewTyped[putItemRequest, putItemResponse](
		"PutItem", h.putItemTyped,
	)
	ops["GetItem"] = op.NewTyped[getItemRequest, getItemResponse](
		"GetItem", h.getItemTyped,
	)
	ops["DeleteItem"] = op.NewTyped[deleteItemRequest, deleteItemResponse](
		"DeleteItem", h.deleteItemTyped,
	)
	ops["UpdateItem"] = op.NewTyped[updateItemRequest, updateItemResponse](
		"UpdateItem", h.updateItemTyped,
	)
	ops["BatchGetItem"] = op.NewTyped[batchGetItemRequest, batchGetItemResponse](
		"BatchGetItem", h.batchGetItemTyped,
	)
	ops["BatchWriteItem"] = op.NewTyped[batchWriteItemRequest, batchWriteItemResponse](
		"BatchWriteItem", h.batchWriteItemTyped,
	)
	ops["Scan"] = op.NewTypedAny[scanRequest](
		"Scan", h.scanTyped,
	)
	ops["Query"] = op.NewTypedAny[queryRequest](
		"Query", h.queryTyped,
	)
	ops["UpdateTimeToLive"] = op.NewTyped[updateTimeToLiveRequest, updateTimeToLiveResponse](
		"UpdateTimeToLive", h.updateTimeToLiveTyped,
	)
	ops["DescribeTimeToLive"] = op.NewTyped[describeTimeToLiveRequest, describeTimeToLiveResponse](
		"DescribeTimeToLive", h.describeTimeToLiveTyped,
	)
	ops["TransactWriteItems"] = op.NewTyped[transactWriteItemsRequest, struct{}](
		"TransactWriteItems", h.transactWriteItemsTyped,
	)
	ops["TransactGetItems"] = op.NewTyped[transactGetItemsRequest, transactGetItemsResponse](
		"TransactGetItems", h.transactGetItemsTyped,
	)
	return ops
}

// Operations implements router.ProtocolService.
func (s *Service) Operations() []op.Operation {
	ops := s.handler.typedOps()
	out := make([]op.Operation, 0, len(ops))
	for _, o := range ops {
		out = append(out, o)
	}
	return out
}

// SupportedProtocols implements router.ProtocolService.
func (s *Service) SupportedProtocols() []codec.Codec {
	return []codec.Codec{codec.JSON10, codec.JSON11, codec.RPCv2CBOR}
}

func wrapCRC32(w http.ResponseWriter) *crc32ResponseWriter {
	return &crc32ResponseWriter{ResponseWriter: w, hash: crc32.NewIEEE()}
}
