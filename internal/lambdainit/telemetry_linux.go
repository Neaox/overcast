//go:build linux

package lambdainit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/overcast-sh/overcast/internal/services/lambda/initproto"
)

const (
	// telemetryPostTimeout bounds one POST to a subscriber's destination. It is
	// the same second the host used to give its own attempt, kept here so the
	// budget an extension's listener is judged against did not change when the
	// POST moved inside the sandbox — the host's wait for the result is wider
	// than this, so a destination that is merely slow is still the init's
	// verdict rather than a timeout race between the two sides.
	telemetryPostTimeout = time.Second

	// telemetryDialTimeout bounds the connect to the host's poll endpoint. The
	// poll itself is unbounded: it is a long poll, and the host answers it when
	// there is a batch or when its own wait elapses.
	telemetryDialTimeout = 2 * time.Second

	// Reconnect backoff for the poll channel, the same shape the log shipper
	// uses and for the same reason: the only thing the first attempts ever wait
	// out is the host finishing the container's registration.
	telemetryFirstBackoff = 5 * time.Millisecond
	telemetryBackoffMin   = 50 * time.Millisecond
	telemetryBackoffMax   = 2 * time.Second
)

// telemetryRelay carries Telemetry API deliveries from the host into the
// sandbox, so nothing has to connect the other way.
//
// It is one loop, deliberately: every exchange with the host both reports the
// delivery the previous one handed out and collects the next, so a single
// parked request is the whole channel and one batch is in flight at a time.
// That makes an execution environment's deliveries strictly ordered — better
// than the pool of host workers that used to POST them — at the cost of a slow
// destination delaying the batch behind it. The cost is bounded by
// [telemetryPostTimeout], and the host's retry and drop accounting are
// unchanged by where the POST happens.
//
// It parses nothing it carries: the body is the host's bytes and goes to the
// destination as it arrived.
type telemetryRelay struct {
	url  string
	poll *http.Client
	post *http.Client
	diag *diagLog
}

func newTelemetryRelay(hostAddr string, diag *diagLog) *telemetryRelay {
	dial := (&net.Dialer{Timeout: telemetryDialTimeout, KeepAlive: 30 * time.Second}).DialContext
	return &telemetryRelay{
		url:  "http://" + hostAddr + initproto.TelemetryPath,
		diag: diag,
		// Two clients, two connection pools. The poll is held open against the
		// host for as long as there is nothing to deliver; the destination
		// POSTs are short requests to a listener inside this container. Sharing
		// a transport would put a parked long poll and the delivery traffic in
		// the same idle pool.
		poll: &http.Client{Transport: &http.Transport{
			DialContext:        dial,
			DisableCompression: true,
			MaxIdleConns:       2,
			ForceAttemptHTTP2:  false,
		}},
		post: &http.Client{
			Timeout: telemetryPostTimeout,
			Transport: &http.Transport{
				DialContext:        dial,
				DisableCompression: true,
				MaxIdleConns:       4,
				ForceAttemptHTTP2:  false,
			},
		},
	}
}

// run keeps the poll channel open for the life of the container, delivering
// each batch the host hands out and reporting the outcome on the next poll.
func (r *telemetryRelay) run(ctx context.Context) {
	// pending survives a failed exchange: the host is still waiting on that
	// delivery's result, so it is carried until an exchange actually completes
	// rather than being dropped with the connection. A result the host never
	// hears becomes a retry there, which is the at-least-once behaviour the
	// Telemetry API path has always had.
	var pending *initproto.TelemetryResult
	backoff := telemetryFirstBackoff
	for ctx.Err() == nil {
		delivery, ok, err := r.exchange(ctx, pending)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			r.diag.printf("telemetry channel: %v", err)
			select {
			case <-time.After(backoff):
			case <-ctx.Done():
				return
			}
			backoff *= 2
			if backoff > telemetryBackoffMax {
				backoff = telemetryBackoffMax
			}
			continue
		}
		pending = nil
		backoff = telemetryBackoffMin
		if !ok {
			continue
		}
		result := initproto.TelemetryResult{ID: delivery.ID}
		if err := r.deliver(ctx, delivery); err != nil {
			result.Error = err.Error()
			r.diag.printf("telemetry delivery to %s failed: %v", delivery.URI, err)
		}
		pending = &result
	}
}

// exchange is one poll: it reports result, if there is one, and returns the
// next delivery. The false return is the host having nothing to hand out.
func (r *telemetryRelay) exchange(ctx context.Context, result *initproto.TelemetryResult) (initproto.TelemetryDelivery, bool, error) {
	var none initproto.TelemetryDelivery
	body, err := json.Marshal(initproto.TelemetryPoll{Result: result})
	if err != nil {
		return none, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.url, bytes.NewReader(body))
	if err != nil {
		return none, false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.poll.Do(req)
	if err != nil {
		return none, false, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	switch resp.StatusCode {
	case http.StatusOK:
		var delivery initproto.TelemetryDelivery
		if err := json.NewDecoder(resp.Body).Decode(&delivery); err != nil {
			return none, false, fmt.Errorf("undecodable delivery: %w", err)
		}
		return delivery, true, nil
	case http.StatusNoContent:
		return none, false, nil
	default:
		return none, false, fmt.Errorf("poll returned %s", resp.Status)
	}
}

// deliver POSTs one batch to the destination the extension subscribed. Any
// response means the bytes arrived and the delivery is done — the same reading
// the host applied when it made this POST itself; only a transport failure is
// an error the host will retry.
func (r *telemetryRelay) deliver(ctx context.Context, delivery initproto.TelemetryDelivery) error {
	if delivery.URI == "" {
		return errors.New("the delivery names no destination")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, delivery.URI, bytes.NewReader(delivery.Body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := r.post.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return nil
}
