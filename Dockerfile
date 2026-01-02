FROM golang:1.26-alpine3.23 AS builder

WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-w -s" -o /advisor ./cmd/advisor/...

FROM alpine:3.23

RUN apk add --no-cache ca-certificates tzdata
ENV TZ=Asia/Seoul

WORKDIR /app
COPY --from=builder /advisor ./advisor

ENTRYPOINT ["./advisor"]
