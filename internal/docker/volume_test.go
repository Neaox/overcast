package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// newVolumeTestClient returns a Client pointed at a fake daemon plus the
// request log the handler appends to.
func newVolumeTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewClient("tcp://"+srv.Listener.Addr().String(), zap.NewNop())
}

func TestCreateVolume_sendsNameAndLabels(t *testing.T) {
	var got createVolumeRequest
	c := newVolumeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/volumes/create") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode create request: %v", err)
		}
		w.Write([]byte(`{"Name":"overcast-efs-fs-1"}`)) //nolint:errcheck
	})

	labels := ManagedLabels("efs", "fs-1")
	if err := c.CreateVolume(context.Background(), "overcast-efs-fs-1", labels); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if got.Name != "overcast-efs-fs-1" {
		t.Fatalf("expected volume name in request, got %q", got.Name)
	}
	if got.Labels[LabelManaged] != "true" || got.Labels[LabelService] != "efs" {
		t.Fatalf("expected managed labels, got %#v", got.Labels)
	}
}

func TestRemoveVolume_toleratesMissing(t *testing.T) {
	c := newVolumeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			t.Errorf("unexpected method %s", r.Method)
		}
		w.WriteHeader(http.StatusNotFound)
	})
	if err := c.RemoveVolume(context.Background(), "overcast-efs-gone", true); err != nil {
		t.Fatalf("RemoveVolume on missing volume should not error, got %v", err)
	}
}

func TestRemoveVolume_surfacesDaemonErrors(t *testing.T) {
	c := newVolumeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict) // volume in use
	})
	if err := c.RemoveVolume(context.Background(), "overcast-efs-busy", false); err == nil {
		t.Fatal("expected error for 409 response")
	}
}

func TestListVolumes_filtersByServiceLabel(t *testing.T) {
	var gotFilters string
	c := newVolumeTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotFilters, _ = url.QueryUnescape(r.URL.Query().Get("filters"))
		w.Write([]byte(`{"Volumes":[{"Name":"overcast-efs-fs-1","Labels":{"overcast.managed":"true","overcast.service":"efs","overcast.resource-id":"fs-1"}}]}`)) //nolint:errcheck
	})

	volumes, err := c.ListVolumes(context.Background(), "efs")
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if !strings.Contains(gotFilters, LabelManaged+"=true") || !strings.Contains(gotFilters, LabelService+"=efs") {
		t.Fatalf("expected managed+service label filters, got %q", gotFilters)
	}
	if len(volumes) != 1 || volumes[0].Name != "overcast-efs-fs-1" {
		t.Fatalf("unexpected volumes: %#v", volumes)
	}
	if volumes[0].Service() != "efs" || volumes[0].ResourceID() != "fs-1" {
		t.Fatalf("label accessors wrong: %#v", volumes[0])
	}
}
