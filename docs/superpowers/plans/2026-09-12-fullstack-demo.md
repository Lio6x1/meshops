# Fullstack Demo Implementation Plan

> **For agentic workers:** Use superpowers:executing-plans with independent agents for bounded frontend and evidence work. Check boxes record actual verification, not intent.

**Goal:** Deliver a runnable full-stack reference plus Docker and local learning instructions.
**Architecture:** Vue calls a session-protected HTTP gateway. Generated unary RPC adapters and a bounded SSE adapter reuse the existing Go services. Isolated Compose includes persistent middleware, initialization, simulators and frontend.
**Tech Stack:** Go 1.25.10, grpc-gateway v2.27.7, Vue 3, TypeScript, Vite, Element Plus, Docker Compose.
**Spec:** [approved scope and interface contract](../specs/2026-09-12-fullstack-demo-design.md)

## Global constraints

- Six entity types, four inspect executors, task-only search; no physical device control or production HA claims.
- Original machine credentials stay on the server. HTTP-generated contracts never expose ReportTaskStatus or source ingestion.
- Main directory holds reference code; learning code belongs in sibling meshops-course-lab.
- Preserve baseline course answers and data volumes. No deletion before evidence and path checks.

## 1. Evidence archive and hygiene

Files: docs/review/2026-09-12/evidence/*; README.md; .gitignore.
- [x] Convert raw test JSONL to per-test results with no Output fields; record original SHA256 and coverage, validate 145 pass/3 helper skips/19 required cases.
- [x] Archive course result and raw-source checksum catalog next to existing runtime/CI evidence.
- [x] Preserve remaining selected raw evidence outside cache; verify hashes before removing temporary copies and old worktree with Git.
- [x] Record before/after space with reparse points excluded, update README ports and student entry.

## 2. HTTP boundary

Files: proto/http.yaml; scripts/generate-http.ps1; gen/*/*.pb.gw.go; internal/web/{server,session,stream,server_test}.go; cmd/web-gateway/main.go.
Interface: New(Config) (*Server,error), Server.Handler() http.Handler, Config contains typed Entity/Task/Dispatcher/Search clients, trusted registry, role access codes and allowed origins.
- [x] Add tests issuing GET /api/v1/tasks without cookie and POST /api/session with hostile Origin; expect 401/403 before any upstream call. Run go test ./internal/web -count=1 and capture red.
- [x] Generate external HTTP mappings only for spec routes; implement bounded sessions, CSRF and trusted role-to-RPC credential mapping.
- [x] Use real loopback fake gRPC services to assert JSON conversion, caller principal, admin denial, tenant inventory isolation and unexposed report route.
- [x] Add SSE read/write bounds and context cancellation tests, then ordinary/race tests for this package.

## 3. Real frontend

Files: web/package.json and lock; web/src/{api,domain,stores,views,components}; web/vite.config.ts; web/tests.
Interface: exact HTTP table and protobuf JSON described in spec. Frontend credentials=include, write requests X-CSRF-Token, no upstream token storage.
- [x] Write domain tests for unknown power, int64 comparison, sync boundary and cancellation display; execute red.
- [x] Implement six navigation pages, session login/logout and real API calls; preserve uncertain creation idempotency key and expire PIT cursor visibly.
- [x] Build with TypeScript checks and production bundling; inspect real browser connected to gateway.

## 4. Container network and complete runtime

Files: internal/platform/config.go/auth.go; Dockerfile; compose.demo.yml; deploy/demo/*; scripts/demo-stack.ps1; initialization command.
- [x] Test local-only default remains enforced; add explicit isolated-demo network policy with fixed configured peers, reject URL/resolver injection.
- [x] Build Linux backend and frontend images, create dedicated volumes and random secrets; migrate and seed before initializing search.
- [x] Configure internal Kafka listener and persisted Canal starting boundary; restart reuses existing markers and credentials.
- [x] Start full stack, assert six snapshots/four tasks/search and restart preservation. Test error messages when a dependency is unavailable.

## 5. Complete teaching and delivery

Files: docs/learning/from-zero/lessons/z11.md onward; stage/file/substep publishing; docs/run-fullstack.md; README.md; CI.
- [x] Provide complete gateway, frontend and deployment reference files with numbered explanations and commands from the student directory.
- [x] Document first run, access codes, browser URL, start/stop, logs, data reset and IDE+Vite workflow.
- [x] Exercise fresh copy and clean Compose deployment; link actual evidence and code SHA.
- [x] Review changed backend/HTTP security and browser behavior independently; run appropriate integration/race/proto/copy checks.
- [x] Set project topics and optimize pending scans with regression evidence.
- [ ] License selection remains pending the owner's MIT / Apache-2.0 / no-license decision; no license grant has been invented.
- [x] Integrate committed reference, confirm Git/CI and deliver complete user-facing startup entry.

## Verification notes

Actual runtime evidence: [fullstack acceptance](../../verification/2026-09-12-fullstack/report.md). Docker first-up needed a frontend bridge and host probes before the browser could connect. Historical failed Linux container integration is retained separately from successful Windows real-dependency acceptance and ordinary Linux race. Code commit 28fad46 passed both cloud CI jobs, including real-dependency Linux race; see the fullstack release receipt.
