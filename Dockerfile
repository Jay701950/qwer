FROM golang:1.23-bookworm AS build
WORKDIR /src
COPY . .
RUN go mod tidy && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends chromium ca-certificates fonts-liberation && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 10001 app
USER app
WORKDIR /app
COPY --from=build /server /app/server
RUN mkdir -p /app/profiles
EXPOSE 8080
ENV LISTEN_ADDR=:8080
ENV PROFILE_DIR=/app/profiles
ENTRYPOINT ["/app/server"]
