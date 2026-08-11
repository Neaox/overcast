package trace

// deepsearch_race_test.go — the scan reads a trace that is still being written.
//
// Deep search takes slice headers under a brief read lock and does the matching
// outside every lock, which is the only way to scan 8 MiB without blocking the
// AddHop of the deploy being searched for. That is safe because a Hop's bodies
// and a LogEntry's fields are never touched after they are appended — see
// searchableFields. This is the test that would catch it if that ever stopped
// being true, and it is worth nothing unless run under -race.

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestDeepSearch_scansWhileTheTraceIsStillBeingWritten(t *testing.T) {
	buf := NewBuffer(50)

	// A trace that goes on recording for the whole test, as a CloudFormation
	// deploy does long after its request has been answered.
	rec := NewRecorder("in-flight", time.Now(), http.MethodPost, "/", "localhost", "", http.Header{})
	rec.SetServiceInfo("cloudformation", "CreateStack", "us-east-1")
	buf.Add(rec)

	stop := make(chan struct{})
	var writers sync.WaitGroup

	writers.Add(2)
	go func() {
		defer writers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			rec.AddHop(Hop{
				Service:      "ecr",
				Operation:    "DescribeImages",
				ResponseBody: []byte(`{"images":[` + strconv.Itoa(i) + `]}`),
				Error:        "hop " + strconv.Itoa(i),
			})
		}
	}()
	go func() {
		defer writers.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			rec.AddLog(LogEntry{Level: "info", Message: "line " + strconv.Itoa(i)})
		}
	}()

	// Scan repeatedly against the moving target. Whether any given match is
	// found is not the assertion — what arrived mid-scan is legitimately
	// undefined — only that concurrent reading and writing is safe.
	var readers sync.WaitGroup
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for i := 0; i < 50; i++ {
				buf.DeepSearch(context.Background(), DeepFilter{Query: "describeimages"})
				buf.ListSummaries(ListFilter{Search: "createstack", Limit: 10})
			}
		}()
	}

	readers.Wait()
	close(stop)
	writers.Wait()
}
