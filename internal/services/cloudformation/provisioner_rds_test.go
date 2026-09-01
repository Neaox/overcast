package cloudformation

// provisioner_rds_test.go — when an AWS::RDS::DBInstance resource is done.
//
// The handler fired CreateDBInstance and returned the moment the API answered.
// That is what the RDS API itself does — CreateDBInstance is asynchronous and
// answers with the instance in "creating" — but it is not what CloudFormation
// does with the result: the DBInstance resource is not CREATE_COMPLETE until
// the instance reaches "available", and every resource downstream of it waits
// behind that.
//
// Without the wait, `cdk deploy` returned green while the engine was still
// initialising its data directory, so a migration or an app started off the
// back of a successful deploy was refused a connection. The gap is the one
// already recorded against a real MySQL container in
// internal/services/rds/typed_logic.go: available 0.9s after CreateDBInstance,
// not accepting a connection for another 27s. Worse in the other direction: an
// instance whose container never came up settled into "failed" behind a stack
// that had already reported CREATE_COMPLETE, where AWS fails the resource and
// rolls the stack back.
//
// The wait is tested against a scripted RDS endpoint rather than the real
// service, because the thing under test is the polling — that the resource
// keeps asking, believes only "available", and reports the database's own
// reason when it fails. Against the real service a metadata-only instance is
// available on the first check, which is exactly the case that cannot tell a
// wait apart from no wait at all.
//
// The contract the wait hangs off — a failed stabilization keeping its physical
// ID so rollback can delete it — is in provisioner_stabilize_test.go.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/serviceutil"
	"github.com/overcast-sh/overcast/internal/state"
)

// fakeRDS is an RDS Query endpoint that walks one instance or cluster through a
// scripted sequence of statuses — one per Describe call, with the last repeating
// once the script runs out. It is the shape the stabilization wait sees: a
// database that is not ready when it is created and becomes ready later.
type fakeRDS struct {
	mu sync.Mutex

	statuses  []string // status per Describe call; the last one repeats
	describes int      // Describe calls served
	gone      bool     // answer Describe with an empty result, as a deleted instance does
	// createForm is the form of the last CreateDBInstance, for asserting which
	// template properties actually reached the API.
	createForm url.Values
	events     []string // DescribeEvents messages, oldest first
}

// nextStatus returns the status this Describe call should report and records
// that the call happened.
func (f *fakeRDS) nextStatus() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.describes
	f.describes++
	if len(f.statuses) == 0 {
		return "available"
	}
	if i >= len(f.statuses) {
		i = len(f.statuses) - 1
	}
	return f.statuses[i]
}

func (f *fakeRDS) describeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.describes
}

