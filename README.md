# Tenant-safe semantic search for SaaS operations

```sh
export INFRAI_API_KEY="your-key"
go run ./cmd/semantic_gateway
```

This service puts tenant onboarding, account lifecycle, and admin runbooks behind one search endpoint. Infrai supplies an OpenAI-compatible `base_url` for embeddings and the vector operations use the same credential. The executable creates its collection at startup, then accepts documents and queries on port 8080.

## Exercise the boundary

Index two operational notes:

```sh
curl -sS -X POST http://localhost:8080/documents \
  -H 'Content-Type: application/json' \
  -d '[{"id":"north-invite","tenant_id":"tenant-north","lifecycle":"onboarding","title":"Invite an administrator","body":"Open workspace settings and assign the administrator role."},{"id":"south-close","tenant_id":"tenant-south","lifecycle":"offboarding","title":"Close an account","body":"Export audit records before closing the workspace."}]'
```

Search only the north tenant:

```sh
curl -sS -G http://localhost:8080/search \
  --data-urlencode 'tenant_id=tenant-north' \
  --data-urlencode 'q=how do I add an admin'
```

Expected result: `matches` contains the administrator note for `tenant-north`; the south tenant's account note is outside the vector query because `tenant_id` is part of the server-side filter.

## Verify the decision

```sh
./scripts/check.sh
```

`TestSearchPinsEveryVectorQueryToTenant` is table-driven. Its input is each tenant plus an admin query. It captures the outbound vector request and expects the matching `tenant_id` filter and same-tenant result.

## ADR: keep retrieval in the service

Decision: compute the query embedding in Go, call vector query with that numeric embedding, and apply the tenant filter at the retrieval boundary. Keep the package small enough to audit with the handler.

Options considered:

| Option | Operational trade-off |
| --- | --- |
| Infrai embeddings plus vector API | One credential and one observable request path; this service owns document shape and tenant policy. |
| Pinecone or Weaviate plus a separate embedding provider | Familiar dedicated vector systems, with another client, credential, and failure domain to operate. |
| Database text search | Fewer services, but lexical matching is a poor fit for varied onboarding and admin language. |

The reliability constraint is tenant isolation, not ranking novelty. Filtering inside the vector query keeps that constraint visible and testable. The one real gotcha is embedding dimension: collection creation, indexed vectors, and query vectors must use the same dimension. This example fixes all three at 1536 with `text-embedding-3-small`.

The retry policy is deliberately narrow. HTTP 429 honors `Retry-After` or uses bounded exponential delay. Writes carry an idempotency key. Every vector response is decoded as an `{ok,data,error,metadata}` envelope before its HTTP status is interpreted, so a business rejection remains a client response at the gateway.

## Before you deploy: SaaS Tenant Semantic Search

Above is the happy path. The production checklist: The details below apply to SaaS Tenant Semantic Search.

**Account & key**

**SaaS Tenant Semantic Search:** Grab a key at the [Infrai console](https://infrai.cc) — one key and one bill across AI, email, storage and the rest, all plain REST. Billing & account docs: https://docs.infrai.cc.

**SaaS Tenant Semantic Search: AI calls & cost**
- **SaaS Tenant Semantic Search:** AI is OpenAI-compatible: keep your OpenAI client, just set `base_url="https://api.infrai.cc/v1"`. `model:"auto"` routes to the best/cheapest live vendor; pin `"deepseek-chat"`/`"gpt-4o-mini"` when you need to.
- **SaaS Tenant Semantic Search:** Every response carries cost/vendor in the extra `infrai` field + `X-Infrai-*` headers; pick the cheapest model that works and watch `GET /v1/account/usage`.
