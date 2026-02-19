# Stage 1: build
FROM golang:1.26.0-alpine AS builder

WORKDIR /src

# Download dependencies first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and templates
COPY main.go ./
COPY templates/ ./templates/

# Build a fully static binary
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /firescan .

# Stage 2: minimal runtime image
FROM scratch

# Copy CA certificates so TLS calls to GCP APIs work
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# Copy the binary and templates
COPY --from=builder /firescan /firescan
COPY --from=builder /src/templates /templates

EXPOSE 8080

ENTRYPOINT ["/firescan"]
