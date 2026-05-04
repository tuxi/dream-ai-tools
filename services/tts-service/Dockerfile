FROM golang:1.24-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o tts-service ./cmd/server

FROM alpine:3.19

RUN apk add --no-cache tzdata ca-certificates

WORKDIR /app

COPY --from=builder /app/tts-service .
COPY config.example.yaml ./config.example.yaml

ENV CONFIG_PATH=/app/config.yaml
ENV TZ=Asia/Shanghai

EXPOSE 8088

CMD ["./tts-service"]
