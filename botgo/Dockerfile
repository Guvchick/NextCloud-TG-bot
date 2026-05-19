FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY botgo/go.mod ./
COPY botgo/go.sum ./
COPY botgo/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/telegram-nextcloud-bot .

FROM debian:bookworm-slim

WORKDIR /app
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/telegram-nextcloud-bot /usr/local/bin/telegram-nextcloud-bot

CMD ["telegram-nextcloud-bot"]
