package dynamodb

// index_maintenance.go implements the write-path GSI index maintenance from
// docs/plans/dynamodb-gsi-design.md section 3: for every write to a table
// that declares one or more GlobalSecondaryIndexes, decide which index rows
// need to change, using AWS's own write-amplification table as the source
// of truth:
//
//	Base-table write                                    | Index write
//	New item defines the indexed attribute(s)           | 1 write  (insert)
//	Update changes an indexed key attribute's value A->B | 2 writes (delete old, insert new)
//	Update deletes a previously-defined indexed attr     | 1 write  (delete)
//	Item never had the indexed attribute, still doesn't  | 0 writes (sparse)
//	Update changes only a projected (non-key) attribute  | 1 write  (re-upsert projection)
//	Update changes an attribute neither keyed/projected  | 0 writes (unchanged) *
//
// * This implementation always re-upserts the projected copy when the
// index's own key is unchanged, rather than deep-diffing the projected
// attribute set to detect the true no-op case. The observable result is
// identical either way (the stored projection is correct), and there is no
// client-visible way to observe "0 vs 1 index writes" in this phase (no read
// path consults the index structure yet — see handler.go's scanTyped/
// queryTyped, still routed at the full-scan fallback per the phase-2/phase-3
// split). Simplicity was chosen over exactly matching AWS's internal write
// count for a case that isn't behaviorally distinguishable.
//
// This file only contains the *decision* logic (which mutations are
// needed) and the *projection* logic (what attribute subset an index row
// stores). Applying the resulting []indexMutation atomically alongside a
// base-item write lives in item_store.go (putWithIndexMutations /
// removeWithIndexMutations); the storage shapes (memory tree / SQL table)
// also live there.

// diffIndexMutations computes the index-row mutations required to bring
// every GSI on table in sync with a base-table write from oldItem (nil if
// the item didn't previously exist, e.g. a fresh PutItem or a not-found
// UpdateItem upsert) to newItem (nil for a DeleteItem — there is no "new"
// state). Returns nil for a table with no GSIs (the common case), so
// callers can skip the whole index-write path cheaply without a separate
// len() check.
//
// The base item's own primary key never changes between oldItem and newItem
// for a single write (PutItem/UpdateItem/DeleteItem all address one fixed
// (hash, sort) identity — DynamoDB has no operation that "moves" an item to
// a different primary key), so baseHash/baseSort is computed once from
// whichever of newItem/oldItem is non-nil.
func diffIndexMutations(table *Table, oldItem, newItem Item) []indexMutation {
	if len(table.GlobalSecondaryIndexes) == 0 {
		return nil
	}

	baseItem := newItem
	if baseItem == nil {
		baseItem = oldItem
	}
	if baseItem == nil {
		return nil // both nil — nothing to diff (shouldn't happen in practice)
	}
	baseHash, baseSort, aerr := resolveStorageKeys(table, baseItem)
	if aerr != nil {
		// Malformed item (missing/invalid key attributes) — isolate per
		// CLAUDE.md's rule rather than failing the whole write; the base
		// item write itself already validates keys upstream of this call,
		// so this is a defensive fallback, not the primary validation path.
		return nil
	}

	var mutations []indexMutation
	for i := range table.GlobalSecondaryIndexes {
		idx := &table.GlobalSecondaryIndexes[i]
		oldHash, oldSort, oldOK := indexKeyComponents(table, idx, oldItem)
		newHash, newSort, newOK := indexKeyComponents(table, idx, newItem)

		switch {
		case !oldOK && !newOK:
			// Sparse before and after — nothing to do.

		case oldOK && !newOK:
			// Update removed the indexed attribute (or the item was
			// deleted): the old row must go.
			mutations = append(mutations, indexMutation{
				indexName: idx.IndexName,
				op:        indexMutationDelete,
				indexHash: oldHash, indexSort: oldSort,
				baseHash: baseHash, baseSort: baseSort,
			})

		case !oldOK && newOK:
			// Item newly defines the indexed attribute(s) — insert.
			mutations = append(mutations, indexMutation{
				indexName: idx.IndexName,
				op:        indexMutationUpsert,
				indexHash: newHash, indexSort: newSort,
				baseHash: baseHash, baseSort: baseSort,
				item: projectForIndex(table, idx, newItem),
			})

		default: // oldOK && newOK — present both before and after
			if oldHash != newHash || oldSort != newSort {
				// Indexed key value itself changed (A -> B): the old
				// composite key is a different tree/table row than the
				// new one, so the old row must be explicitly removed —
				// an upsert at the new key alone would leave the old key's
				// row behind as a stale duplicate.
				mutations = append(mutations, indexMutation{
					indexName: idx.IndexName,
					op:        indexMutationDelete,
					indexHash: oldHash, indexSort: oldSort,
					baseHash: baseHash, baseSort: baseSort,
				})
			}
			// Same key (common case) or key changed (above): either way
			// the current row at the new key must reflect the current
			// projection.
			mutations = append(mutations, indexMutation{
				indexName: idx.IndexName,
				op:        indexMutationUpsert,
				indexHash: newHash, indexSort: newSort,
				baseHash: baseHash, baseSort: baseSort,
				item: projectForIndex(table, idx, newItem),
			})
		}
	}
	return mutations
}

