package rds

// handler_events.go — RDS events, and DescribeEvents.
//
// A DB instance that will not start records *why* on its own record
// (DBInstance.StatusReason, see health.go). That text has nowhere to go in the
// DescribeDBInstances wire shape: the real DBInstance has no StatusReason
// field, and DBInstanceStatusInfo/StatusInfos is documented read-replica-only
// ("The status of a read replica. If the DB instance isn't a read replica, the
// value is blank"), so filling it in for a primary would be inventing a field.
//
// The channel AWS does provide is RDS events. DescribeEvents is what the RDS
// console renders as an instance's failure history, and it is where a failure
// reason belongs. So Overcast records an event for the lifecycle transitions it
// already knows about — created, started, stopped, deleted, failed — and the
// failure event carries the same reason text the record holds.
//
// Fidelity notes, all from the DescribeEvents API reference:
//
//   - Events are retained for 14 days.
//   - With no StartTime and no Duration the window is the past 60 minutes.
//   - MaxRecords is 20-100, default 100.
//   - Message shapes mirror the real ones: RDS-EVENT-0087 "DB instance
//     stopped.", RDS-EVENT-0088 "DB instance started.", RDS-EVENT-0035
//     "Database instance put into {state}. {message}.", RDS-EVENT-0278 "The DB
//     instance creation failed. {message}."

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/Neaox/overcast/internal/protocol"
	"github.com/Neaox/overcast/internal/serviceutil"
)

const (
	// eventRetention matches AWS: DescribeEvents returns events for the past
	// 14 days.
	eventRetention = 14 * 24 * time.Hour

	// maxStoredEventsPerRegion bounds the event log independently of the
	// retention window, so a busy CloudFormation loop cannot grow the store
	// without limit inside a single day.
	maxStoredEventsPerRegion = 1000

	// defaultEventWindow is the window DescribeEvents uses when the caller
	// supplies neither StartTime nor Duration.
	defaultEventWindow = 60 * time.Minute

	// AWS's documented MaxRecords bounds for DescribeEvents.
	defaultEventMaxRecords = 100
	minEventMaxRecords     = 20
)

// Source types AWS documents for RDS events. Overcast only produces
// db-instance events today, but the parameter is validated against the whole
// enum because a caller filtering on db-cluster deserves an empty list rather
// than an error, and a caller filtering on a typo deserves an error.
var eventSourceTypes = map[string]bool{
	"db-instance":           true,
	"db-cluster":            true,
	"db-parameter-group":    true,
	"db-security-group":     true,
	"db-snapshot":           true,
	"db-cluster-snapshot":   true,
	"custom-engine-version": true,
	"db-proxy":              true,
	"blue-green-deployment": true,
	"tenant-database":       true,
}

const sourceTypeDBInstance = "db-instance"

// Event categories AWS documents for db-instance events.
var dbInstanceEventCategories = map[string]bool{
	"availability":         true,
	"backup":               true,
	"configuration change": true,
	"creation":             true,
	"deletion":             true,
	"failover":             true,
	"failure":              true,
	"low storage":          true,
	"maintenance":          true,
	"notification":         true,
	"read replica":         true,
	"recovery":             true,
	"restoration":          true,
	"security":             true,
}

// ── XML response types ───────────────────────────────────────────────────────

type xmlDescribeEventsResponse struct {
	XMLName          xml.Name                  `xml:"DescribeEventsResponse"`
	Xmlns            string                    `xml:"xmlns,attr"`
	Result           xmlDescribeEventsResult   `xml:"DescribeEventsResult"`
	ResponseMetadata protocol.ResponseMetadata `xml:"ResponseMetadata"`
}

type xmlDescribeEventsResult struct {
	Marker string    `xml:"Marker,omitempty"`
	Events xmlEvents `xml:"Events"`
}

type xmlEvents struct {
	Items []xmlEvent `xml:"Event"`
}

type xmlEvent struct {
	SourceIdentifier string             `xml:"SourceIdentifier"`
	SourceType       string             `xml:"SourceType"`
	Message          string             `xml:"Message"`
	EventCategories  xmlEventCategories `xml:"EventCategories"`
	Date             string             `xml:"Date"`
	SourceArn        string             `xml:"SourceArn,omitempty"`
}

