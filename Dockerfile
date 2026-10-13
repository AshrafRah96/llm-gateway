FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build static binary with security flags
# CGO_ENABLED=0: Pure Go, no C dependencies (works in minimal images)
# -ldflags="-s -w": Strip debug info and symbol table (~30% smaller)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o llm-gateway .

FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3
COPY --from=builder /build/llm-gateway /llm-gateway
EXPOSE 8080
USER nonroot:nonroot

# Start the application
ENTRYPOINT ["/llm-gateway"]
