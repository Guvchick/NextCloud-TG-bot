FROM golang:1.22-bookworm AS build

WORKDIR /src
COPY dbservice/go.mod ./
COPY dbservice/ ./
RUN go mod tidy && go mod download
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/bot-db .

FROM debian:bookworm-slim

WORKDIR /app
COPY --from=build /out/bot-db /app/bot-db

ENV DB_LISTEN_ADDR=:8080
ENV POSTGRES_HOST=postgres
ENV POSTGRES_PORT=5432
ENV POSTGRES_DB=bot
ENV POSTGRES_USER=bot
ENV POSTGRES_SSLMODE=disable

CMD ["/app/bot-db"]
