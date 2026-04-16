.PHONY: dev dev-front dev-back build stop logs deploy

# ── Local development ──────────────────────────────────────────────────────
dev:
	docker compose up --build

stop:
	docker compose down

logs:
	docker compose logs -f

# Run backend locally (port 8080)
dev-back:
	@set -a; source .env; set +a; \
	cd backend && go run ./cmd/advisor/...

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
