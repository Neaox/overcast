//go:build slim

package main

import "net/http"

func newUIHandler(_, _ int, _ string, _ bool, _ bool, _ []byte) (http.Handler, error) {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "web UI not included in slim build", http.StatusNotFound)
	}), nil
}
