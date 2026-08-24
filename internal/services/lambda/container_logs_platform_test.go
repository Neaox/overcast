package lambda

// container_logs_platform_test.go — the host end of the init's platform
// records.
//
// The init observes the INIT phase and reports it as initproto.Records on the
// same seq-ordered stream as the container's output (internal/lambdainit).
// These tests are about what the host does with one: the AWS-shaped event it
// marshals, where that event goes under each log format and system log level,
// and where it deliberately does not go.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/clock"
	"github.com/Neaox/overcast/internal/events"
	"github.com/Neaox/overcast/internal/services/lambda/initproto"
)

// recordFrameOf is one platform-telemetry record off the init's stream.
func recordFrameOf(seq uint64, rec initproto.Record) initproto.Frame {
	return initproto.Frame{Seq: seq, T: 1700000000000, Rec: rec}
}

// newInitRecordSink is a sink for a function whose logging configuration and
// initialization type the test chooses.
func newInitRecordSink(t *testing.T, writer events.LogWriter, format string, sysLevel logLevel, initType string) *logSink {
	t.Helper()
	s := newLogSink(logSinkConfig{
		logger:       zap.NewNop(),
		clk:          clock.New(),
		logWriter:    writer,
		group:        "/aws/lambda/fn",
		stream:       "stream",
		region:       "us-east-1",
		functionName: "fn",
		initType:     initType,
		logFormat:    format,
		sysLogLevel:  sysLevel,
	})
	t.Cleanup(s.close)
	return s
}

// eventTypeOf reads the Telemetry API event type out of one CloudWatch message.
func eventTypeOf(t *testing.T, message string) string {
	t.Helper()
	var event struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal([]byte(message), &event); err != nil {
		t.Fatalf("CloudWatch message is not a JSON event: %v (%q)", err, message)
	}
	return event.Type
}

// Under LogFormat: JSON the init-phase records are CloudWatch lines, and they
// sit in the sequence position the init observed them in: initStart, then
// whatever the INIT phase printed, then initRuntimeDone and initReport. That
// ordering is the reason they travel as frames at all.
func TestLogSink_initPhaseRecordsAreOrderedWithTheInitPhasesOutput(t *testing.T) {
	writer := &stubLogWriter{}
	s := newInitRecordSink(t, writer, logFormatJSON, logLevelDebug, initTypeOnDemand)

	s.frame(recordFrameOf(1, initproto.Record{Type: initproto.RecInitStart}))
	s.frame(initproto.Frame{Seq: 2, Src: initproto.SrcStdout, T: 1700000000000, Msg: "loading the handler"})
	s.frame(recordFrameOf(3, initproto.Record{Type: initproto.RecInitRuntimeDone, Status: initproto.StatusSuccess}))
	s.frame(recordFrameOf(4, initproto.Record{Type: initproto.RecInitReport, Status: initproto.StatusSuccess, DurationMs: 125.33}))
	s.flush()

	got := writer.messages()
	if len(got) != 4 {
		t.Fatalf("CloudWatch messages = %v, want four", got)
	}
	for i, want := range []string{"platform.initStart", "", "platform.initRuntimeDone", "platform.initReport"} {
		if want == "" {
			if got[i] != "loading the handler" {
				t.Errorf("message %d = %q, want the INIT phase's own line", i, got[i])
			}
			continue
		}
		if typ := eventTypeOf(t, got[i]); typ != want {
			t.Errorf("message %d is %q, want %q", i, typ, want)
		}
	}
	// The record's own members, on the one that carries the measurement.
	if !strings.HasPrefix(got[3], `{"time":"`) {
		t.Errorf("initReport is not a Telemetry API event: %s", got[3])
	}
	if want := `"record":{"initializationType":"on-demand","phase":"init","status":"success","metrics":{"durationMs":125.33}}}`; !strings.HasSuffix(got[3], want) {
		t.Errorf("initReport record\n got: %s\nwant it to end with: %s", got[3], want)
	}

	// A record belongs to no invocation and is not container output: it is in
	// no tail, and the INIT-phase buffer the timeout diagnostic quotes holds
	// only what the container actually printed.
	if tail := string(s.snapshot("req-1")); tail != "" {
		t.Errorf("a request tail picked up an init-phase record: %q", tail)
	}
	if out := s.initOutput(); out != "loading the handler\n" {
		t.Errorf("the INIT-phase buffer = %q, want only the container's own line", out)
	}
}

