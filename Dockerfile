# Build stage
FROM golang:1.24-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make protobuf-dev

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o export-server cmd/server/main.go

# Runtime stage
FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy binary from builder
COPY --from=builder /app/export-server .

# Copy config example
COPY --from=builder /app/config.example.yaml ./config.example.yaml

# Create temp directory
RUN mkdir -p /tmp/export-middleware

# Expose ports
EXPOSE 9090 9091 8080

# Run the server
CMD ["./export-server"]
