# Backend Architecture

## Why Keep internal/

We intentionally keep most backend code under internal/.

Reasons:
- Enforces module boundaries: packages under internal/ cannot be imported from outside this module.
- Prevents accidental public API surface growth.
- Improves refactor safety for domain, service, and adapter layers.
- Fits clean architecture in Go: entrypoint in main.go, business logic in internal/.

## Layering

- main.go: composition root and runtime orchestration (API server + scheduler loop).
- internal/api: HTTP adapter (Gin handlers, request/response mapping, Swagger routes).
- internal/service: application use-cases (signal evaluation workflow, pruning policy).
- internal/advisor: pure domain calculations (indicators, scoring, decision model).
- internal/marketdata: outbound market data integrations.
- internal/mongodb: persistence adapter.
- internal/telegram: outbound notification adapter.

## Current Policy

- Keep internal/ for all non-public backend packages.
- Expose only the executable entrypoint at backend/main.go.
- Add new features through service layer first, then wire through adapters.