// SystemLogLevel filters them exactly as it filters the invocation records, by
// AWS's mapping: at the default INFO an on-demand cold start's three records
// are all below the line, a provisioned environment's report is not, and a
// failed phase's two closing records are not either.
func TestLogSink_initPhaseRecordsAreFilteredBySystemLogLevel(t *testing.T) {
	tests := []struct {
		name     string
		level    logLevel
		initType string
		status   string
		want     []string
	}{
		{
			name:     "on-demand success at the default INFO",
			level:    logLevelInfo,
			initType: initTypeOnDemand,
			status:   initproto.StatusSuccess,
			want:     nil,
		},
		{
			name:     "on-demand success at DEBUG",
			level:    logLevelDebug,
			initType: initTypeOnDemand,
			status:   initproto.StatusSuccess,
			want:     []string{"platform.initStart", "platform.initRuntimeDone", "platform.initReport"},
		},
		{
			name:     "provisioned success at the default INFO keeps the report",
			level:    logLevelInfo,
			initType: initTypeProvisioned,
			status:   initproto.StatusSuccess,
			want:     []string{"platform.initReport"},
		},
		{
			name:     "a failed phase is visible at the default INFO",
			level:    logLevelInfo,
			initType: initTypeOnDemand,
			status:   initproto.StatusError,
			want:     []string{"platform.initRuntimeDone", "platform.initReport"},
		},
		{
			name:     "nothing survives WARN for a successful phase",
			level:    logLevelWarn,
			initType: initTypeOnDemand,
			status:   initproto.StatusSuccess,
			want:     nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writer := &stubLogWriter{}
			s := newInitRecordSink(t, writer, logFormatJSON, tc.level, tc.initType)

			s.frame(recordFrameOf(1, initproto.Record{Type: initproto.RecInitStart}))
			s.frame(recordFrameOf(2, initproto.Record{Type: initproto.RecInitRuntimeDone, Status: tc.status}))
			s.frame(recordFrameOf(3, initproto.Record{Type: initproto.RecInitReport, Status: tc.status, DurationMs: 1}))
			s.flush()

			var got []string
			for _, message := range writer.messages() {
				got = append(got, eventTypeOf(t, message))
			}
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("CloudWatch records = %v, want %v", got, tc.want)
			}

			// Filtered or not, the sequence always moves: an invocation
			// waiting on a seq at or above a dropped record must not be held
			// to its bound.
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if !s.awaitLogSeq(ctx, 3) {
				t.Error("a wait for the last record's sequence was not satisfied")
			}
		})
	}
}

