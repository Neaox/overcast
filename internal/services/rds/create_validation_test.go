package rds

// create_validation_test.go — CreateDBCluster and CreateDBInstance enforcing the
// constraints AWS documents for them.
//
// Every gap these cover was permissive: Overcast accepted a request AWS rejects.
// That is the direction that costs a user a production incident rather than a
// local one — a stack that provisions cleanly against the emulator and then
// fails in the account, having been told yes by the thing they tested against.
//
// Sources, quoted in each case below:
//   https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBCluster.html
//   https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_CreateDBInstance.html

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

// validCluster is a CreateDBCluster request that passes every check, so a test
// can change one field and know the failure it gets is that field's.
func validCluster(id string) *createDBClusterReq {
	return &createDBClusterReq{
		DBClusterIdentifier: id,
		Engine:              "aurora-mysql",
		MasterUsername:      "admin",
		MasterUserPassword:  "Password1!",
	}
}

// validInstance is validCluster for CreateDBInstance.
func validInstance(id string) *createDBInstanceReq {
	return &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "Password1!",
		DBInstanceClass:      "db.t3.micro",
		AllocatedStorage:     20,
	}
}

// AWS: "Must contain from 1 to 63 letters, numbers, or hyphens. First character
// must be a letter. Can't end with a hyphen or contain two consecutive
// hyphens." Identical wording on both operations, so both are held to it.
func TestCreateDB_identifierShape(t *testing.T) {
	cases := []struct {
		name, id string
		wantErr  bool
	}{
		{name: "simple", id: "my-cluster1"},
		{name: "single letter", id: "a"},
		{name: "63 chars", id: "a" + strings.Repeat("b", 62)},
		{name: "64 chars", id: "a" + strings.Repeat("b", 63), wantErr: true},
		{name: "leading digit", id: "1cluster", wantErr: true},
		{name: "leading hyphen", id: "-cluster", wantErr: true},
		{name: "trailing hyphen", id: "cluster-", wantErr: true},
		{name: "consecutive hyphens", id: "my--cluster", wantErr: true},
		{name: "underscore", id: "my_cluster", wantErr: true},
		{name: "dot", id: "my.cluster", wantErr: true},
		{name: "space", id: "my cluster", wantErr: true},
	}

	for _, tc := range cases {
		t.Run("cluster/"+tc.name, func(t *testing.T) {
			h := newClusterTestHandler(t)
			_, aerr := h.createDBClusterTyped(context.Background(), validCluster(tc.id))
			if (aerr != nil) != tc.wantErr {
				t.Fatalf("CreateDBCluster(%q) error = %v, wantErr %v", tc.id, aerr, tc.wantErr)
			}
			if tc.wantErr && aerr.Code != "InvalidParameterValue" {
				t.Errorf("CreateDBCluster(%q) code = %q, want InvalidParameterValue", tc.id, aerr.Code)
			}
		})

		t.Run("instance/"+tc.name, func(t *testing.T) {
			h := newClusterTestHandler(t)
			_, aerr := h.createDBInstanceTyped(context.Background(), validInstance(tc.id))
			if (aerr != nil) != tc.wantErr {
				t.Fatalf("CreateDBInstance(%q) error = %v, wantErr %v", tc.id, aerr, tc.wantErr)
			}
			if tc.wantErr && aerr.Code != "InvalidParameterValue" {
				t.Errorf("CreateDBInstance(%q) code = %q, want InvalidParameterValue", tc.id, aerr.Code)
			}
		})
	}
}

// AWS: "This parameter is stored as a lowercase string." An identifier that
// comes back in the case it was sent is a physical ID that will not match the
// one the same template produces on AWS.
func TestCreateDB_identifierIsStoredLowercase(t *testing.T) {
	ctx := context.Background()

	t.Run("cluster", func(t *testing.T) {
		h := newClusterTestHandler(t)
		resp, aerr := h.createDBClusterTyped(ctx, validCluster("MyCluster"))
		if aerr != nil {
			t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
		}
		if got := resp.Result.DBCluster.DBClusterIdentifier; got != "mycluster" {
			t.Errorf("DBClusterIdentifier = %q, want %q", got, "mycluster")
		}
		// The lowercased name is the one the record is filed under, so a
		// describe for it has to find the cluster.
		if _, aerr := h.describeDBClustersTyped(ctx, &describeDBClustersReq{DBClusterIdentifier: "mycluster"}); aerr != nil {
			t.Errorf("DescribeDBClusters(mycluster) after create: %s: %s", aerr.Code, aerr.Message)
		}
	})

	t.Run("instance", func(t *testing.T) {
		h := newClusterTestHandler(t)
		resp, aerr := h.createDBInstanceTyped(ctx, validInstance("MyInstance"))
		if aerr != nil {
			t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
		}
		if got := resp.Result.DBInstance.DBInstanceIdentifier; got != "myinstance" {
			t.Errorf("DBInstanceIdentifier = %q, want %q", got, "myinstance")
		}
	})
}

