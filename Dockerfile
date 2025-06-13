# ---------- Stage 1: Build ----------
FROM golang:1.24 AS builder

WORKDIR /workspace

# Cache go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -a -o balancerx ./cmd/main.go

# ---------- Stage 2: Run ----------
FROM mcr.microsoft.com/cbl-mariner/distroless/base:2.0

WORKDIR /

COPY --from=builder /workspace/balancerx .

# No user in distroless, just run it — best if binary is not privileged.
ENTRYPOINT ["/balancerx"]
