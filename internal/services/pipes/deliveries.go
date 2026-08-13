package pipes

// Wiring and delivery visibility for the web console.
//
// The pipes page could show a pipe as RUNNING when its source/target
// combination did nothing at all. CreatePipe now refuses those combinations, so
// the only way one can exist is in state written by an older build — and that
// is exactly the case the console has to be able to show. /wiring reports each
// pipe's resolved source, enrichment and target types plus whether the pipe is
// actually wired; /deliveries reports what recently flowed through it.
//
// Outcomes live in a fixed-size in-process ring rather than state.Store: they
// are diagnostic data with no durability requirement, and a store write per
// batch would put I/O on the delivery path for nothing. Same reasoning, and
// same shape, as internal/services/eventbridge/deliveries.go.

import (
	"net/http"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/eventtarget"
	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

// deliveryLogSize is how many recent pipe executions are retained, across all
// pipes. Sized to cover a debugging session without unbounded growth.
const deliveryLogSize = 500

// Execution outcomes, in the vocabulary the console renders.
const (
	outcomeDelivered = "delivered"
	outcomeFiltered  = "filtered"
	outcomeFailed    = "failed"
	outcomeDLQ       = "dlq"
)

// deliveryRecord is one pipe execution.
type deliveryRecord struct {
	Region     string `json:"Region"`
	Pipe       string `json:"Pipe"`
	SourceARN  string `json:"SourceArn"`
	TargetARN  string `json:"TargetArn"`
	TargetType string `json:"TargetType"`
	Records    int    `json:"Records"`
	Outcome    string `json:"Outcome"`
	// Attempts is how many delivery attempts the execution took. A stream
	// source retries in place (see executeStream); a polled source retries by
	// leaving its cursor or message alone, so it always reports 1.
	Attempts int       `json:"Attempts"`
	Error    string    `json:"Error,omitempty"`
	Time     time.Time `json:"Time"`
}

// deliveryLog is a fixed-capacity ring of execution outcomes, newest last.
type deliveryLog struct {
	mu      sync.Mutex
	records []deliveryRecord
	next    int
	filled  bool
}

func newDeliveryLog() *deliveryLog {
	return &deliveryLog{}
}

// add records one outcome, overwriting the oldest entry when full.
func (l *deliveryLog) add(rec deliveryRecord) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.records == nil {
		l.records = make([]deliveryRecord, deliveryLogSize)
	}
	l.records[l.next] = rec
	l.next = (l.next + 1) % len(l.records)
	if l.next == 0 {
		l.filled = true
	}
}

// snapshot returns the retained records newest-first, optionally filtered by
// region and pipe name. An empty filter value matches everything.
func (l *deliveryLog) snapshot(region, pipe string) []deliveryRecord {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	count := l.next
	if l.filled {
		count = len(l.records)
	}
	out := make([]deliveryRecord, 0, count)
	for i := 0; i < count; i++ {
		idx := (l.next - 1 - i + len(l.records)) % len(l.records)
		rec := l.records[idx]
		if region != "" && rec.Region != region {
			continue
		}
		if pipe != "" && rec.Pipe != pipe {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// recordDelivery appends an outcome to the handler's delivery log.
func (h *Handler) recordDelivery(rec deliveryRecord) {
	if h.deliveries == nil {
		return
	}
	rec.Time = h.clk.Now().UTC()
	h.deliveries.add(rec)
}

// pipeWiringView is one pipe's resolved wiring, as the console renders it.
type pipeWiringView struct {
	Name           string `json:"Name"`
	Arn            string `json:"Arn"`
	CurrentState   string `json:"CurrentState"`
	DesiredState   string `json:"DesiredState"`
	Source         string `json:"Source"`
	SourceType     string `json:"SourceType"`
	Enrichment     string `json:"Enrichment,omitempty"`
	EnrichmentType string `json:"EnrichmentType,omitempty"`
	Target         string `json:"Target"`
	TargetType     string `json:"TargetType"`
	// Wired is false for a pipe whose configuration this build cannot run, so
	// the console can show "stored but inert" rather than implying it is live.
	Wired bool `json:"Wired"`
	// Reason says why an unwired pipe will never fire.
	Reason string `json:"Reason,omitempty"`
}

// adminListWiring serves GET /_overcast/pipes/wiring.
//
// This is an emulator-only console endpoint, not an AWS API surface — it lives
// under /_overcast/ alongside the other console feeds for the same reason.
func (h *Handler) adminListWiring(w http.ResponseWriter, r *http.Request) {
	pipes, aerr := h.store.listPipes(r.Context())
	if aerr != nil {
		writeError(w, r, aerr)
		return
	}
	views := make([]pipeWiringView, 0, len(pipes))
	for _, p := range pipes {
		views = append(views, wiringView(p))
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Pipes": views})
}

// wiringView resolves one pipe's legs for the console.
//
// Each leg is resolved independently so the view still names the two that are
// fine when the third is not, and the first failure becomes the reason the pipe
// is inert.
func wiringView(p *Pipe) pipeWiringView {
	const unknown = "Unknown"
	view := pipeWiringView{
		Name:         p.Name,
		Arn:          p.Arn,
		CurrentState: string(p.CurrentState),
		DesiredState: string(p.DesiredState),
		Source:       p.SourceArn,
		SourceType:   unknown,
		Enrichment:   p.Enrichment,
		Target:       p.TargetArn,
		TargetType:   unknown,
	}
	note := func(err error) {
		if view.Reason == "" {
			view.Reason = err.Error()
		}
	}

	if kind, err := classifySourceARN(p.SourceArn); err == nil {
		view.SourceType = sourceDisplayName(kind)
	} else {
		note(err)
	}
	if p.Enrichment != "" {
		view.EnrichmentType = unknown
		if kind, err := classifyEnrichment(p.Enrichment); err == nil {
			view.EnrichmentType = eventtarget.DisplayName(kind)
		} else {
			note(err)
		}
	}
	if kind, err := classifyTarget(p.TargetArn); err == nil {
		view.TargetType = eventtarget.DisplayName(kind)
	} else {
		note(err)
	}

	view.Wired = view.Reason == ""
	return view
}

// adminListDeliveries serves GET /_overcast/pipes/deliveries, the console's
// per-pipe execution feed. Query parameters pipe, region and limit narrow the
// result; all are optional.
func (h *Handler) adminListDeliveries(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	if region == "" {
		region = h.store.region(r.Context())
	}
	records := h.deliveries.snapshot(region, r.URL.Query().Get("pipe"))
	if records == nil {
		records = []deliveryRecord{}
	}
	if limit := serviceutil.ParseIntDefault(r.URL.Query().Get("limit"), 0); limit > 0 && limit < len(records) {
		records = records[:limit]
	}
	protocol.WriteJSON(w, r, http.StatusOK, map[string]any{"Deliveries": records})
}

// registerAdminRoutes mounts the console-only Pipes endpoints.
func (h *Handler) registerAdminRoutes(r chi.Router) {
	r.Get("/_overcast/pipes/wiring", h.adminListWiring)
	r.Get("/_overcast/pipes/deliveries", h.adminListDeliveries)
}