// AWS, CreateDBCluster: "Default: 1", "Must be a value from 1 to 35."
//
// The default matters on its own: a cluster created without the parameter
// reported a retention of 0, which on AWS means something CreateDBCluster
// cannot even be asked for.
func TestCreateDBCluster_backupRetentionPeriod(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults to 1", func(t *testing.T) {
		h := newClusterTestHandler(t)
		resp, aerr := h.createDBClusterTyped(ctx, validCluster("cl"))
		if aerr != nil {
			t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
		}
		if got := resp.Result.DBCluster.BackupRetentionPeriod; got != 1 {
			t.Errorf("BackupRetentionPeriod = %d, want 1", got)
		}
	})

	for _, tc := range []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "minimum", value: 1},
		{name: "maximum", value: 35},
		{name: "zero is not a cluster value", value: 0, wantErr: true},
		{name: "negative", value: -1, wantErr: true},
		{name: "above maximum", value: 36, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newClusterTestHandler(t)
			req := validCluster("cl")
			req.BackupRetentionPeriod = intPtr(tc.value)
			resp, aerr := h.createDBClusterTyped(ctx, req)
			if (aerr != nil) != tc.wantErr {
				t.Fatalf("BackupRetentionPeriod=%d error = %v, wantErr %v", tc.value, aerr, tc.wantErr)
			}
			if tc.wantErr {
				if aerr.Code != "InvalidParameterValue" {
					t.Errorf("code = %q, want InvalidParameterValue", aerr.Code)
				}
				return
			}
			if got := resp.Result.DBCluster.BackupRetentionPeriod; got != tc.value {
				t.Errorf("BackupRetentionPeriod = %d, want %d", got, tc.value)
			}
		})
	}
}

// AWS, both operations: "Valid Values: 1150-65535".
func TestCreateDB_portRange(t *testing.T) {
	ctx := context.Background()

	for _, tc := range []struct {
		name    string
		port    int
		wantErr bool
	}{
		{name: "minimum", port: 1150},
		{name: "maximum", port: 65535},
		{name: "below minimum", port: 1149, wantErr: true},
		{name: "above maximum", port: 65536, wantErr: true},
		{name: "negative", port: -1, wantErr: true},
	} {
		t.Run("cluster/"+tc.name, func(t *testing.T) {
			h := newClusterTestHandler(t)
			req := validCluster("cl")
			req.Port = tc.port
			resp, aerr := h.createDBClusterTyped(ctx, req)
			if (aerr != nil) != tc.wantErr {
				t.Fatalf("Port=%d error = %v, wantErr %v", tc.port, aerr, tc.wantErr)
			}
			if !tc.wantErr && resp.Result.DBCluster.Port != tc.port {
				t.Errorf("Port = %d, want %d", resp.Result.DBCluster.Port, tc.port)
			}
		})
	}

	// Omitted still falls back to the engine's documented default.
	t.Run("cluster/omitted uses the engine default", func(t *testing.T) {
		h := newClusterTestHandler(t)
		resp, aerr := h.createDBClusterTyped(ctx, validCluster("cl"))
		if aerr != nil {
			t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
		}
		if got := resp.Result.DBCluster.Port; got != defaultPorts["aurora-mysql"] {
			t.Errorf("Port = %d, want %d", got, defaultPorts["aurora-mysql"])
		}
	})
}

