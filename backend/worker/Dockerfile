FROM golang:alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /worker ./worker

FROM alpine:latest
WORKDIR /app
RUN apk add --no-cache ca-certificates ffmpeg
COPY --from=builder /worker /app/worker
RUN chmod +x /app/worker
CMD ["./worker"]
