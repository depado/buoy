FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:b8c9b82affbbb82a020bbcf998dad2ba9516944ac7e883524642a481a9505e9c AS builder

RUN apk add --no-cache make git tzdata unzip

ARG TARGETOS
ARG TARGETARCH
ARG RESTIC_VERSION=0.19.1
RUN wget -q "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" && \
    bunzip2 "restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" && \
    mv "restic_${RESTIC_VERSION}_linux_${TARGETARCH}" /usr/local/bin/restic && \
    chmod +x /usr/local/bin/restic

ARG RCLONE_VERSION=1.75.2
RUN wget -q "https://github.com/rclone/rclone/releases/download/v${RCLONE_VERSION}/rclone-v${RCLONE_VERSION}-linux-${TARGETARCH}.zip" && \
    unzip -q "rclone-v${RCLONE_VERSION}-linux-${TARGETARCH}.zip" && \
    mv "rclone-v${RCLONE_VERSION}-linux-${TARGETARCH}/rclone" /usr/local/bin/rclone && \
    chmod +x /usr/local/bin/rclone && \
    rm -rf "rclone-v${RCLONE_VERSION}-linux-${TARGETARCH}" "rclone-v${RCLONE_VERSION}-linux-${TARGETARCH}.zip"

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
RUN go mod verify
COPY . .
RUN GOOS=$TARGETOS GOARCH=$TARGETARCH make daemon

FROM alpine:3.24.3@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a AS alpine
RUN apk add --no-cache openssh-client
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
COPY --from=builder /usr/local/bin/rclone /usr/local/bin/rclone
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]

FROM gcr.io/distroless/static@sha256:15f8637509792e05b97d83451b0b6ccfa4abd548f997e406ac1e72c87fe1fa78 AS distroless
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
COPY --from=builder /usr/local/bin/rclone /usr/local/bin/rclone
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]
