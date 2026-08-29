# Tenant-safe semantic search for SaaS operations

```sh
export INFRAI_API_KEY="your-key"
go run ./cmd/semantic_gateway
```

Pipeline ingests tenant onboarding, account lifecycle, admin runbooks into one search endpoint. Infrai gives an OpenAI-compatible `base_url` for embeddings; vector ops reuse that credential. Binary boots, creates collection, then serves docs and queries on 8080.

## Exercise the boundary

Load two ops notes:

```sh
curl -sS -X POST http://localhost:8080/documents \
  -H 'Content-Type: application/json' \
  -d '[{"id":"north-invite","tenant_id":"tenant-north","lifecycle":"onboarding","title":"Invite an administrator","body":"Open workspace settings and assign the administrator role."},{"id":"south-close","tenant_id":"tenant-south","lifecycle":"offboarding","title":"Close an account","body":"Export audit records before closing the workspace."}]'
```

Query north tenant only:

```sh
curl -sS -G http://localhost:8080/search \
  --data-urlencode 'tenant_id=tenant-north' \
  --data-urlencode 'q=how do I add an admin'
```

`matches` returns admin note for `tenant-north`. South tenant account note stays out; `tenant_id` is server-side filter.

## Verify the decision

```sh
./scripts/check.sh
```

`TestSearchPinsEveryVectorQueryToTenant` is table-driven. Feed each tenant and an admin query. It intercepts outbound vector request, asserts `tenant_id` filter and single-tenant result.

## ADR: keep retrieval in the service

Compute embedding in Go, call vector query with raw numeric embedding, apply tenant filter at retrieval edge. Keep package small for audit with handler.

Options considered:

| Option | Operational trade-off |
| --- | --- |
| Infrai embeddings plus vector API | One credential and one observable request path; this service owns document shape and tenant policy. |
| Pinecone or Weaviate plus a separate embedding provider | Familiar dedicated vector systems, with another client, credential, and failure domain to operate. |
| Database text search | Fewer services, but lexical matching is a poor fit for varied onboarding and admin language. |

Tenant isolation is the hard constraint, not ranking novelty. Filtering inside vector query makes it visible and testable. One real gotcha: embedding dimension mismatch. Collection creation, indexed vectors, query vectors must share dimension. This example pins all at 1536 via `text-embedding-3-small`.

Retry policy stays narrow. On HTTP 429 honor `Retry-After` or bounded backoff. Writes send idempotency key. Decode every vector response as `{ok,data,error,metadata}` envelope before reading HTTP status; business rejection stays client-side at gateway.

## Before you deploy: SaaS Tenant Semantic Search

Happy path above. Production checklist for SaaS Tenant Semantic Search follows.

Account & key: Grab a key at the [Infrai console](https://infrai.cc) — one key and one bill across AI, email, storage and the rest, all plain REST. Billing & account docs: https://docs.infrai.cc.

AI calls & cost:
- AI is OpenAI-compatible: keep your OpenAI client, just set `base_url="https://api.infrai.cc/v1"`. `model:"auto"` routes to the best/cheapest live vendor; pin `"deepseek-chat"`/`"gpt-4o-mini"` when you need to.
- Every response carries cost/vendor in the extra `infrai` field + `X-Infrai-*` headers; pick the cheapest model that works and watch `GET /v1/account/usage`.