.PHONY: help test advisor advisor-once advisor-test docker-build docker-run docker-test

help:
	@echo "Available targets:"
	@echo "  make test          - send one Telegram test notification"
	@echo "  make advisor       - run scheduled analysis alerts (KST: 0/4/8/12/16/20h)"
	@echo "  make advisor-test  - force-send one analysis alert now (test)"
	@echo "  make docker-build  - build Docker image (midas-touch)"
	@echo "  make docker-run    - run advisor in Docker (scheduled)"
	@echo "  make docker-test   - run advisor in Docker once (test)"

test:
	@set -a; source .env; set +a; \
	go run ./cmd/notify/...

advisor:
	@set -a; source .env; set +a; \
	go run ./cmd/advisor/...

advisor-test:
	@set -a; source .env; set +a; \
	ADVISOR_RUN_ONCE=true go run ./cmd/advisor/...

docker-build:
	docker build -t midas-touch .

docker-run: docker-build
	docker run --env-file .env midas-touch

docker-test: docker-build
	docker run --env-file .env -e ADVISOR_RUN_ONCE=true midas-touch
