FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .

# Build static binary with security flags
# CGO_ENABLED=0: Pure Go, no C dependencies (works in minimal images)
# -ldflags="-s -w": Strip debug info and symbol table (~30% smaller)
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o llm-gateway .

FROM gcr.io/distroless/static:nonroot
COPY --from=builder /build/llm-gateway /llm-gateway
EXPOSE 8080
USER nonroot:nonroot

# Start the application
ENTRYPOINT ["/llm-gateway"]
