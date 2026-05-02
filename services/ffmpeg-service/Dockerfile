FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o ffmpeg-service ./cmd/server

FROM alpine:3.19

RUN apk add --no-cache tzdata ca-certificates ffmpeg

WORKDIR /app

COPY --from=builder /app/ffmpeg-service .
COPY config.example.yaml ./config.example.yaml

ENV CONFIG_PATH=/app/config.yaml
ENV TZ=Asia/Shanghai

EXPOSE 8089

CMD ["./ffmpeg-service"]