func (f *fakeRDS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "text/xml")

	switch action := r.Form.Get("Action"); action {
	case "CreateDBInstance", "ModifyDBInstance":
		id := r.Form.Get("DBInstanceIdentifier")
		if action == "CreateDBInstance" {
			f.mu.Lock()
			f.createForm = r.Form
			f.mu.Unlock()
		}
		fmt.Fprintf(w, `<%sResponse><%sResult><DBInstance>`+
			`<DBInstanceIdentifier>%s</DBInstanceIdentifier>`+
			`<DBInstanceStatus>creating</DBInstanceStatus>`+
			`<DBInstanceArn>arn:aws:rds:us-east-1:000000000000:db:%s</DBInstanceArn>`+
			`<Endpoint><Address>%s.rds.overcast.test</Address><Port>3306</Port></Endpoint>`+
			`</DBInstance></%sResult></%sResponse>`, action, action, id, id, id, action, action)

	case "DescribeDBInstances":
		if f.gone {
			fmt.Fprint(w, `<DescribeDBInstancesResponse><DescribeDBInstancesResult>`+
				`<DBInstances></DBInstances>`+
				`</DescribeDBInstancesResult></DescribeDBInstancesResponse>`)
			return
		}
		fmt.Fprintf(w, `<DescribeDBInstancesResponse><DescribeDBInstancesResult><DBInstances><DBInstance>`+
			`<DBInstanceIdentifier>%s</DBInstanceIdentifier>`+
			`<DBInstanceStatus>%s</DBInstanceStatus>`+
			`</DBInstance></DBInstances></DescribeDBInstancesResult></DescribeDBInstancesResponse>`,
			r.Form.Get("DBInstanceIdentifier"), f.nextStatus())

	case "CreateDBCluster", "ModifyDBCluster":
		id := r.Form.Get("DBClusterIdentifier")
		fmt.Fprintf(w, `<%sResponse><%sResult><DBCluster>`+
			`<DBClusterIdentifier>%s</DBClusterIdentifier>`+
			`<Status>creating</Status>`+
			`<DBClusterArn>arn:aws:rds:us-east-1:000000000000:cluster:%s</DBClusterArn>`+
			`<Endpoint>%s.cluster.rds.overcast.test</Endpoint><Port>3306</Port>`+
			`</DBCluster></%sResult></%sResponse>`, action, action, id, id, id, action, action)

	case "DescribeDBClusters":
		fmt.Fprintf(w, `<DescribeDBClustersResponse><DescribeDBClustersResult><DBClusters><DBCluster>`+
			`<DBClusterIdentifier>%s</DBClusterIdentifier>`+
			`<Status>%s</Status>`+
			`</DBCluster></DBClusters></DescribeDBClustersResult></DescribeDBClustersResponse>`,
			r.Form.Get("DBClusterIdentifier"), f.nextStatus())

	case "DescribeEvents":
		var b strings.Builder
		b.WriteString(`<DescribeEventsResponse><DescribeEventsResult><Events>`)
		for _, m := range f.events {
			fmt.Fprintf(&b, `<Event><SourceIdentifier>%s</SourceIdentifier>`+
				`<SourceType>db-instance</SourceType><Message>%s</Message></Event>`,
				r.Form.Get("SourceIdentifier"), m)
		}
		b.WriteString(`</Events></DescribeEventsResult></DescribeEventsResponse>`)
		fmt.Fprint(w, b.String())

	default:
		http.Error(w, "unexpected action "+action, http.StatusBadRequest)
	}
}

// newTestProvisioner wires a provisioner to a scripted AWS endpoint. The RDS
// tests go through the provisioner rather than calling a handler directly
// because the wait is the provisioner's to run — a handler that implements
// Stabilize but is never asked to would pass a handler-level test and still
// complete the resource early.
//
// Nothing here is RDS-specific: the router is whatever the caller scripts, so
// any resource handler's Create can be driven through it.
//
// The optional clock is what makes a timeout testable. A stabilization budget
// is minutes long, so a test that means to run one out hands in a clock that
// moves when the wait waits rather than when the wall does — see
// pollDrivenClock. Omit it and the provisioner runs on the real clock.
func newTestProvisioner(t *testing.T, router http.Handler, clocks ...clock.Clock) (*provisioner, *resolveContext) {
	t.Helper()
	cfg := &config.Config{
		Region:    "us-east-1",
		AccountID: "000000000000",
	}
	var clk clock.Clock = clock.New()
	if len(clocks) > 0 {
		clk = clocks[0]
	}
	st := newCFNStore(state.NewMemoryStore(), cfg.Region, clk)
	p := newProvisioner(cfg, st, clk, serviceutil.NewServiceLogger(zap.NewNop(), "cloudformation"))
	p.initRouter(router)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		p.stop(ctx)
	})
	return p, &resolveContext{
		Region:    cfg.Region,
		AccountID: cfg.AccountID,
		StackName: "rds-stack",
		Resources: map[string]string{},
	}
}

