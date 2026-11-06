---
name: add-endpoint-from-contract
description: Steps to add a Go (chi) REST handler that exactly follows docs/API.md and api/openapi.yaml, with tests and a conformance check. Use when an issue asks for a new endpoint.
compatibility: opencode
---

# Add an Endpoint from the Contract

1. **Read the contract first.** Find the endpoint in `docs/API.md` (method, path, auth, request, response, error codes) and in `api/openapi.yaml`. Missing or different → **stop**, create a `contract-change` issue, don't guess.
2. **Route:** register it in the `internal/http` router under `/api/v1`. Auth per the Auth column: `public`, `recruiter` (bearer header only; the run SSE route uses `?sse_token=` instead), or session `token` in the path.
3. **Handler:** decode → validate → call service/store → encode. JSON fields `snake_case`, exactly as in the contract. Times in ISO-8601 UTC.
4. **Errors:** always through the error writer: `{"error":{"code","message","details"}}` with `code` from `docs/API.md`. Never invent new codes.
5. **Store:** add the method to the `internal/store` interface, with pgx **and** in-memory implementations.
6. **Tests** (`httptest`, in-memory store, `LLM_MODE=fake`, `EMAIL_MODE=log`):
   - success path
   - every error code the contract lists for that endpoint
   - auth: no token → 401
   - conformance: response validated against `api/openapi.yaml`
7. **Events:** if the endpoint triggers graph events, persist to `node_events` first, then publish; names & payloads per `docs/EVENTS.md`.
8. **Checks:** `gofmt -l .` empty, `go vet ./...`, `go test ./...`.

Don't: add fields "while you're at it", rename fields, use an ORM, or print secrets to logs.
