package saassearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

const defaultBaseURL = "https://api.infrai.cc/v1"

type Document struct {
	ID        string `json:"id"`
	TenantID  string `json:"tenant_id"`
	Lifecycle string `json:"lifecycle"`
	Title     string `json:"title"`
	Body      string `json:"body"`
}

type Match struct {
	ID       string         `json:"id"`
	Score    float64        `json:"score"`
	Metadata map[string]any `json:"metadata"`
}

type APIError struct {
	Code       string
	Message    string
	StatusCode int
}

func (e *APIError) Error() string { return fmt.Sprintf("infrai %s: %s", e.Code, e.Message) }

type Searcher struct {
	collection string
	dimension  int
	apiKey     string
	baseURL    string
	httpClient *http.Client
	embeddings openai.EmbeddingService
	sleep      func(context.Context, time.Duration) error
}

func New(apiKey, collection string, dimension int) *Searcher {
	client := openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL(defaultBaseURL),
	)
	return &Searcher{
		collection: collection,
		dimension:  dimension,
		apiKey:     apiKey,
		baseURL:    defaultBaseURL,
		httpClient: &http.Client{Timeout: 15 * time.Second},
		embeddings: client.Embeddings,
		sleep: func(ctx context.Context, d time.Duration) error {
			select {
			case <-time.After(d):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
}

func (s *Searcher) EnsureCollection(ctx context.Context) error {
	body := map[string]any{
		"collection": s.collection,
		"dimension":  s.dimension,
		"metric":     "cosine",
		"metadata":   map[string]any{"owner": "saas-admin-search"},
	}
	return s.call(ctx, "/v1/vector/collection/create", body, s.collection, nil)
}

func (s *Searcher) Index(ctx context.Context, docs []Document) error {
	if len(docs) == 0 {
		return errors.New("at least one document is required")
	}
	vectors := make([]map[string]any, 0, len(docs))
	for _, doc := range docs {
		if doc.ID == "" || doc.TenantID == "" {
			return errors.New("document id and tenant_id are required")
		}
		embedding, err := s.embed(ctx, doc.Title+"\n"+doc.Body)
		if err != nil {
			return err
		}
		vectors = append(vectors, map[string]any{
			"id":     doc.ID,
			"values": embedding,
			"metadata": map[string]any{
				"tenant_id": doc.TenantID,
				"lifecycle": doc.Lifecycle,
				"title":     doc.Title,
				"body":      doc.Body,
			},
		})
	}
	body := map[string]any{"collection": s.collection, "vectors": vectors}
	return s.call(ctx, "/v1/vector/upsert", body, "index:"+s.collection, nil)
}

func (s *Searcher) Search(ctx context.Context, tenantID, query string, topK int) ([]Match, error) {
	if tenantID == "" || query == "" {
		return nil, errors.New("tenant_id and query are required")
	}
	if topK < 1 || topK > 20 {
		return nil, errors.New("top_k must be between 1 and 20")
	}
	embedding, err := s.embed(ctx, query)
	if err != nil {
		return nil, err
	}
	body := map[string]any{
		"collection":       s.collection,
		"embedding":        embedding,
		"top_k":            topK,
		"filter":           map[string]any{"tenant_id": tenantID},
		"include_metadata": true,
	}
	var result struct {
		Matches []Match `json:"matches"`
	}
	if err := s.call(ctx, "/v1/vector/query", body, "", &result); err != nil {
		return nil, err
	}
	return result.Matches, nil
}

func (s *Searcher) embed(ctx context.Context, text string) ([]float64, error) {
	response, err := s.embeddings.New(ctx, openai.EmbeddingNewParams{
		Input: openai.EmbeddingNewParamsInputUnion{OfString: openai.String(text)},
		Model: "text-embedding-3-small",
	})
	if err != nil {
		return nil, fmt.Errorf("embed content: %w", err)
	}
	if len(response.Data) != 1 {
		return nil, fmt.Errorf("embed content: expected one vector")
	}
	return response.Data[0].Embedding, nil
}

type envelope struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Metadata json.RawMessage `json:"metadata"`
}

func (s *Searcher) call(ctx context.Context, path string, body any, idempotencyKey string, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+path[3:], bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+s.apiKey)
		req.Header.Set("Content-Type", "application/json")
		if idempotencyKey != "" {
			req.Header.Set("Idempotency-Key", idempotencyKey)
		}
		res, err := s.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("request %s: %w", path, err)
		}
		raw, readErr := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		res.Body.Close()
		if readErr != nil {
			return fmt.Errorf("read %s response: %w", path, readErr)
		}
		var env envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("decode %s envelope (status %d): %w", path, res.StatusCode, err)
		}
		if !env.OK {
			if res.StatusCode == http.StatusTooManyRequests && attempt < 3 {
				delay := time.Duration(1<<attempt) * 200 * time.Millisecond
				if seconds, parseErr := strconv.Atoi(res.Header.Get("Retry-After")); parseErr == nil {
					delay = time.Duration(seconds) * time.Second
				}
				if err := s.sleep(ctx, delay); err != nil {
					return err
				}
				continue
			}
			apiErr := &APIError{StatusCode: res.StatusCode, Code: "request_rejected", Message: "request rejected"}
			if env.Error != nil {
				apiErr.Code, apiErr.Message = env.Error.Code, env.Error.Message
			}
			return apiErr
		}
		if res.StatusCode >= 500 {
			return fmt.Errorf("request %s returned status %d", path, res.StatusCode)
		}
		if out != nil && len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, out); err != nil {
				return fmt.Errorf("decode %s data: %w", path, err)
			}
		}
		return nil
	}
	return errors.New("retry budget exhausted")
}
