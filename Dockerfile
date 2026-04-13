ARG GO_IMAGE=golang:latest
ARG BUILDPLATFORM=linux/amd64

FROM --platform=${BUILDPLATFORM} ${GO_IMAGE} AS builder

ARG TARGETOS=linux
ARG TARGETARCH=amd64

WORKDIR /src

COPY go.mod go.sum ./
COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/warp-socksd ./cmd/warp-socksd

FROM alpine:3.23 AS runtime-deps

RUN apk add --no-cache \
    ca-certificates \
    iproute2-minimal \
    wireguard-tools-wg

RUN mkdir -p /rootfs/app /rootfs/etc/ssl/certs /rootfs/etc /rootfs/sbin /rootfs/usr/bin /rootfs/usr/share /rootfs/lib /rootfs/usr/lib \
    && mkdir -m 1777 -p /rootfs/tmp \
    && cp /etc/ssl/certs/ca-certificates.crt /rootfs/etc/ssl/certs/ca-certificates.crt \
    && cp -r /usr/share/iproute2 /rootfs/usr/share/iproute2 \
    && for bin in /sbin/ip /usr/bin/wg; do \
        mkdir -p "/rootfs$(dirname "$bin")"; \
        cp "$bin" "/rootfs$bin"; \
        ldd "$bin" | awk '/=>/ {print $3} /^\// {print $1}' | sort -u | while read -r lib; do \
          [ -n "$lib" ] || continue; \
          mkdir -p "/rootfs$(dirname "$lib")"; \
          cp "$lib" "/rootfs$lib"; \
        done; \
      done

FROM scratch

COPY --from=runtime-deps /rootfs/ /
COPY --from=builder /out/warp-socksd /app/warp-socksd

ENV PATH=/usr/bin:/sbin:/app \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt \
    TMPDIR=/tmp \
    WARP_BACKEND=legacy

WORKDIR /app

EXPOSE 1080/tcp
EXPOSE 1080/udp

ENTRYPOINT ["/app/warp-socksd"]
