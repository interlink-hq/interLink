package api

import (
	"encoding/json"
	"net/http"
)

// VersionHandler reports the version of the running interLink server.
func VersionHandler(version string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(struct {
			Version string `json:"version"`
		}{Version: version}); err != nil {
			return
		}
	}
}