func rdsInstanceProps(id string) map[string]any {
	return map[string]any{
		"DBInstanceIdentifier": id,
		"Engine":               "mysql",
		"DBInstanceClass":      "db.t3.micro",
		"MasterUsername":       "admin",
		"MasterUserPassword":   "correct-horse-battery",
	}
}

// A created instance is not done until the database behind it answers. The
// resource must keep asking rather than take CreateDBInstance's own "creating"
// as the end of it.
func TestRDSDBInstanceCreate_waitsForTheInstanceToBecomeAvailable(t *testing.T) {
	// Given: a database that is still creating for its first two status checks
	f := &fakeRDS{statuses: []string{"creating", "creating", "available"}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Database",
		TemplateResource{Type: "AWS::RDS::DBInstance"}, rdsInstanceProps("appdb"), rCtx)

	// Then: it completed only once the instance was available
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "appdb" {
		t.Errorf("physical ID = %q, want %q", id, "appdb")
	}
	if got := f.describeCount(); got != 3 {
		t.Errorf("DescribeDBInstances calls = %d, want 3 — the resource completed before the "+
			"database was available", got)
	}
	// The endpoint attributes CreateDBInstance answered with must survive the
	// wait; a GetAtt on Endpoint.Address is the most common reason a resource
	// depends on this one.
	if addr := rCtx.Attributes["Database"]["Endpoint.Address"]; addr == "" {
		t.Errorf("Endpoint.Address attribute is empty; got %v", rCtx.Attributes["Database"])
	}
}

// An instance that will never come up must fail the resource — and say why.
// Reporting CREATE_COMPLETE over a failed database is the failure the RDS
// service's own "failed" status exists to prevent.
func TestRDSDBInstanceCreate_failsWithTheDatabasesOwnReason(t *testing.T) {
	// Given: a database whose container dies while it is being created
	f := &fakeRDS{
		statuses: []string{"creating", "failed"},
		events: []string{
			"DB instance created.",
			"The DB instance creation failed. the database container exited with code 1 — see the instance logs.",
		},
	}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Database",
		TemplateResource{Type: "AWS::RDS::DBInstance"}, rdsInstanceProps("brokendb"), rCtx)

	// Then: the resource fails, carrying the reason RDS recorded against it
	if err == nil {
		t.Fatal("expected the resource to fail for an instance that went to \"failed\"")
	}
	if !strings.Contains(err.Error(), "exited with code 1") {
		t.Errorf("expected the RDS event's own reason to reach the stack, got %v", err)
	}
	// The instance was created before it failed, so its name has to come back
	// with the error — it is what rollback deletes it by.
	if id != "brokendb" {
		t.Errorf("physical ID = %q, want %q — a database that failed to come up still exists, "+
			"and losing its name strands it", id, "brokendb")
	}
}

// An instance that disappears mid-wait is not going to become available, and
// waiting out the full budget for one only delays saying so.
func TestRDSDBInstanceCreate_failsWhenTheInstanceDisappears(t *testing.T) {
	// Given: an instance that is gone by the time the wait looks for it
	f := &fakeRDS{gone: true}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	_, err := p.provisionResource(context.Background(), "Database",
		TemplateResource{Type: "AWS::RDS::DBInstance"}, rdsInstanceProps("vanishingdb"), rCtx)

	// Then: the resource fails immediately rather than polling a missing instance
	if err == nil {
		t.Fatal("expected the resource to fail for an instance that no longer exists")
	}
	if !strings.Contains(err.Error(), "vanishingdb") {
		t.Errorf("expected the failure to name the instance, got %v", err)
	}
}

