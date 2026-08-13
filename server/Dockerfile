# syntax=docker/dockerfile:1

FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ENV CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64
# Output into WORKDIR so the parent directory always exists.
RUN go version && go build -trimpath -ldflags=-s\ -w -o jvpn-server ./cmd/jvpn-server

FROM debian:bookworm-slim
RUN apt-get update \
  && apt-get install -y --no-install-recommends ca-certificates curl iptables iproute2 \
  && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/jvpn-server /usr/local/bin/jvpn-server
EXPOSE 443
ENTRYPOINT ["/usr/local/bin/jvpn-server"]