type xmlEventCategories struct {
	Items []string `xml:"EventCategory"`
}

// ── Request type ─────────────────────────────────────────────────────────────

type describeEventsReq struct {
	SourceIdentifier string   `json:"SourceIdentifier"`
	SourceType       string   `json:"SourceType"`
	StartTime        string   `json:"StartTime"`
	EndTime          string   `json:"EndTime"`
	Duration         int      `json:"Duration"`
	EventCategories  []string `json:"EventCategories"`
	Marker           string   `json:"Marker"`
	MaxRecords       int      `json:"MaxRecords"`
}

// ── Recording ────────────────────────────────────────────────────────────────

// eventSeq disambiguates events recorded within the same clock tick. The store
// key is timestamp-ordered, and under a mock clock — or simply a fast
// lifecycle — several events share a nanosecond; without the sequence the
// second one would overwrite the first.
var eventSeq atomic.Uint64

// recordInstanceEvent appends a db-instance event and prunes the log back
// inside its retention window and count cap. Failures are logged, never
// surfaced: the caller has already committed the thing being reported, and
// losing an event must not fail an API call.
func (h *Handler) recordInstanceEvent(ctx context.Context, instanceID, message string, categories ...string) {
	now := h.clk.Now().UTC()
	region := h.store.region(ctx)
	log := h.log.WithRecorder(ctx)
	e := &DBEvent{
		SourceIdentifier: instanceID,
		SourceType:       sourceTypeDBInstance,
		SourceArn:        protocol.ARN(region, h.accountID(), "rds", "db:"+instanceID),
		Message:          message,
		EventCategories:  categories,
		Date:             now,
	}
	key := fmt.Sprintf("%019d-%010d", now.UnixNano(), eventSeq.Add(1))
	if aerr := h.store.putEvent(ctx, key, e); aerr != nil {
		log.Warn("RDS: record event",
			zap.String("instance", instanceID), zap.String("error", aerr.Message))
		return
	}
	h.store.pruneEvents(ctx, now.Add(-eventRetention), maxStoredEventsPerRegion)
}

// recordInstanceFailureEvent reports a database that will not come up, in the
// shape AWS uses. A failure while the instance was still being created is
// RDS-EVENT-0278's message; a failure on the way back up is RDS-EVENT-0035's.
func (h *Handler) recordInstanceFailureEvent(ctx context.Context, instanceID, priorStatus, reason string) {
	message := fmt.Sprintf("Database instance put into failed state. %s.", reason)
	if priorStatus == "creating" {
		message = fmt.Sprintf("The DB instance creation failed. %s.", reason)
	}
	h.recordInstanceEvent(ctx, instanceID, message, "failure")
}

func (h *Handler) accountID() string {
	if h.cfg != nil {
		return h.cfg.AccountID
	}
	return ""
}

// ── DescribeEvents ───────────────────────────────────────────────────────────

func (h *Handler) describeEventsTyped(ctx context.Context, req *describeEventsReq) (*xmlDescribeEventsResponse, *protocol.AWSError) {
	if req.SourceType != "" && !eventSourceTypes[req.SourceType] {
		return nil, errInvalidParameterValue(fmt.Sprintf("Invalid source type: %s", req.SourceType))
	}
	for _, c := range req.EventCategories {
		if !dbInstanceEventCategories[strings.ToLower(c)] {
			return nil, errInvalidParameterValue(fmt.Sprintf("Invalid event category: %s", c))
		}
	}

	maxRecords := req.MaxRecords
	if maxRecords == 0 {
		maxRecords = defaultEventMaxRecords
	}
	if maxRecords < minEventMaxRecords || maxRecords > defaultEventMaxRecords {
		return nil, errInvalidParameterValue(fmt.Sprintf(
			"Invalid value %d for MaxRecords. Must be between %d and %d",
			req.MaxRecords, minEventMaxRecords, defaultEventMaxRecords))
	}

	start, end, aerr := h.eventWindow(req)
	if aerr != nil {
		return nil, aerr
	}

	all, aerr := h.store.listEvents(ctx)
	if aerr != nil {
		return nil, aerr
	}

	matched := make([]xmlEvent, 0, len(all))
	for _, e := range all {
		if !eventMatches(e, req, start, end) {
			continue
		}
		matched = append(matched, toXMLEvent(e))
	}

	page, err := serviceutil.Paginate(matched, maxRecords, req.Marker,
		serviceutil.PaginateOptions{DefaultLimit: defaultEventMaxRecords, MaxLimit: defaultEventMaxRecords})
	if err != nil {
		if errors.Is(err, serviceutil.ErrInvalidPageToken) {
			return nil, errInvalidParameterValue("Invalid value for Marker")
		}
		return nil, protocol.Wrap(protocol.ErrInternalError, err)
	}

	return &xmlDescribeEventsResponse{
		Xmlns: rdsXMLNS,
		Result: xmlDescribeEventsResult{
			Marker: page.NextToken,
			Events: xmlEvents{Items: page.Items},
		},
		ResponseMetadata: protocol.ResponseMetadata{RequestID: protocol.RequestIDFromContext(ctx)},
	}, nil
}

