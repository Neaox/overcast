//go:build slim

package main

import "net/http"

func newUIHandler(_ int, _ string) (http.Handler, error) {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "web UI not included in slim build", http.StatusNotFound)
	}), nil
}