// The same wait applies to an update: ModifyDBInstance puts the instance into
// "modifying", and the resource is not UPDATE_COMPLETE until it settles back.
func TestRDSDBInstanceUpdate_waitsForTheModificationToSettle(t *testing.T) {
	// Given: an instance that spends a status check in "modifying"
	f := &fakeRDS{statuses: []string{"modifying", "available"}}
	p, rCtx := newTestProvisioner(t, f)

	props := rdsInstanceProps("appdb")
	props["MasterUserPassword"] = "rotated-password"
	old := &StackResource{PhysicalID: "appdb", Properties: rdsInstanceProps("appdb")}

	// When: CloudFormation updates it in place
	outcome, err := p.updateResource(context.Background(), "Database",
		TemplateResource{Type: "AWS::RDS::DBInstance"}, props, "appdb", old, rCtx)

	// Then: it completed only once the instance had settled
	if err != nil {
		t.Fatalf("updateResource: %v", err)
	}
	if outcome.PhysicalID != "appdb" {
		t.Errorf("physical ID = %q, want %q", outcome.PhysicalID, "appdb")
	}
	if outcome.ReplacedPhysicalID != "" {
		t.Errorf("in-place update reported a replacement of %q", outcome.ReplacedPhysicalID)
	}
	if got := f.describeCount(); got != 2 {
		t.Errorf("DescribeDBInstances calls = %d, want 2 — the update completed while the "+
			"instance was still modifying", got)
	}
}

// An update that applies and then fails to settle is terminal for the resource.
// Answering it with a replacement would build a second database rather than fix
// the one that did not settle — and for an instance whose identifier the
// template pins, the replacement cannot succeed at all.
func TestRDSDBInstanceUpdate_failureToSettleIsNotAnsweredByReplacement(t *testing.T) {
	// Given: an instance that goes to "failed" after being modified
	f := &fakeRDS{statuses: []string{"failed"}}
	p, rCtx := newTestProvisioner(t, f)

	props := rdsInstanceProps("appdb")
	props["DBInstanceClass"] = "db.t3.small"
	old := &StackResource{PhysicalID: "appdb", Properties: rdsInstanceProps("appdb")}

	// When: CloudFormation updates it in place
	outcome, err := p.updateResource(context.Background(), "Database",
		TemplateResource{Type: "AWS::RDS::DBInstance"}, props, "appdb", old, rCtx)

	// Then: the update fails outright, with no second instance created
	if err == nil {
		t.Fatalf("expected the update to fail; outcome %+v", outcome)
	}
	var failed updateFailure
	if !errors.As(err, &failed) {
		t.Errorf("expected a terminal updateFailure so the provisioner does not replace the "+
			"instance, got %T: %v", err, err)
	}
	if outcome.PhysicalID != "appdb" || !outcome.UpdatedInPlace || outcome.Replaced() {
		t.Errorf("failed update outcome = %+v, want applied in-place mutation", outcome)
	}
}

// PubliclyAccessible is the template's own opt-out from a subnet group making
// the instance private. Not forwarding it left a template that asked for a
// reachable database with a private one and no way to say otherwise.
func TestRDSDBInstanceCreate_forwardsPubliclyAccessible(t *testing.T) {
	// Given: a template that puts the instance in a subnet group but asks for it
	// to stay publicly accessible
	f := &fakeRDS{}
	p, rCtx := newTestProvisioner(t, f)
	props := rdsInstanceProps("appdb")
	props["DBSubnetGroupName"] = "app-subnets"
	props["PubliclyAccessible"] = true

	// When: CloudFormation provisions it
	if _, err := p.provisionResource(context.Background(), "Database",
		TemplateResource{Type: "AWS::RDS::DBInstance"}, props, rCtx); err != nil {
		t.Fatalf("provisionResource: %v", err)
	}

	// Then: RDS was told both
	f.mu.Lock()
	form := f.createForm
	f.mu.Unlock()
	if got := form.Get("PubliclyAccessible"); got != "true" {
		t.Errorf("PubliclyAccessible = %q, want %q — the template's opt-out never reached RDS", got, "true")
	}
	if got := form.Get("DBSubnetGroupName"); got != "app-subnets" {
		t.Errorf("DBSubnetGroupName = %q, want %q", got, "app-subnets")
	}
}

