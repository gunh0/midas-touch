.PHONY: up down dev-front dev-back dev-back-run swagger build logs deploy kill-port-8000 test-back test-back-integration

# ── Local development ──────────────────────────────────────────────────────
up:
	docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f

# Run backend locally (port 8000)
dev-back:
	@$(MAKE) kill-port-8000
	@set -a; source .env; set +a; export API_PORT=8000; \
	cd backend && \
	(command -v air >/dev/null 2>&1 || go install github.com/air-verse/air@latest) && \
	(command -v swag >/dev/null 2>&1 || go install github.com/swaggo/swag/cmd/swag@latest) && \
	SWAG_BIN=$$(command -v swag || echo "$$(go env GOPATH)/bin/swag") && \
	AIR_BIN=$$(command -v air || echo "$$(go env GOPATH)/bin/air") && \
	$$SWAG_BIN init -g main.go -o docs --parseInternal --outputTypes json,yaml && \
	$$AIR_BIN -c .air.toml

# Run backend once without hot reload
dev-back-run:
	@$(MAKE) kill-port-8000
	@set -a; source .env; set +a; export API_PORT=8000; \
	cd backend && go run .

kill-port-8000:
	@PIDS=$$(lsof -ti tcp:8000); \
	if [ -n "$$PIDS" ]; then \
		echo "Killing processes on port 8000: $$PIDS"; \
		kill -9 $$PIDS; \
	else \
		echo "No process is using port 8000"; \
	fi

swagger:
	cd backend && $$(command -v swag || echo "$$(go env GOPATH)/bin/swag") init -g main.go -o docs --parseInternal --outputTypes json,yaml

# Run frontend locally (port 3000)
dev-front:
	npm run dev --prefix frontend

# ── Docker images ──────────────────────────────────────────────────────────
build:
	docker compose build

# ── Deploy (run this on the remote server after git clone) ─────────────────
deploy:
	git fetch origin
	git pull origin main
	docker compose up -d --build

# ── Tests ─────────────────────────────────────────────────────────────────
test-back:
	cd backend && go test ./...

test-back-integration:
	cd backend && go test ./tests/...
