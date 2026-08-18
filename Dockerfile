# Stage 1: build
FROM --platform=$BUILDPLATFORM golang:1.26.6-alpine AS builder

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src

# Download dependencies first for layer caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source and templates
COPY main.go ./
COPY templates/ ./templates/

# Build a fully static binary
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -trimpath -ldflags="-s -w" -o /firescan .

# Stage 2: distroless runtime image
FROM gcr.io/distroless/static-debian13:nonroot

# Copy the binary and templates
COPY --from=builder /firescan /firescan
COPY --from=builder /src/templates /templates

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/firescan"]
