package router

import (
	"encoding/json"
	"net/http"

	"github.com/Neaox/overcast/internal/inithooks"
	"github.com/go-chi/chi/v5"
)

// initStatusHandler returns the state of all init hook stages and scripts.
// GET /_overcast/init — init hook status endpoint.
func initStatusHandler(runner *inithooks.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if runner == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(inithooks.InitStatus{
				Completed: map[inithooks.Stage]bool{
					inithooks.StageBoot:     false,
					inithooks.StageStart:    false,
					inithooks.StageReady:    false,
					inithooks.StageShutdown: false,
				},
				Scripts: []inithooks.ScriptResult{},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(runner.Status())
	}
}

// initStageStatusHandler returns the state of a single init hook stage.
// GET /_overcast/init/{stage} — per-stage init hook status endpoint.
func initStageStatusHandler(runner *inithooks.Runner) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		stageStr := chi.URLParam(r, "stage")
		stage, ok := inithooks.ParseStage(stageStr)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{
				"error": "unknown stage: " + stageStr,
			})
			return
		}

		if runner == nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(inithooks.StageInfo{
				Completed: false,
				Scripts:   []inithooks.ScriptResult{},
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(runner.StageStatus(stage))
	}
}
