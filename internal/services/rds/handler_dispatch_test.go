package rds

// handler_dispatch_test.go — one implementation per operation.
//
// Service.DispatchQuery prefers a typed operation when a codec is in context
// and otherwise falls back to the raw handler map, so both paths are live.
// StopDBInstance, StartDBInstance and ModifyDBInstance were implemented twice,
// once on each path, and had already drifted: only the raw pair honoured
// `MultiAZ=false`, so whether you could turn Multi-AZ off depended on how the
// request happened to be routed.

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/overcast-sh/overcast/internal/clock"
	"github.com/overcast-sh/overcast/internal/config"
	"github.com/overcast-sh/overcast/internal/state"
)

func newDispatchHandler(t *testing.T) (*Handler, *clock.Mock) {
	t.Helper()
	cfg := &config.Config{Region: "us-east-1", AccountID: "123456789012"}
	clk := clock.NewMock()
	clk.Set(time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC))
	s := New(cfg, state.NewMemoryStore(), zap.NewNop(), clk)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.handler.scheduler.Stop(ctx)
	})
	return s.handler, clk
}

// rawQuery drives the fallback dispatch path: a Query request with no codec in
// context, exactly as Service.DispatchQuery hands it over.
func rawQuery(t *testing.T, h *Handler, action string, params map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	form := make([]string, 0, len(params)+2)
	form = append(form, "Action="+action, "Version=2014-10-31")
	for k, v := range params {
		form = append(form, k+"="+v)
	}
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Join(form, "&")))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	h.dispatch(rec, req)
	return rec
}

func instanceStatusFromXML(t *testing.T, rec *httptest.ResponseRecorder, wrapper string) string {
	t.Helper()
	var out struct {
		Status string `xml:"DBInstance>DBInstanceStatus"`
	}
	// The result element name varies per operation; strip the envelope by
	// decoding the whole document and taking the first matching path.
	dec := xml.NewDecoder(strings.NewReader(rec.Body.String()))
	for {
		tok, err := dec.Token()
		if err != nil {
			t.Fatalf("%s: no %s element in %s", wrapper, wrapper, rec.Body.String())
		}
		if se, ok := tok.(xml.StartElement); ok && se.Name.Local == wrapper {
			if err := dec.DecodeElement(&out, &se); err != nil {
				t.Fatalf("decode %s: %v", wrapper, err)
			}
			return out.Status
		}
	}
}