// indexKeyComponents extracts the storage-encoded (hash, sort) pair for one
// GSI's key attributes from item, applying the AWS sparse-index rule
// (dynamodb-gsi-design.md section 2/3): ok is false unless BOTH the index's
// hash key and (if the index defines one) sort key attribute are present on
// item — DynamoDB "only tracks data items where its key attributes actually
// exist," and a composite index key with a missing sort-key component is
// exactly as absent as a missing hash key.
//
// item may be nil (an item that doesn't exist, e.g. before a fresh
// PutItem, or after a DeleteItem) — this always returns ok=false for a nil
// item, which is exactly the semantics diffIndexMutations needs.
func indexKeyComponents(table *Table, idx *SecondaryIndex, item Item) (hash, sort string, ok bool) {
	if item == nil || idx == nil {
		return "", "", false
	}
	hashName := indexHashKeyName(idx)
	if hashName == "" {
		return "", "", false
	}
	hashAttr, present := item[hashName]
	if !present {
		return "", "", false
	}
	hash = encodeStorageKeyComponent(keyAttrType(table, hashName), extractKeyValue(hashAttr))

	if sortName := indexSortKeyName(idx); sortName != "" {
		sortAttr, present := item[sortName]
		if !present {
			return "", "", false
		}
		sort = encodeStorageKeyComponent(keyAttrType(table, sortName), extractKeyValue(sortAttr))
	}
	return hash, sort, true
}

// projectForIndex returns the subset of item's attributes that idx's
// Projection stores in the index — AWS's ALL / KEYS_ONLY / INCLUDE
// semantics (dynamodb-gsi-design.md section 2) — which is what's physically
// written to the index row, not the full base item. This is the storage-
// layer half of the "global secondary index queries cannot fetch attributes
// from the parent table" fidelity fix: once a read path consults this
// structure (phase 3), it will only ever have what was projected here to
// work with, closing the gap where today's fallback reads the full base
// item and applies projection as a display-time filter.
func projectForIndex(table *Table, idx *SecondaryIndex, item Item) Item {
	if item == nil {
		return Item{}
	}
	switch idx.Projection.ProjectionType {
	case "ALL":
		return cloneItem(item)
	case "INCLUDE":
		out := indexKeysOnlyProjection(table, idx, item)
		for _, attr := range idx.Projection.NonKeyAttributes {
			if v, ok := item[attr]; ok {
				out[attr] = v
			}
		}
		return out
	default: // "KEYS_ONLY", or ProjectionType unset — KEYS_ONLY is the floor
		return indexKeysOnlyProjection(table, idx, item)
	}
}

// indexKeysOnlyProjection returns the table's primary key attributes plus
// the index's own key attributes — the KEYS_ONLY projection, and the base
// every other projection type builds on (AWS: "KEYS_ONLY... consists only
// of the table and index keys").
func indexKeysOnlyProjection(table *Table, idx *SecondaryIndex, item Item) Item {
	out := Item{}
	if hn := table.hashKeyName(); hn != "" {
		if v, ok := item[hn]; ok {
			out[hn] = v
		}
	}
	if sn := table.sortKeyName(); sn != "" {
		if v, ok := item[sn]; ok {
			out[sn] = v
		}
	}
	if hn := indexHashKeyName(idx); hn != "" {
		if v, ok := item[hn]; ok {
			out[hn] = v
		}
	}
	if sn := indexSortKeyName(idx); sn != "" {
		if v, ok := item[sn]; ok {
			out[sn] = v
		}
	}
	return out
}