// A cluster stabilizes on the same rule, reading DBCluster's "Status" rather
// than DBInstance's "DBInstanceStatus".
func TestRDSDBClusterCreate_waitsForTheClusterToBecomeAvailable(t *testing.T) {
	// Given: a cluster that is still creating for its first status check
	f := &fakeRDS{statuses: []string{"creating", "available"}}
	p, rCtx := newTestProvisioner(t, f)

	// When: CloudFormation provisions it
	id, err := p.provisionResource(context.Background(), "Cluster",
		TemplateResource{Type: "AWS::RDS::DBCluster"}, map[string]any{
			"DBClusterIdentifier": "appcluster",
			"Engine":              "aurora-mysql",
			"MasterUsername":      "admin",
			"MasterUserPassword":  "correct-horse-battery",
		}, rCtx)

	// Then: it completed only once the cluster was available
	if err != nil {
		t.Fatalf("provisionResource: %v", err)
	}
	if id != "appcluster" {
		t.Errorf("physical ID = %q, want %q", id, "appcluster")
	}
	if got := f.describeCount(); got != 2 {
		t.Errorf("DescribeDBClusters calls = %d, want 2 — the resource completed before the "+
			"cluster was available", got)
	}
	if addr := rCtx.Attributes["Cluster"]["Endpoint.Address"]; addr == "" {
		t.Errorf("Endpoint.Address attribute is empty; got %v", rCtx.Attributes["Cluster"])
	}
}

// The status vocabulary the wait is written against. Every value here is one
// AWS documents for a DB instance or cluster; the emulator only produces a
// handful of them, but a status it does not understand must leave the resource
// waiting rather than guess.
func TestRDSStatusOutcome_awsStatusVocabulary(t *testing.T) {
	cases := []struct {
		name   string
		status string
		want   rdsStatusOutcome
	}{
		{name: "available", status: "available", want: rdsStatusAvailable},
		{name: "creating", status: "creating", want: rdsStatusWaiting},
		{name: "starting", status: "starting", want: rdsStatusWaiting},
		{name: "modifying", status: "modifying", want: rdsStatusWaiting},
		{name: "backing-up", status: "backing-up", want: rdsStatusWaiting},
		{name: "upgrading", status: "upgrading", want: rdsStatusWaiting},
		{name: "no status at all", status: "", want: rdsStatusWaiting},
		{name: "a status AWS added since", status: "storage-config-upgrade", want: rdsStatusWaiting},
		{name: "recoverable encryption credentials", status: "inaccessible-encryption-credentials-recoverable", want: rdsStatusWaiting},
		{name: "cloning failed", status: "cloning-failed", want: rdsStatusFailed},
		{name: "failed", status: "failed", want: rdsStatusFailed},
		{name: "inaccessible encryption credentials", status: "inaccessible-encryption-credentials", want: rdsStatusFailed},
		{name: "incompatible create", status: "incompatible-create", want: rdsStatusFailed},
		{name: "incompatible-parameters", status: "incompatible-parameters", want: rdsStatusFailed},
		{name: "incompatible-restore", status: "incompatible-restore", want: rdsStatusFailed},
		{name: "incompatible-network", status: "incompatible-network", want: rdsStatusFailed},
		{name: "incompatible option group", status: "incompatible-option-group", want: rdsStatusFailed},
		{name: "insufficient capacity", status: "insufficient-capacity", want: rdsStatusFailed},
		{name: "migration failed", status: "migration-failed", want: rdsStatusFailed},
		{name: "restore-error", status: "restore-error", want: rdsStatusFailed},
		{name: "upgrade failed", status: "upgrade-failed", want: rdsStatusFailed},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := rdsOutcome(tc.status); got != tc.want {
				t.Errorf("rdsOutcome(%q) = %v, want %v", tc.status, got, tc.want)
			}
		})
	}
}
