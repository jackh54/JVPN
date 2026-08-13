# syntax=docker/dockerfile:1

FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/jvpn-server ./cmd/jvpn-server

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iptables iproute2 \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/jvpn-server /usr/local/bin/jvpn-server
EXPOSE 443
ENTRYPOINT ["/usr/local/bin/jvpn-server"]
