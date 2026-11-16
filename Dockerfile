# Build a static binary.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/llm_check_bot .

# Minimal runtime: CA certificates for the Telegram/Anthropic APIs, no shell.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/llm_check_bot /app/llm_check_bot
COPY assets /app/assets
ENTRYPOINT ["/app/llm_check_bot"]