func createAvailableInstance(t *testing.T, h *Handler, clk *clock.Mock, id string) {
	t.Helper()
	if _, aerr := h.createDBInstanceTyped(context.Background(), &createDBInstanceReq{
		DBInstanceIdentifier: id,
		Engine:               "mysql",
		MasterUsername:       "admin",
		MasterUserPassword:   "password123",
		MultiAZ:              true,
	}); aerr != nil {
		t.Fatalf("CreateDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	h.scheduler.AdvanceAndSettle(clk, time.Second)
}

// The fallback path must still answer, and answer the same way the typed path
// does — it is the same implementation now, and this is what says so.
func TestRawQueryDispatch_stopStartAndModifyStillWork(t *testing.T) {
	h, clk := newDispatchHandler(t)
	const id = "dispatch-lifecycle"
	createAvailableInstance(t, h, clk, id)

	rec := rawQuery(t, h, "StopDBInstance", map[string]string{"DBInstanceIdentifier": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("StopDBInstance via raw dispatch: %d: %s", rec.Code, rec.Body.String())
	}
	if got := instanceStatusFromXML(t, rec, "StopDBInstanceResult"); got != "stopping" {
		t.Errorf("StopDBInstance returned status %q, want stopping", got)
	}
	h.scheduler.AdvanceAndSettle(clk, time.Second)

	rec = rawQuery(t, h, "StartDBInstance", map[string]string{"DBInstanceIdentifier": id})
	if rec.Code != http.StatusOK {
		t.Fatalf("StartDBInstance via raw dispatch: %d: %s", rec.Code, rec.Body.String())
	}
	if got := instanceStatusFromXML(t, rec, "StartDBInstanceResult"); got != "starting" {
		t.Errorf("StartDBInstance returned status %q, want starting", got)
	}
	h.scheduler.AdvanceAndSettle(clk, time.Second)

	rec = rawQuery(t, h, "ModifyDBInstance", map[string]string{
		"DBInstanceIdentifier": id,
		"DBInstanceClass":      "db.t3.small",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ModifyDBInstance via raw dispatch: %d: %s", rec.Code, rec.Body.String())
	}
	inst, aerr := h.store.getDBInstance(context.Background(), id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.DBInstanceClass != "db.t3.small" {
		t.Errorf("DBInstanceClass = %q, want the modification applied", inst.DBInstanceClass)
	}

	// A validation failure still has to look like one on this path.
	rec = rawQuery(t, h, "StopDBInstance", map[string]string{})
	if rec.Code != http.StatusBadRequest {
		t.Errorf("StopDBInstance with no identifier: %d, want 400", rec.Code)
	}
}

// `MultiAZ=false` is a request to turn Multi-AZ off, not an absent parameter.
// The typed implementation could not tell the two apart, so it ignored the
// request — and with one implementation left, that had to be fixed rather than
// inherited.
func TestModifyDBInstance_multiAZCanBeTurnedOff(t *testing.T) {
	h, clk := newDispatchHandler(t)
	ctx := context.Background()
	const id = "dispatch-multiaz"
	createAvailableInstance(t, h, clk, id)

	off := false
	if _, aerr := h.modifyDBInstanceTyped(ctx, &modifyDBInstanceReq{
		DBInstanceIdentifier: id,
		MultiAZ:              &off,
	}); aerr != nil {
		t.Fatalf("ModifyDBInstance: %s: %s", aerr.Code, aerr.Message)
	}
	inst, aerr := h.store.getDBInstance(ctx, id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.MultiAZ {
		t.Error("MultiAZ=false was ignored — the instance is still Multi-AZ")
	}

	// And an absent MultiAZ leaves it exactly where it was.
	inst.MultiAZ = true
	if aerr := h.store.putDBInstance(ctx, inst); aerr != nil {
		t.Fatalf("putDBInstance: %s", aerr.Message)
	}
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
	if !inst.MultiAZ {
		t.Error("an absent MultiAZ turned Multi-AZ off")
	}
}

// The same request through the raw Query path, which is where `MultiAZ=false`
// used to be the only way to get the behaviour.
func TestRawQueryDispatch_multiAZCanBeTurnedOff(t *testing.T) {
	h, clk := newDispatchHandler(t)
	const id = "dispatch-multiaz-raw"
	createAvailableInstance(t, h, clk, id)

	rec := rawQuery(t, h, "ModifyDBInstance", map[string]string{
		"DBInstanceIdentifier": id,
		"MultiAZ":              "false",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("ModifyDBInstance via raw dispatch: %d: %s", rec.Code, rec.Body.String())
	}
	inst, aerr := h.store.getDBInstance(context.Background(), id)
	if aerr != nil {
		t.Fatalf("getDBInstance: %s", aerr.Message)
	}
	if inst.MultiAZ {
		t.Error("MultiAZ=false was ignored on the raw dispatch path")
	}
}

// DescribeEvents is reachable both ways too.
func TestRawQueryDispatch_describeEvents(t *testing.T) {
	h, clk := newDispatchHandler(t)
	const id = "dispatch-events"
	createAvailableInstance(t, h, clk, id)

	rec := rawQuery(t, h, "DescribeEvents", map[string]string{"SourceIdentifier": id, "SourceType": "db-instance"})
	if rec.Code != http.StatusOK {
		t.Fatalf("DescribeEvents via raw dispatch: %d: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "DB instance created.") {
		t.Errorf("DescribeEvents body carries no creation event: %s", rec.Body.String())
	}
}
