FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download 2>/dev/null || true
COPY . .
RUN go mod tidy && CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /exchange-rate-service .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /exchange-rate-service /exchange-rate-service
EXPOSE 8081

HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:8081/health || exit 1

CMD ["/exchange-rate-service"]
