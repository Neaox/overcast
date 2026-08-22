package elasticache

// Tags are keyed by resource ARN in a namespace of their own, so nothing ties
// them to the lifetime of the record they describe. Deleting a cache left them
// behind, so ListTagsForResource kept answering for a resource that was gone.
//
// The check is at the store, because that is where every delete path — the
// Query handlers and the typed operations alike — funnels through, so a new
// resource type gets the cleanup by writing one line rather than remembering to.

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/config"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
	"github.com/Neaox/overcast/internal/state"
)

func newTagLifetimeStore() *cacheStore {
	return newCacheStore(state.NewMemoryStore(), "us-east-1")
}

// storedTagCount reports how many tag blobs the whole namespace holds, which is
// what says whether a delete cleaned up or merely stopped pointing at it.
func storedTagCount(t *testing.T, s *cacheStore) int {
	t.Helper()
	kvs, err := s.store.Scan(context.Background(), nsTags, "")
	if err != nil {
		t.Fatalf("scan tags: %v", err)
	}
	return len(kvs)
}

func tagResource(t *testing.T, s *cacheStore, arn string) {
	t.Helper()
	if _, aerr := serviceutil.ApplyStoreTags(context.Background(), s.tags(), arn,
		map[string]string{"env": "prod"}, cacheTagCfg); aerr != nil {
		t.Fatalf("tag %s: %v", arn, aerr)
	}
}

// Every taggable ElastiCache resource, so a delete path that forgets the
// cleanup is a named failure rather than a gap nobody notices.
func TestDelete_takesTagsWithIt(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		arn    string
		create func(s *cacheStore) *protocol.AWSError
		remove func(s *cacheStore) *protocol.AWSError
	}{
		{
			name: "cache cluster",
			arn:  "arn:aws:elasticache:us-east-1:000000000000:cluster:c1",
			create: func(s *cacheStore) *protocol.AWSError {
				return s.putCacheCluster(ctx, &CacheCluster{
					CacheClusterId: "c1",
					ARN:            "arn:aws:elasticache:us-east-1:000000000000:cluster:c1",
				})
			},
			remove: func(s *cacheStore) *protocol.AWSError { return s.deleteCacheCluster(ctx, "c1") },
		},
		{
			name: "replication group",
			arn:  "arn:aws:elasticache:us-east-1:000000000000:replicationgroup:r1",
			create: func(s *cacheStore) *protocol.AWSError {
				return s.putReplicationGroup(ctx, &ReplicationGroup{
					ReplicationGroupId: "r1",
					ARN:                "arn:aws:elasticache:us-east-1:000000000000:replicationgroup:r1",
				})
			},
			remove: func(s *cacheStore) *protocol.AWSError { return s.deleteReplicationGroup(ctx, "r1") },
		},
		{
			name: "serverless cache",
			arn:  "arn:aws:elasticache:us-east-1:000000000000:serverlesscache:s1",
			create: func(s *cacheStore) *protocol.AWSError {
				return s.putServerlessCache(ctx, &ServerlessCache{
					ServerlessCacheName: "s1",
					ARN:                 "arn:aws:elasticache:us-east-1:000000000000:serverlesscache:s1",
				})
			},
			remove: func(s *cacheStore) *protocol.AWSError { return s.deleteServerlessCache(ctx, "s1") },
		},
		{
			name: "cache subnet group",
			arn:  "arn:aws:elasticache:us-east-1:000000000000:subnetgroup:sg1",
			create: func(s *cacheStore) *protocol.AWSError {
				return s.putCacheSubnetGroup(ctx, &CacheSubnetGroup{
					CacheSubnetGroupName: "sg1",
					ARN:                  "arn:aws:elasticache:us-east-1:000000000000:subnetgroup:sg1",
				})
			},
			remove: func(s *cacheStore) *protocol.AWSError { return s.deleteCacheSubnetGroup(ctx, "sg1") },
		},
		{
			name: "cache parameter group",
			arn:  "arn:aws:elasticache:us-east-1:000000000000:parametergroup:pg1",
			create: func(s *cacheStore) *protocol.AWSError {
				return s.putCacheParameterGroup(ctx, &CacheParameterGroup{
					CacheParameterGroupName: "pg1",
					ARN:                     "arn:aws:elasticache:us-east-1:000000000000:parametergroup:pg1",
				})
			},
			remove: func(s *cacheStore) *protocol.AWSError { return s.deleteCacheParameterGroup(ctx, "pg1") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a tagged resource
			s := newTagLifetimeStore()
			if aerr := tc.create(s); aerr != nil {
				t.Fatalf("create: %v", aerr)
			}
			tagResource(t, s, tc.arn)
			if got := storedTagCount(t, s); got != 1 {
				t.Fatalf("stored tag blobs = %d, want 1 before the delete", got)
			}

			// When: it is deleted
			if aerr := tc.remove(s); aerr != nil {
				t.Fatalf("delete: %v", aerr)
			}

			// Then: its tags went with it
			if got := storedTagCount(t, s); got != 0 {
				t.Fatalf("stored tag blobs = %d, want 0 after the delete", got)
			}
		})
	}
}

