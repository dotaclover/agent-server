# AGENTS.md

This repository is a compact learning/demo project for Go Agent patterns.

## Product Intent

- Default user-facing experience is the Customer Dify product documentation Q&A at `/`.
- Operator and Admin are intentional hidden entries at `/operator.html` and `/admin.html`.
- Keep the project small and readable. Prefer removing unused knobs over preserving platform-like configuration.
- External RAG is intentionally outside this repo. Customer uses `CUSTOMER_RAG_API_ENDPOINT` through the `search_product_docs` tool.
- MCP default behavior is intentional; do not change `mcp.json` defaults unless the user asks.

## Current Architecture

- `cmd/commands/serve.go` wires the app, tools, LLM provider, SQLite, trace recorder, MCP, and routes.
- `server/router/router.go` owns HTTP routes, auth, health checks, rate limiting, and admin APIs.
- Customer path:
  - `modules/customer/handler.go`
  - `services/simplechat/chat.go`
  - `services/tools/customer/registry.go`
- Operator path:
  - `modules/operator/handler.go`
  - `services/agentcore/agent.go`
  - `services/orchestrator/*`
  - `services/tools/operator/registry.go`
- Admin path:
  - `server/router/router.go`
  - `modules/mcp/handler.go`
  - `services/tools/admin/registry.go`

## Configuration Policy

Only keep configuration that has a current runtime effect.

Do not reintroduce these unless implementing the actual feature:

- Local RAG index config such as `RAG_INDEX_PATH`, `RAG_TOP_K`, `RAG_MIN_SCORE`
- Embedding config such as `AI_EMBEDDING_*`
- Image/video generation config such as `AI_IMAGE_*`, `AI_VIDEO_*`
- Operator search config such as `OPERATOR_SEARCH_API_ENDPOINT`
- Operator tool-step env config such as `OPERATOR_MAX_TOOL_STEPS`
- Customer-specific rate config such as `CUSTOMER_RATE_LIMIT_RPS`
- Public RAG switch config such as `APP_PUBLIC_RAG_SEARCH_ENABLED`

`MaxToolStepsPerRun` is intentionally hardcoded in `serve.go` for the demo.

## Documentation Policy

- Keep `README.md` for humans.
- Keep this file for AI/code agents.
- Avoid adding broad planning or migration documents at repo root.
- If temporary design notes are needed, prefer short issue/PR text rather than persistent root docs.

## Verification

Before finishing code changes, run:

```powershell
gofmt -l .
go build ./...
```

Run `go vet ./...` when touching shared code, routing, config, or persistence.

## Existing Worktree Caution

The worktree may contain unrelated local changes. Do not revert or stage unrelated files unless the user asks.