// AWS, both operations: "Must match the name of an existing DB subnet group",
// and DBSubnetGroupNotFoundFault is a documented 404 on each.
//
// This is the permissive gap with the longest reach: a template naming a subnet
// group that does not exist provisioned cleanly here and fails in the account.
func TestCreateDB_subnetGroupMustExist(t *testing.T) {
	ctx := context.Background()

	seedSubnetGroup := func(t *testing.T, h *Handler, name string) {
		t.Helper()
		if _, aerr := h.createDBSubnetGroupTyped(ctx, &createDBSubnetGroupReq{
			DBSubnetGroupName:        name,
			DBSubnetGroupDescription: "seeded by test",
			SubnetIds:                []string{"subnet-00000000", "subnet-00000001"},
		}); aerr != nil {
			t.Fatalf("seed subnet group %q: %s: %s", name, aerr.Code, aerr.Message)
		}
	}

	t.Run("cluster/unknown group is a 404", func(t *testing.T) {
		h := newClusterTestHandler(t)
		req := validCluster("cl")
		req.DBSubnetGroupName = "no-such-group"
		_, aerr := h.createDBClusterTyped(ctx, req)
		if aerr == nil {
			t.Fatal("CreateDBCluster accepted a DBSubnetGroupName that does not exist")
		}
		if aerr.Code != "DBSubnetGroupNotFoundFault" {
			t.Errorf("code = %q, want DBSubnetGroupNotFoundFault", aerr.Code)
		}
		if aerr.HTTPStatus != http.StatusNotFound {
			t.Errorf("HTTP status = %d, want %d", aerr.HTTPStatus, http.StatusNotFound)
		}
	})

	t.Run("cluster/existing group is accepted", func(t *testing.T) {
		h := newClusterTestHandler(t)
		seedSubnetGroup(t, h, "real-group")
		req := validCluster("cl")
		req.DBSubnetGroupName = "real-group"
		resp, aerr := h.createDBClusterTyped(ctx, req)
		if aerr != nil {
			t.Fatalf("CreateDBCluster with an existing subnet group: %s: %s", aerr.Code, aerr.Message)
		}
		if got := resp.Result.DBCluster.DBSubnetGroup; got != "real-group" {
			t.Errorf("DBSubnetGroup = %q, want real-group", got)
		}
	})

	t.Run("instance/unknown group is a 404", func(t *testing.T) {
		h := newClusterTestHandler(t)
		req := validInstance("db")
		req.DBSubnetGroupName = "no-such-group"
		_, aerr := h.createDBInstanceTyped(ctx, req)
		if aerr == nil {
			t.Fatal("CreateDBInstance accepted a DBSubnetGroupName that does not exist")
		}
		if aerr.Code != "DBSubnetGroupNotFoundFault" {
			t.Errorf("code = %q, want DBSubnetGroupNotFoundFault", aerr.Code)
		}
		if aerr.HTTPStatus != http.StatusNotFound {
			t.Errorf("HTTP status = %d, want %d", aerr.HTTPStatus, http.StatusNotFound)
		}
	})

	t.Run("instance/existing group is accepted", func(t *testing.T) {
		h := newClusterTestHandler(t)
		seedSubnetGroup(t, h, "real-group")
		req := validInstance("db")
		req.DBSubnetGroupName = "real-group"
		if _, aerr := h.createDBInstanceTyped(ctx, req); aerr != nil {
			t.Fatalf("CreateDBInstance with an existing subnet group: %s: %s", aerr.Code, aerr.Message)
		}
	})

	// An Aurora member takes its subnet group from the cluster, which was
	// validated when the cluster was created. The member must not be re-checked
	// against a name it did not send.
	t.Run("instance/aurora member inherits the cluster group", func(t *testing.T) {
		h := newClusterTestHandler(t)
		seedSubnetGroup(t, h, "real-group")
		clusterReq := validCluster("cl")
		clusterReq.DBSubnetGroupName = "real-group"
		if _, aerr := h.createDBClusterTyped(ctx, clusterReq); aerr != nil {
			t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
		}

		member := validInstance("member")
		member.Engine = "aurora-mysql"
		member.DBClusterIdentifier = "cl"
		member.MasterUsername = ""
		member.MasterUserPassword = ""
		if _, aerr := h.createDBInstanceTyped(ctx, member); aerr != nil {
			t.Fatalf("CreateDBInstance for an Aurora member: %s: %s", aerr.Code, aerr.Message)
		}
	})
}

// A mixed-case identifier must work through the whole lifecycle, not only at
// create. AWS lowercases the identifier and then treats every later reference
// to it case-insensitively — ModifyDBCluster says so outright: "This parameter
// isn't case-sensitive." Normalising the create alone would leave a resource
// that exists under a name none of its own operations accept.
func TestClusterLifecycle_isCaseInsensitive(t *testing.T) {
	h := newClusterTestHandler(t)
	ctx := context.Background()

	if _, aerr := h.createDBClusterTyped(ctx, validCluster("MixedCaseCluster")); aerr != nil {
		t.Fatalf("CreateDBCluster: %s: %s", aerr.Code, aerr.Message)
	}

	// Every later operation, in a case the create never saw.
	const shouting = "MIXEDCASECLUSTER"

	if _, aerr := h.describeDBClustersTyped(ctx, &describeDBClustersReq{DBClusterIdentifier: shouting}); aerr != nil {
		t.Errorf("DescribeDBClusters(%s): %s: %s", shouting, aerr.Code, aerr.Message)
	}
	if _, aerr := h.modifyDBClusterTyped(ctx, &modifyDBClusterReq{
		DBClusterIdentifier:   shouting,
		BackupRetentionPeriod: intPtr(7),
	}); aerr != nil {
		t.Errorf("ModifyDBCluster(%s): %s: %s", shouting, aerr.Code, aerr.Message)
	}

	makeClusterAvailable(t, h, "mixedcasecluster")
	if _, aerr := h.stopDBClusterTyped(ctx, &stopDBClusterReq{DBClusterIdentifier: shouting}); aerr != nil {
		t.Errorf("StopDBCluster(%s): %s: %s", shouting, aerr.Code, aerr.Message)
	}

	// The modify landed on the one record, under its normalized name.
	got, aerr := h.store.getDBCluster(ctx, "mixedcasecluster")
	if aerr != nil {
		t.Fatalf("getDBCluster: %s: %s", aerr.Code, aerr.Message)
	}
	if got.BackupRetentionPeriod != 7 {
		t.Errorf("BackupRetentionPeriod = %d, want 7 — the modify addressed a different record",
			got.BackupRetentionPeriod)
	}

	if _, aerr := h.deleteDBClusterTyped(ctx, &deleteDBClusterReq{DBClusterIdentifier: shouting}); aerr != nil {
		t.Errorf("DeleteDBCluster(%s): %s: %s", shouting, aerr.Code, aerr.Message)
	}
}