// TestCreateReplicationGroupTyped_tagsAppliedAtCreate exercises the CBOR
// typed path directly (issue #1196, Axis B): createReplicationGroupTyped
// used to be a second, independent implementation of CreateReplicationGroup
// that ignored Tags entirely, alongside the Query-XML handler's own copy.
func TestCreateReplicationGroupTyped_tagsAppliedAtCreate(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	ctx := context.Background()

	if _, aerr := s.handler.createReplicationGroupTyped(ctx, &ecCreateReplicationGroupReq{
		ReplicationGroupId: "typed-tagged-rg",
		Tags:               []ecTag{{Key: "env", Value: "prod"}},
	}); aerr != nil {
		t.Fatalf("createReplicationGroupTyped: %v", aerr)
	}

	tags, aerr := s.handler.store.tags().Load(ctx, "arn:aws:elasticache:us-east-1:000000000000:replicationgroup:typed-tagged-rg")
	if aerr != nil {
		t.Fatalf("load tags: %v", aerr)
	}
	if tags["env"] != "prod" {
		t.Errorf("tags = %v, want env=prod", tags)
	}
}

// TestCreateReplicationGroupTyped_invalidTagRejected: an invalid tag must be
// rejected before the group is created.
func TestCreateReplicationGroupTyped_invalidTagRejected(t *testing.T) {
	cfg := &config.Config{Region: "us-east-1", AccountID: "000000000000"}
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clock.New())
	ctx := context.Background()

	_, aerr := s.handler.createReplicationGroupTyped(ctx, &ecCreateReplicationGroupReq{
		ReplicationGroupId: "typed-rejected-rg",
		Tags:               []ecTag{{Key: "aws:reserved", Value: "x"}},
	})
	if aerr == nil {
		t.Fatal("expected tag validation to reject the create")
	}
	if _, notFoundErr := s.handler.store.getReplicationGroup(ctx, "typed-rejected-rg"); notFoundErr == nil {
		t.Fatal("rejected create must leave no replication group behind")
	}
}

// Deleting a resource that was never tagged must not be a special case its
// author has to remember.
func TestDelete_untaggedResourceIsNotAnError(t *testing.T) {
	s := newTagLifetimeStore()
	if aerr := s.putCacheCluster(context.Background(), &CacheCluster{
		CacheClusterId: "bare",
		ARN:            "arn:aws:elasticache:us-east-1:000000000000:cluster:bare",
	}); aerr != nil {
		t.Fatalf("create: %v", aerr)
	}
	if aerr := s.deleteCacheCluster(context.Background(), "bare"); aerr != nil {
		t.Fatalf("delete: %v", aerr)
	}
}
