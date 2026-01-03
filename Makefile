.PHONY: local-notify-test local-advisor-run local-advisor-test local-test advisor advisor-test docker-build docker-run docker-test

local-notify-test:
	@set -a; source .env; set +a; \
	go run ./cmd/notify/...

local-advisor-test:
	@set -a; source .env; set +a; \
	ADVISOR_RUN_ONCE=true go run ./cmd/advisor/...

local-test: local-notify-test

advisor-test: local-advisor-test

docker-build:
	docker build -t midas-touch .

docker-run: docker-build
	mkdir -p logs
	docker run --env-file .env -e ADVISOR_LOG_FILE=/app/logs/advisor.log -v "$(PWD)/logs:/app/logs" midas-touch

docker-test: docker-build
	mkdir -p logs
	docker run --env-file .env -e ADVISOR_RUN_ONCE=true -e ADVISOR_LOG_FILE=/app/logs/advisor.log -v "$(PWD)/logs:/app/logs" midas-touch
