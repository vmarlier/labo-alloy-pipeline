# Build Stage
FROM golang:1.26.2-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o producer main.go

# Runtime Stage
FROM gcr.io/distroless/static-debian12

WORKDIR /
COPY --from=builder /app/producer /producer

USER nonroot:nonroot

ENTRYPOINT ["/producer"]
