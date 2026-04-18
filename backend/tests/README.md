# Backend Test Layout

This directory is the central place for backend non-unit tests.

## Policy

- Keep unit tests next to source files (`*_test.go` in each package).
  - Reason: unit tests often need package-private functions and tighter locality.
- Put integration and scenario tests under `backend/tests/`.
- Keep test fixtures under `backend/tests/fixtures/`.

## Folders

- `integration/`: tests across package boundaries (API + service + DB behavior).
- `e2e/`: full flow tests (process-level, optional docker/external deps).
- `fixtures/`: static test data.

## Commands

From repository root:

```bash
make test-back
make test-back-integration
```

From backend folder:

```bash
go test ./...
go test ./tests/...
```
