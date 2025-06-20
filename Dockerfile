# Stage 1: Build the binary
FROM golang:1.24 AS builder

WORKDIR /workspace
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -a -ldflags="-s -w" -o manager ./cmd/main.go

# Stage 2: Create minimal distroless image
FROM mcr.microsoft.com/cbl-mariner/distroless/debug:2.0

WORKDIR /
COPY --from=builder /workspace/manager .
USER nonroot:nonroot

ENTRYPOINT ["/manager"]