// eventWindow resolves the [start, end) interval a DescribeEvents call is
// asking about. AWS's rules: EndTime defaults to now, StartTime wins over
// Duration, and with neither the window is the past 60 minutes.
func (h *Handler) eventWindow(req *describeEventsReq) (start, end time.Time, aerr *protocol.AWSError) {
	now := h.clk.Now().UTC()

	end = now
	if req.EndTime != "" {
		t, err := parseEventTime(req.EndTime)
		if err != nil {
			return start, end, errInvalidParameterValue("Invalid value for EndTime: " + req.EndTime)
		}
		end = t
	}

	switch {
	case req.StartTime != "":
		t, err := parseEventTime(req.StartTime)
		if err != nil {
			return start, end, errInvalidParameterValue("Invalid value for StartTime: " + req.StartTime)
		}
		start = t
	case req.Duration > 0:
		start = end.Add(-time.Duration(req.Duration) * time.Minute)
	default:
		start = end.Add(-defaultEventWindow)
	}

	// Nothing older than the retention window exists to be returned, and
	// clamping keeps a caller asking for a year of history from being told
	// something Overcast cannot honour.
	if cutoff := now.Add(-eventRetention); start.Before(cutoff) {
		start = cutoff
	}
	return start, end, nil
}

// parseEventTime accepts the ISO 8601 forms AWS SDKs put on the wire for a
// Query-protocol timestamp.
func parseEventTime(s string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("unrecognised timestamp %q", s)
}

func eventMatches(e *DBEvent, req *describeEventsReq, start, end time.Time) bool {
	if e.Date.Before(start) || e.Date.After(end) {
		return false
	}
	if req.SourceType != "" && e.SourceType != req.SourceType {
		return false
	}
	// SourceIdentifier is only meaningful alongside SourceType — AWS rejects
	// one without the other, but being permissive here costs nothing and a
	// filter that matches is more useful than an error.
	if req.SourceIdentifier != "" && e.SourceIdentifier != req.SourceIdentifier {
		return false
	}
	if len(req.EventCategories) > 0 && !hasAnyCategory(e.EventCategories, req.EventCategories) {
		return false
	}
	return true
}

func hasAnyCategory(have, want []string) bool {
	for _, w := range want {
		for _, h := range have {
			if strings.EqualFold(h, w) {
				return true
			}
		}
	}
	return false
}

func toXMLEvent(e *DBEvent) xmlEvent {
	return xmlEvent{
		SourceIdentifier: e.SourceIdentifier,
		SourceType:       e.SourceType,
		Message:          e.Message,
		EventCategories:  xmlEventCategories{Items: e.EventCategories},
		Date:             e.Date.UTC().Format("2006-01-02T15:04:05.000Z"),
		SourceArn:        e.SourceArn,
	}
}

// DescribeEvents is the raw Query entry point; it delegates to the typed
// implementation so the two dispatch paths cannot drift.
func (h *Handler) DescribeEvents(w http.ResponseWriter, r *http.Request) {
	h.invokeTypedAsQuery("DescribeEvents", w, r)
}
