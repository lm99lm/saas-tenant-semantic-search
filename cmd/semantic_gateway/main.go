package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/infrai-examples/saas-tenant-semantic-search/saassearch"
)

func main() {
	apiKey := os.Getenv("INFRAI_API_KEY")
	if apiKey == "" {
		log.Fatal("INFRAI_API_KEY is required")
	}
	searcher := saassearch.New(apiKey, "saas-operations", 1536)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := searcher.EnsureCollection(ctx); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /documents", func(w http.ResponseWriter, r *http.Request) {
		var docs []saassearch.Document
		if err := json.NewDecoder(r.Body).Decode(&docs); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid document list"})
			return
		}
		if err := searcher.Index(r.Context(), docs); err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]int{"indexed": len(docs)})
	})
	mux.HandleFunc("GET /search", func(w http.ResponseWriter, r *http.Request) {
		matches, err := searcher.Search(r.Context(), r.URL.Query().Get("tenant_id"), r.URL.Query().Get("q"), 5)
		if err != nil {
			writeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"matches": matches})
	})

	server := &http.Server{Addr: ":8080", Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Printf("semantic gateway listening on %s", server.Addr)
	log.Fatal(server.ListenAndServe())
}

func writeError(w http.ResponseWriter, err error) {
	status := http.StatusBadGateway
	var apiErr *saassearch.APIError
	if errors.As(err, &apiErr) && apiErr.StatusCode >= 400 && apiErr.StatusCode < 500 {
		status = apiErr.StatusCode
	} else if !errors.As(err, &apiErr) {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
