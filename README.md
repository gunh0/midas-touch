# midas-touch

Midas Touch is a Go + Gin backend and Next.js frontend for multi-symbol signal analysis, watchlist-based alerting, and Telegram delivery.

## Core Features

- Market-cap priority list + custom universe management
- Watchlist alert modes: `event` and `interval`
- Multi-timeframe signal outputs (direction + timing)
- Telegram notification pipeline
- System monitor (data-source health, DB usage)
- View history persistence

## Architecture (Simple)

- `backend/`
	- `internal/api`: HTTP handlers and routes
	- `internal/service`: signal orchestration and caching
	- `internal/advisor`: indicator scoring and recommendation logic
	- `internal/mongodb`: persistence layer
	- `internal/marketdata`: external market data adapters
- `frontend/`
	- Next.js app UI (`app/page.tsx` + modular dashboard components)

## Run

### Local backend

```bash
cd backend
go run .
```

### Local frontend

```bash
cd frontend
npm install
npm run dev
```

### Docker compose

```bash
docker compose up --build
```

## Required Environment

Key variables used by backend:

```env
TELEGRAM_BOT_TOKEN=...
TELEGRAM_CHAT_ID=...
FINNHUB_API_KEY=...
MONGODB_URI=...
API_PORT=8000
```

Optional performance-related variables:

```env
MONITOR_MAX_WORKERS=4
POPULAR_SCAN_MAX_WORKERS=3
```

## Main APIs

- `GET /api/signal?symbol=NVDA&timing_tf=120`
- `POST /api/signals/batch?timing_tf=120` body: `{ "symbols": ["NVDA", "TSLA"] }`
- `GET /api/watchlist`
- `POST /api/watchlist?symbol=NVDA&notify_interval_minutes=5&notify_mode=event`
- `POST /api/watchlist/pin?symbol=NVDA&pinned=true`
- `GET /api/universe`
- `POST /api/universe?symbol=NVDA&kind=base|custom`

## Performance Notes

- In-memory signal TTL cache (short-lived)
- Batch signal endpoint to reduce N HTTP calls to 1
- Worker pools in monitor loops to cap concurrency
- Mongo indexes are ensured at startup

## Testing

```bash
cd backend
go test ./...

cd ../frontend
npm run build
```

Backend test organization:

- Unit tests: colocated with source files (`*_test.go`)
- Integration/E2E tests: `backend/tests/`
- Test fixtures: `backend/tests/fixtures/`

## Language Policy

- Default language for product UX and logs: Korean
- English is allowed in Telegram message content where useful
