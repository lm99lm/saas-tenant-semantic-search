package saassearch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

func TestSearchPinsEveryVectorQueryToTenant(t *testing.T) {
	tests := []struct {
		name     string
		tenantID string
	}{
		{name: "onboarding workspace", tenantID: "tenant-north"},
		{name: "admin workspace", tenantID: "tenant-south"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/v1/embeddings":
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"data":[{"embedding":[0.2,0.8],"index":0,"object":"embedding"}],"model":"text-embedding-3-small","object":"list","usage":{"prompt_tokens":2,"total_tokens":2}}`))
				case "/v1/vector/query":
					if r.Method != http.MethodPost {
						t.Fatalf("method = %s", r.Method)
					}
					if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
						t.Fatalf("authorization = %q", got)
					}
					if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
						t.Fatal(err)
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"ok":true,"data":{"matches":[{"id":"runbook-7","score":0.91,"metadata":{"tenant_id":"` + tt.tenantID + `"}}]},"error":null,"metadata":{}}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			client := openai.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(server.URL+"/v1"))
			searcher := New("test-key", "saas-operations", 2)
			searcher.baseURL = server.URL + "/v1"
			searcher.httpClient = server.Client()
			searcher.embeddings = client.Embeddings
			searcher.sleep = func(context.Context, time.Duration) error { return nil }

			matches, err := searcher.Search(context.Background(), tt.tenantID, "rotate an admin", 3)
			if err != nil {
				t.Fatal(err)
			}
			filter := captured["filter"].(map[string]any)
			if filter["tenant_id"] != tt.tenantID {
				t.Fatalf("tenant filter = %v", filter["tenant_id"])
			}
			if len(matches) != 1 || matches[0].Metadata["tenant_id"] != tt.tenantID {
				t.Fatalf("matches = %#v", matches)
			}
		})
	}
}
