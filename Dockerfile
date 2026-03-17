FROM golang:1.25-alpine AS builder

WORKDIR /app

RUN apk add --no-cache ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /usr/local/bin/dns-mux-lite .

FROM alpine:3.19

RUN apk add --no-cache ca-certificates && update-ca-certificates

RUN adduser -D -u 10001 dnsuser
USER dnsuser

COPY --from=builder /usr/local/bin/dns-mux-lite /usr/local/bin/dns-mux-lite

EXPOSE 53/udp

ENTRYPOINT ["/usr/local/bin/dns-mux-lite"]

