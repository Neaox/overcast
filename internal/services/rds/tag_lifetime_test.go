package rds

// Tags are keyed by resource ARN in a namespace of their own, so nothing ties
// them to the lifetime of the record they describe. Deleting an instance left
// them behind, so ListTagsForResource kept answering for a resource that was
// gone and the store grew for the life of the session.
//
// The check is at the store, because that is where every delete path — the
// Query handlers and the typed operations alike — funnels through, so a new
// resource type gets the cleanup by writing one line rather than remembering to.

import (
	"context"
	"testing"

	"github.com/overcast-sh/overcast/internal/protocol"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// storedTagCount reports how many tag blobs the whole namespace holds, which is
// what says whether a delete cleaned up or merely stopped pointing at it.
func storedTagCount(t *testing.T, s *rdsStore) int {
	t.Helper()
	kvs, err := s.store.Scan(context.Background(), nsTags, "")
	if err != nil {
		t.Fatalf("scan tags: %v", err)
	}
	return len(kvs)
}

// Every taggable RDS resource, so a delete path that forgets the cleanup is a
// named failure rather than a gap nobody notices.
func TestDelete_takesTagsWithIt(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name   string
		arn    string
		create func(s *rdsStore) *protocol.AWSError
		remove func(s *rdsStore) *protocol.AWSError
	}{
		{
			name: "db instance",
			arn:  "arn:aws:rds:us-east-1:000000000000:db:db1",
			create: func(s *rdsStore) *protocol.AWSError {
				return s.putDBInstance(ctx, &DBInstance{
					DBInstanceIdentifier: "db1",
					DBInstanceArn:        "arn:aws:rds:us-east-1:000000000000:db:db1",
				})
			},
			remove: func(s *rdsStore) *protocol.AWSError { return s.deleteDBInstance(ctx, "db1") },
		},
		{
			name: "db cluster",
			arn:  "arn:aws:rds:us-east-1:000000000000:cluster:cl1",
			create: func(s *rdsStore) *protocol.AWSError {
				return s.putDBCluster(ctx, &DBCluster{
					DBClusterIdentifier: "cl1",
					DBClusterArn:        "arn:aws:rds:us-east-1:000000000000:cluster:cl1",
				})
			},
			remove: func(s *rdsStore) *protocol.AWSError { return s.deleteDBCluster(ctx, "cl1") },
		},
		{
			name: "db subnet group",
			arn:  "arn:aws:rds:us-east-1:000000000000:subgrp:sg1",
			create: func(s *rdsStore) *protocol.AWSError {
				return s.putDBSubnetGroup(ctx, &DBSubnetGroup{
					DBSubnetGroupName: "sg1",
					DBSubnetGroupArn:  "arn:aws:rds:us-east-1:000000000000:subgrp:sg1",
				})
			},
			remove: func(s *rdsStore) *protocol.AWSError { return s.deleteDBSubnetGroup(ctx, "sg1") },
		},
		{
			name: "db parameter group",
			arn:  "arn:aws:rds:us-east-1:000000000000:pg:pg1",
			create: func(s *rdsStore) *protocol.AWSError {
				return s.putDBParameterGroup(ctx, &DBParameterGroup{
					DBParameterGroupName: "pg1",
					DBParameterGroupArn:  "arn:aws:rds:us-east-1:000000000000:pg:pg1",
				})
			},
			remove: func(s *rdsStore) *protocol.AWSError { return s.deleteDBParameterGroup(ctx, "pg1") },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Given: a tagged resource
			s := newRDSStore(state.NewMemoryStore(), "us-east-1")
			if aerr := tc.create(s); aerr != nil {
				t.Fatalf("create: %v", aerr)
			}
			if _, aerr := serviceutil.ApplyStoreTags(ctx, s.tags(), tc.arn,
				map[string]string{"env": "prod"}, rdsTagCfg); aerr != nil {
				t.Fatalf("tag: %v", aerr)
			}
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

// Deleting a resource that was never tagged must not be a special case its
// author has to remember.
func TestDelete_untaggedResourceIsNotAnError(t *testing.T) {
	ctx := context.Background()
	s := newRDSStore(state.NewMemoryStore(), "us-east-1")
	if aerr := s.putDBInstance(ctx, &DBInstance{
		DBInstanceIdentifier: "bare",
		DBInstanceArn:        "arn:aws:rds:us-east-1:000000000000:db:bare",
	}); aerr != nil {
		t.Fatalf("create: %v", aerr)
	}
	if aerr := s.deleteDBInstance(ctx, "bare"); aerr != nil {
		t.Fatalf("delete: %v", aerr)
	}
}