// In Text mode nothing reaches CloudWatch. AWS's plain-text counterpart of
// platform.initStart is an INIT_START line whose whole content is a managed
// runtime's version and that version's ARN — neither of which Overcast has, and
// neither of which a container-image function has at all — and the other two
// records have no plain-text counterpart. The sequence still advances.
func TestLogSink_initPhaseRecordsAreNotWrittenInTextFormat(t *testing.T) {
	writer := &stubLogWriter{}
	s := newInitRecordSink(t, writer, logFormatText, logLevelDebug, initTypeOnDemand)

	records := []initproto.Record{
		{Type: initproto.RecInitStart},
		{Type: initproto.RecInitRuntimeDone, Status: initproto.StatusSuccess},
		{Type: initproto.RecInitReport, Status: initproto.StatusSuccess, DurationMs: 1},
	}
	for i, rec := range records {
		if s.frame(recordFrameOf(uint64(i+1), rec)) {
			t.Errorf("%s was batched for CloudWatch in Text mode", rec.Type)
		}
	}
	s.frame(initproto.Frame{Seq: 4, Src: initproto.SrcStdout, T: 1700000000000, Msg: "printed by the runtime"})
	s.flush()

	if got := writer.messages(); len(got) != 1 || got[0] != "printed by the runtime" {
		t.Errorf("CloudWatch messages = %v, want only the container's own line", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !s.awaitLogSeq(ctx, 3) {
		t.Error("a wait for a record's sequence was not satisfied in Text mode")
	}
}

// A record type this build does not know is still a frame: it advances the
// sequence, so nothing waits for it, and it invents no record.
func TestLogSink_unknownPlatformRecordAdvancesTheSequenceOnly(t *testing.T) {
	writer := &stubLogWriter{}
	s := newInitRecordSink(t, writer, logFormatJSON, logLevelDebug, initTypeOnDemand)

	if s.frame(recordFrameOf(7, initproto.Record{Type: "restoreStart"})) {
		t.Fatal("an unknown record was batched")
	}
	s.flush()
	if got := writer.messages(); len(got) != 0 {
		t.Errorf("CloudWatch messages = %v, want none", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if !s.awaitLogSeq(ctx, 7) {
		t.Error("a wait for the unknown record's sequence was not satisfied")
	}
}

// Telemetry/Logs API subscribers get the init-phase records whatever the log
// format is, and they get them as the event object AWS documents: a "type" of
// platform.initStart and a "record" that is an object, not a string. AWS is
// explicit that "the Telemetry API always emits platform events such as START
// and REPORT in JSON format. Configuring the format of the system logs Lambda
// sends to CloudWatch doesn't affect Lambda Telemetry API behavior", and says
// the same of the level.
func TestLogSink_initPhaseRecordsReachSubscribersInTextFormatToo(t *testing.T) {
	api, addr := newRuntimeAPITestServer(t)
	const containerIP = "127.0.0.1"
	api.RegisterContainerConfig(containerIP, runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:fn",
		FunctionName: "fn",
	})
	extID := registerExtension(t, http.DefaultClient, addr, "telemetry-extension")
	received := make(chan []map[string]any, 8)
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var batch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- batch
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()
	subscribeToLogs(t, addr, extID, dest.URL, "platform")

	s := newLogSink(logSinkConfig{
		logger:       zap.NewNop(),
		clk:          clock.New(),
		group:        "/aws/lambda/fn",
		stream:       "stream",
		region:       "us-east-1",
		functionName: "fn",
		initType:     initTypeOnDemand,
		// Text format at WARN: the CloudWatch copy of all three records is
		// suppressed twice over, and the subscriber's copy is not affected by
		// either.
		logFormat:   logFormatText,
		sysLogLevel: logLevelWarn,
		runtimeAPI:  api,
	})
	t.Cleanup(s.close)
	s.attach(containerIP, "container123456")

	s.frame(recordFrameOf(1, initproto.Record{Type: initproto.RecInitStart}))
	s.frame(recordFrameOf(2, initproto.Record{Type: initproto.RecInitRuntimeDone, Status: initproto.StatusSuccess}))
	s.frame(recordFrameOf(3, initproto.Record{Type: initproto.RecInitReport, Status: initproto.StatusSuccess, DurationMs: 125.33}))

	got := map[string]map[string]any{}
	for len(got) < 3 {
		select {
		case batch := <-received:
			for _, event := range batch {
				typ, _ := event["type"].(string)
				record, ok := event["record"].(map[string]any)
				if !ok {
					t.Fatalf("event %q carries record %#v, want a JSON object", typ, event["record"])
				}
				if timestamp, _ := event["time"].(string); timestamp == "" {
					t.Errorf("event %q carries no time", typ)
				}
				got[typ] = record
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the subscriber received %v, want all three init-phase records", got)
		}
	}

	start, ok := got["platform.initStart"]
	if !ok {
		t.Fatalf("no platform.initStart in %v", got)
	}
	if start["initializationType"] != initTypeOnDemand || start["phase"] != initPhaseInit ||
		start["functionName"] != "fn" || start["functionVersion"] != functionVersionLatest {
		t.Errorf("platform.initStart record = %#v", start)
	}
	if done := got["platform.initRuntimeDone"]; done["status"] != invokeStatusSuccess {
		t.Errorf("platform.initRuntimeDone record = %#v", done)
	}
	report, ok := got["platform.initReport"]
	if !ok {
		t.Fatalf("no platform.initReport in %v", got)
	}
	metrics, _ := report["metrics"].(map[string]any)
	if metrics == nil || metrics["durationMs"] != 125.33 {
		t.Errorf("platform.initReport metrics = %#v, want the init's measured duration", report["metrics"])
	}
}

// The invocation records are the host's, not the init's, and they were already
// published to subscribers — as a string carrying whatever the log format had
// chosen. They are events now: in Text mode CloudWatch and the tail keep the
// plain-text START line byte for byte, and the subscriber gets the
// platform.start event, because the Telemetry API is not affected by the log
// format.
func TestEmitPlatformRecord_subscribersSeeTheJSONEventInTextMode(t *testing.T) {
	ci, addr := newRICBackedContainerInstance(t)
	extID := registerExtension(t, http.DefaultClient, addr, "telemetry-extension")
	received := make(chan []map[string]any, 8)
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var batch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- batch
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()
	subscribeToLogs(t, addr, extID, dest.URL, "platform")

	if ci.logFormat != logFormatText && ci.logFormat != "" {
		t.Fatalf("this test is about Text mode, but the instance is %q", ci.logFormat)
	}
	ci.emitInvocationStart("req-1")

	select {
	case batch := <-received:
		if len(batch) != 1 {
			t.Fatalf("the subscriber got %d events, want one", len(batch))
		}
		event := batch[0]
		if event["type"] != "platform.start" {
			t.Errorf("event type = %v, want platform.start", event["type"])
		}
		record, ok := event["record"].(map[string]any)
		if !ok {
			t.Fatalf("event record = %#v, want a JSON object", event["record"])
		}
		if record["requestId"] != "req-1" || record["version"] != functionVersionLatest {
			t.Errorf("platform.start record = %#v", record)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the subscriber never received the platform.start event")
	}

	// The tail is the plain-text line, unchanged.
	if got, want := string(ci.logSink.snapshot("req-1")), "START RequestId: req-1 Version: $LATEST\n"; got != want {
		t.Errorf("req-1 tail = %q, want %q", got, want)
	}
}

// The INIT phase reports itself before there is anywhere to report it to:
// platform.initStart is published as the container starts, which is before
// Docker will say what address the container has and before the extensions
// this environment will run have even been spawned. AWS still delivers all
// three init-phase records to an extension that subscribes during INIT — being
// told about the phase it was started in is what an extension is for — so they
// are held and replayed, at both places they can be lost.
func TestInitPhaseRecordsReachAnExtensionThatSubscribesDuringInit(t *testing.T) {
	api, addr := newRuntimeAPITestServer(t)
	const containerIP = "127.0.0.1"
	api.RegisterContainerConfig(containerIP, runtimeContainerConfig{
		FunctionARN:  "arn:aws:lambda:us-east-1:000000000000:function:fn",
		FunctionName: "fn",
	})

	received := make(chan []map[string]any, 8)
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var batch []map[string]any
		if err := json.NewDecoder(r.Body).Decode(&batch); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- batch
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()

	s := newLogSink(logSinkConfig{
		logger:       zap.NewNop(),
		clk:          clock.New(),
		group:        "/aws/lambda/fn",
		stream:       "stream",
		region:       "us-east-1",
		functionName: "fn",
		initType:     initTypeOnDemand,
		logFormat:    logFormatJSON,
		sysLogLevel:  logLevelDebug,
		runtimeAPI:   api,
	})
	t.Cleanup(s.close)

	// The container has no address yet, and nothing is subscribed.
	s.frame(recordFrameOf(1, initproto.Record{Type: initproto.RecInitStart}))
	s.attach(containerIP, "container123456")
	s.frame(recordFrameOf(2, initproto.Record{Type: initproto.RecInitRuntimeDone, Status: initproto.StatusSuccess}))
	s.frame(recordFrameOf(3, initproto.Record{Type: initproto.RecInitReport, Status: initproto.StatusSuccess, DurationMs: 9.5}))

	// Only now does the extension get as far as subscribing, which is what a
	// real one does: it is started inside the phase it wants to hear about.
	extID := registerExtension(t, http.DefaultClient, addr, "telemetry-extension")
	subscribeToLogs(t, addr, extID, dest.URL, "platform")

	got := map[string]bool{}
	for len(got) < 3 {
		select {
		case batch := <-received:
			for _, event := range batch {
				typ, _ := event["type"].(string)
				if _, ok := event["record"].(map[string]any); !ok {
					t.Fatalf("event %q carries record %#v, want a JSON object", typ, event["record"])
				}
				got[typ] = true
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("the subscriber received %v, want all three init-phase records", got)
		}
	}
	for _, want := range []string{"platform.initStart", "platform.initRuntimeDone", "platform.initReport"} {
		if !got[want] {
			t.Errorf("%s never reached the subscriber; got %v", want, got)
		}
	}
}

// ─── attach ordering ─────────────────────────────────────────────────────────

// gatedPublisher records init-phase publishes in order, holding the very first
// one until the test opens the gate — which is how the test parks attach's
// drain mid-flight and proves a concurrently arriving record cannot overtake
// the held ones.
type gatedPublisher struct {
	gate    chan struct{} // closed by the test to let the first publish finish
	started chan struct{} // closed by the publisher once it is inside the first publish

	mu     sync.Mutex
	events []string
	first  bool
}

func (p *gatedPublisher) PublishInitPhaseRecord(_ string, event string) {
	p.mu.Lock()
	hold := !p.first
	p.first = true
	p.mu.Unlock()
	if hold {
		close(p.started)
		<-p.gate
	}
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()
}

func (p *gatedPublisher) PublishExtensionLog(string, string, string) {}

// TestLogSink_attachDoesNotLetALateRecordOvertakeHeldOnes pins the order the
// Runtime API sees init-phase records in when one arrives during attach's
// drain. The Runtime API retains them in publish order and replays that order
// to a subscription made later, so an inversion here is an extension told its
// INIT phase happened backwards.
func TestLogSink_attachDoesNotLetALateRecordOvertakeHeldOnes(t *testing.T) {
	pub := &gatedPublisher{gate: make(chan struct{}), started: make(chan struct{})}
	s := newLogSink(logSinkConfig{
		logger:       zap.NewNop(),
		clk:          clock.New(),
		functionName: "fn",
		initType:     initTypeOnDemand,
		runtimeAPI:   pub,
	})
	t.Cleanup(s.close)

	// Two records arrive before Docker has said where the container is.
	s.frame(recordFrameOf(1, initproto.Record{Type: initproto.RecInitStart}))
	s.frame(recordFrameOf(2, initproto.Record{Type: initproto.RecInitRuntimeDone, Status: initproto.StatusSuccess}))

	// attach starts draining them, and the publisher parks it inside the
	// first publish.
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.attach("10.0.0.9", "cid")
	}()
	<-pub.started

	// A third record arrives mid-drain. Before the fix it saw the address
	// already set, published itself directly, and overtook the two held
	// records the drain had not finished with.
	s.frame(recordFrameOf(3, initproto.Record{
		Type:       initproto.RecInitReport,
		Status:     initproto.StatusSuccess,
		DurationMs: 12,
	}))

	close(pub.gate)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("attach never finished draining")
	}

	pub.mu.Lock()
	defer pub.mu.Unlock()
	var types []string
	for _, event := range pub.events {
		types = append(types, eventTypeOf(t, event))
	}
	want := []string{"platform.initStart", "platform.initRuntimeDone", "platform.initReport"}
	if len(types) != len(want) {
		t.Fatalf("published %d records %v, want %v", len(types), types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Fatalf("records published as %v, want %v — a record arriving during attach overtook the held phase", types, want)
		}
	}
}
