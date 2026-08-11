package rds

// handler_public_access_test.go — PubliclyAccessible.
//
// The field decides whether a DB instance is reachable from outside the VPC it
// was placed in. It has to survive create → describe, be changeable on modify,
// and default the way AWS defaults it, because it is the only answer a user
// has when a database inside a VPC still has to be dialable from the host.

import (
	"context"
	"encoding/xml"
	"net/http"
	"strings"
	"testing"
)

// createInstance is the minimum valid CreateDBInstance, with whatever extra
// the caller wants layered on.
func createInstanceWith(t *testing.T, h *Handler, id string, mutate func(*createDBInstanceReq)) *DBInstance {
	t.Helper()
	req := &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
	}
	if mutate != nil {
		mutate(req)
	}
	if _, aerr := h.createDBInstanceTyped(context.Background(), req); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst, aerr := h.store.getDBInstance(context.Background(), id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	return inst
}

// An instance created without a DB subnet group lands in the default VPC,
// which has an internet gateway attached — AWS calls that public, and so does
// Overcast. This is also the instance that is reachable from the host today,
// so the default is not a behaviour change dressed up as a new field.
func TestCreateDBInstance_publiclyAccessibleDefaultsPublicWithoutSubnetGroup(t *testing.T) {
	h, _ := newDispatchHandler(t)
	inst := createInstanceWith(t, h, "pa-default-public", nil)
	if !inst.PubliclyAccessibleOrDefault() {
		t.Error("an instance created with no DB subnet group defaulted to private")
	}
}

// A named DB subnet group is AWS's "I chose where this goes" signal, and an
// instance in one is private unless asked otherwise.
func TestCreateDBInstance_publiclyAccessibleDefaultsPrivateWithSubnetGroup(t *testing.T) {
	h, _ := newDispatchHandler(t)
	ctx := context.Background()
	if _, aerr := h.createDBSubnetGroupTyped(ctx, &createDBSubnetGroupReq{
		DBSubnetGroupName:        "pa-subnets",
		DBSubnetGroupDescription: "subnets for the public-access default",
		SubnetIds:                []string{"subnet-abc123"},
	}); aerr != nil {
		t.Fatalf("CreateDBSubnetGroup: %s: %s", aerr.Code, aerr.Message)
	}

	inst := createInstanceWith(t, h, "pa-default-private", func(req *createDBInstanceReq) {
		req.DBSubnetGroupName = "pa-subnets"
	})
	if inst.PubliclyAccessibleOrDefault() {
		t.Error("an instance created in a named DB subnet group defaulted to public")
	}
}

// Whatever the default, an explicit value wins — both ways round, and in both
// the presence and the absence of a subnet group.
func TestCreateDBInstance_publiclyAccessibleExplicitValueWins(t *testing.T) {
	h, _ := newDispatchHandler(t)
	ctx := context.Background()
	if _, aerr := h.createDBSubnetGroupTyped(ctx, &createDBSubnetGroupReq{
		DBSubnetGroupName:        "pa-subnets",
		DBSubnetGroupDescription: "subnets for the public-access default",
		SubnetIds:                []string{"subnet-abc123"},
	}); aerr != nil {
		t.Fatalf("CreateDBSubnetGroup: %s: %s", aerr.Code, aerr.Message)
	}

	no := false
	inst := createInstanceWith(t, h, "pa-explicit-private", func(req *createDBInstanceReq) {
		req.PubliclyAccessible = &no
	})
	if inst.PubliclyAccessibleOrDefault() {
		t.Error("PubliclyAccessible=false was ignored on an instance with no subnet group")
	}

	yes := true
	inst = createInstanceWith(t, h, "pa-explicit-public", func(req *createDBInstanceReq) {
		req.DBSubnetGroupName = "pa-subnets"
		req.PubliclyAccessible = &yes
	})
	if !inst.PubliclyAccessibleOrDefault() {
		t.Error("PubliclyAccessible=true was ignored on an instance in a subnet group — " +
			"this is the escape hatch, and it has to work")
	}
}

// ModifyDBInstance can turn it on and off, and leaves it alone when the
// parameter is absent — the distinction MultiAZ had to learn the hard way.
func TestModifyDBInstance_publiclyAccessibleCanBeChanged(t *testing.T) {
	h, clk := newDispatchHandler(t)
	ctx := context.Background()
	const id = "pa-modify"
	createInstanceWith(t, h, id, nil)
	h.scheduler.AdvanceAndSettle(clk, 0)

	off := false
	if _, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		PubliclyAccessible:   &off,
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.PubliclyAccessibleOrDefault() {
		t.Fatal("PubliclyAccessible=false was ignored — the instance is still public")
	}

	// Absent leaves it where it is.
	if _, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		DBInstanceClass:      "db.t3.medium",
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst, aerr = h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.PubliclyAccessibleOrDefault() {
		t.Error("an absent PubliclyAccessible turned the instance public again")
	}

	// And back on.
	on := true
	if _, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		PubliclyAccessible:   &on,
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst, aerr = h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if !inst.PubliclyAccessibleOrDefault() {
		t.Error("PubliclyAccessible=true was ignored on modify")
	}
}

