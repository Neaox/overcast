package router

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Neaox/overcast/internal/smtp"
)

// inboxHandlers returns a chi.Router that mounts the inbox capture API under
// /_overcast/inbox/. Only registered when the SMTP mock server is enabled.
//
// Endpoints:
//
//	GET    /_overcast/inbox/messages            list all captured messages (newest first)
//	GET    /_overcast/inbox/messages/{id}       get a single message
//	DELETE /_overcast/inbox/messages            clear all messages
//	DELETE /_overcast/inbox/messages/{id}       delete a single message
func inboxHandlers(store *smtp.MailStore) func(chi.Router) {
	return func(r chi.Router) {
		r.Get("/messages", func(w http.ResponseWriter, req *http.Request) {
			msgs := store.List()

			// Optional ?limit= query param.
			if limitStr := req.URL.Query().Get("limit"); limitStr != "" {
				if n, err := strconv.Atoi(limitStr); err == nil && n > 0 && n < len(msgs) {
					msgs = msgs[:n]
				}
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(msgs) //nolint:errcheck
		})

		r.Get("/messages/{id}", func(w http.ResponseWriter, req *http.Request) {
			id := chi.URLParam(req, "id")
			msg := store.Get(id)
			if msg == nil {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(msg) //nolint:errcheck
		})

		r.Delete("/messages", func(w http.ResponseWriter, req *http.Request) {
			store.Clear()
			w.WriteHeader(http.StatusNoContent)
		})

		r.Delete("/messages/{id}", func(w http.ResponseWriter, req *http.Request) {
			id := chi.URLParam(req, "id")
			if !store.Delete(id) {
				http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})
	}
}
