package inithooks_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Neaox/overcast/internal/inithooks"
	"github.com/Neaox/overcast/tests/helpers"
)

func TestInitStatus_noHooks(t *testing.T) {
	// Given: a test server with no init hooks configured
	srv := helpers.NewTestServer(t)

	// When: we query the init status endpoint
	resp, err := http.Get(srv.URL + "/_overcast/init")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Then: we get a valid JSON response with all stages
	helpers.AssertStatus(t, resp, http.StatusOK)

	var status inithooks.InitStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	assert.Contains(t, status.Completed, inithooks.StageBoot)
	assert.Contains(t, status.Completed, inithooks.StageStart)
	assert.Contains(t, status.Completed, inithooks.StageReady)
	assert.Contains(t, status.Completed, inithooks.StageShutdown)
}

func TestInitStageStatus_noHooks(t *testing.T) {
	// Given: a test server with no init hooks configured
	srv := helpers.NewTestServer(t)

	// When: we query a specific stage
	resp, err := http.Get(srv.URL + "/_overcast/init/ready")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Then: we get a valid response
	helpers.AssertStatus(t, resp, http.StatusOK)

	var info inithooks.StageInfo
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&info))
	assert.False(t, info.Completed)
}

func TestInitStageStatus_invalidStage(t *testing.T) {
	// Given: a test server
	srv := helpers.NewTestServer(t)

	// When: we query an invalid stage
	resp, err := http.Get(srv.URL + "/_overcast/init/invalid")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Then: we get a 400 error
	helpers.AssertStatus(t, resp, http.StatusBadRequest)
}

func TestInitStatus_withHookRunner(t *testing.T) {
	// Given: a hook runner with a ready.d script and a test server using it
	base := t.TempDir()
	readyDir := filepath.Join(base, "ready.d")
	require.NoError(t, os.MkdirAll(readyDir, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(readyDir, "seed.sh"),
		[]byte("#!/bin/sh\ntrue\n"),
		0o755,
	))

	runner := inithooks.NewRunner([]string{base}, nil, 5*time.Second, nil)
	runner.Discover()
	runner.Run(t.Context(), inithooks.StageReady)

	srv := helpers.NewTestServer(t, helpers.WithInitRunner(runner))

	// When: we query the init status
	resp, err := http.Get(srv.URL + "/_overcast/init")
	require.NoError(t, err)
	defer resp.Body.Close()

	// Then: READY stage is completed with the script in SUCCESSFUL state
	helpers.AssertStatus(t, resp, http.StatusOK)

	var status inithooks.InitStatus
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&status))
	assert.True(t, status.Completed[inithooks.StageReady])

	var readyScripts []inithooks.ScriptResult
	for _, s := range status.Scripts {
		if s.Stage == inithooks.StageReady {
			readyScripts = append(readyScripts, s)
		}
	}
	require.Len(t, readyScripts, 1)
	assert.Equal(t, "seed.sh", readyScripts[0].Name)
	assert.Equal(t, inithooks.StateSuccessful, readyScripts[0].State)
}
