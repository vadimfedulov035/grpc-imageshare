#-----------------------------------------------------------------------------
# Stage 1: Base
#-----------------------------------------------------------------------------
FROM golang:1.24-alpine AS base

WORKDIR /app

# Install required system packages
RUN apk add --no-cache \
	git \
	build-base \
	upx \
	ca-certificates \
	tzdata

#-----------------------------------------------------------------------------
# Stage 2: Builder
#-----------------------------------------------------------------------------
# Start from base
FROM base AS builder

# Download dependencies
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Copy certs (for test run / final copy)
COPY certs ./certs

# Run tests
RUN go test -v -failfast ./...

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w -extldflags '-static'" \
    -o /app/server ./cmd/server/main.go

# Compress the binary
RUN upx --best --lzma /app/server

#-----------------------------------------------------------------------------
# Stage 3: PreRuntime
#-----------------------------------------------------------------------------
# Start from base
FROM base AS runtime_prep

# Create runtime directory structure
RUN mkdir -p /runtime/app/uploads \
             /runtime/app/certs \
             /runtime/etc/ssl/certs \
             /runtime/usr/share/zoneinfo

# Copy certificates and timezone data
COPY --from=base /etc/ssl/certs/ca-certificates.crt /runtime/etc/ssl/certs/
COPY --from=base /usr/share/zoneinfo /runtime/usr/share/zoneinfo

# Copy compiled binary
COPY --from=builder /app/server /runtime/app/server

# Copy app certificates
COPY --from=builder /app/certs /runtime/app/certs

# Set ownership for runtime directory structure
RUN chown -R 65534:65534 /runtime

#-----------------------------------------------------------------------------
# Stage 4: Runtime
#-----------------------------------------------------------------------------
# Start from scratch
FROM scratch

# Copy full structure from preruntime stage
COPY --from=runtime_prep /runtime /

# Runtime configuration
WORKDIR /app
ENV TZ=UTC
EXPOSE 50051
USER 65534:65534

# Entrypoint
ENTRYPOINT ["/app/server"]

# Arguments
CMD ["--port=50051", "--storage=/app/uploads", "--log-format=json", "--log-level=info", "--tls-cert-file=/app/certs/server.crt", "--tls-key-file=/app/certs/server.key"]