// A record written before the field existed carries no answer at all. It has
// to read the way a create would have answered it rather than false, because
// an instance that is dialable today must not go private just because Overcast
// was upgraded underneath it.
func TestPubliclyAccessible_recordsPredatingTheFieldKeepTheirAccess(t *testing.T) {
	h, _ := newDispatchHandler(t)
	ctx := context.Background()

	// An older record decodes with the field absent — a nil pointer.
	older := createInstanceWith(t, h, "pa-legacy", nil)
	older.PubliclyAccessible = nil
	if aerr := h.store.putDBInstance(ctx, older); aerr != nil {
		t.Fatalf("putDBInstance: %s", aerr.Message)
	}
	if !older.PubliclyAccessibleOrDefault() {
		t.Error("a pre-existing instance with no subnet group read as private")
	}

	rec := rawQuery(t, h, "DescribeDBInstances", map[string]string{"DBInstanceIdentifier": "pa-legacy"})
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeDBInstances: %d: %s", rec.Code, rec.Body.String())
	}
	if !publiclyAccessibleFromXML(t, rec.Body.String(), "DescribeDBInstancesResult") {
		t.Error("DescribeDBInstances reported a pre-existing instance as private")
	}
}

// publiclyAccessibleFromXML pulls the field out of whichever result wrapper
// the operation used, so a value that never reached the wire cannot pass.
func publiclyAccessibleFromXML(t *testing.T, body, wrapper string) bool {
	t.Helper()
	// A single-instance result carries the DBInstance directly; a describe
	// wraps it in DBInstances. Both are decoded, and exactly one lands.
	var out struct {
		Single *bool `xml:"DBInstance>PubliclyAccessible"`
		Listed *bool `xml:"DBInstances>DBInstance>PubliclyAccessible"`
	}
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("%s: no %s element in %s", wrapper, wrapper, body)
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != wrapper {
			continue
		}
		if err := dec.DecodeElement(&out, &se); err != nil {
			t.Fatalf("decode %s: %v", wrapper, err)
		}
		switch {
		case out.Single != nil:
			return *out.Single
		case out.Listed != nil:
			return *out.Listed
		default:
			t.Fatalf("%s carries no PubliclyAccessible element: %s", wrapper, body)
			return false
		}
	}
}

// The whole point is the wire: an SDK reads DBInstance.PubliclyAccessible off
// the XML, so create, describe and modify all have to carry it.
func TestPubliclyAccessible_roundTripsOnTheWire(t *testing.T) {
	h, clk := newDispatchHandler(t)
	const id = "pa-wire"

	rec := rawQuery(t, h, "CreateDBInstance", map[string]string{
		"DBInstanceIdentifier": id,
		"Engine":               "mysql",
		"MasterUsername":       "admin",
		"MasterUserPassword":   "password123",
		"PubliclyAccessible":   "false",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("CreateDBInstance: %d: %s", rec.Code, rec.Body.String())
	}
	if publiclyAccessibleFromXML(t, rec.Body.String(), "CreateDBInstanceResult") {
		t.Error("CreateDBInstance returned PubliclyAccessible=true for a request that asked for false")
	}
	h.scheduler.AdvanceAndSettle(clk, 0)

	rec = rawQuery(t, h, "DescribeDBInstances", map[string]string{"DBInstanceIdentifier": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeDBInstances: %d: %s", rec.Code, rec.Body.String())
	}
	if publiclyAccessibleFromXML(t, rec.Body.String(), "DescribeDBInstancesResult") {
		t.Error("DescribeDBInstances did not report the stored PubliclyAccessible=false")
	}

	rec = rawQuery(t, h, "ModifyDBInstance", map[string]string{
		"DBInstanceIdentifier": id,
		"PubliclyAccessible":   "true",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ModifyDBInstance: %d: %s", rec.Code, rec.Body.String())
	}
	if !publiclyAccessibleFromXML(t, rec.Body.String(), "ModifyDBInstanceResult") {
		t.Error("ModifyDBInstance did not report the instance as public after being asked to make it public")
	}

	rec = rawQuery(t, h, "DescribeDBInstances", map[string]string{"DBInstanceIdentifier": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeDBInstances: %d: %s", rec.Code, rec.Body.String())
	}
	if !publiclyAccessibleFromXML(t, rec.Body.String(), "DescribeDBInstancesResult") {
		t.Error("the modification did not survive to the next describe")
	}
}
