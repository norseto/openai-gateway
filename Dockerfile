# Build stage
FROM golang:1.24.2-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o gateway ./cmd/openai-gateway

# Run stage
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/gateway .
CMD ["./gateway", "--open-webui-url=http://open-webui.default.svc.cluster.local/api", "--port=8080"]
