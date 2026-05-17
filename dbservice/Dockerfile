FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY dbservice/go.mod ./
RUN go mod download
COPY dbservice/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot-db .

FROM debian:bookworm-slim

WORKDIR /app
COPY --from=build /out/bot-db /app/bot-db
RUN mkdir -p /app/data && chmod 700 /app/data

ENV DB_LISTEN_ADDR=:8080
ENV DATABASE_PATH=/app/data/bot.sqlite3

CMD ["/app/bot-db"]
