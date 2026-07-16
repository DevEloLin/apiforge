# syntax=docker/dockerfile:1

# ---- build: static, CGO-free binary ----------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache modules first.
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
# Static binary: no libc, trimmed symbols, reproducible. TARGETARCH is set by
# buildx (arm64 for the Pi); defaults to the build host otherwise.
ARG TARGETOS=linux
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/apiforge ./cmd/apiforge

# ---- runtime: scratch (nothing but the binary + CA certs) ------------------
FROM scratch
# TLS to upstreams needs root CAs; tzdata optional (UTC is fine).
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /out/apiforge /apiforge

# Cap the Go heap so the runtime GCs early on a 1GB Pi instead of ballooning.
ENV GOMEMLIMIT=64MiB
# Bind all interfaces inside the container; publish to 127.0.0.1 on the host.
ENV HOST=0.0.0.0
ENV PORT=8899
EXPOSE 8899

# Run as a non-root uid (scratch has no /etc/passwd; numeric uid is fine).
USER 65532:65532
ENTRYPOINT ["/apiforge"]
