.PHONY: help test advisor advisor-once advisor-test

help:
	@echo "Available targets:"
	@echo "  make test  - send one Telegram test notification"
	@echo "  make advisor - run hourly trading decision alerts"
	@echo "  make advisor-once - send one trading decision alert"
	@echo "  make advisor-test - force-send one analysis alert now (test)"

test:
	@set -a; source .env; set +a; \
	go run ./cmd/notify/...

advisor:
	@set -a; source .env; set +a; \
	go run ./cmd/advisor/...

advisor-once:
	@set -a; source .env; set +a; \
	ADVISOR_RUN_ONCE=true go run ./cmd/advisor/...

advisor-test:
	@set -a; source .env; set +a; \
	ADVISOR_RUN_ONCE=true go run ./cmd/advisor/...
