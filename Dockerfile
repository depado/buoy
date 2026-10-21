FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS builder

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

FROM alpine:3.24.3@sha256:1b7232cda4ca3dc20b5d1247bdc9bfb631f2661d15d323bdd6fccc035ed1a9e7 AS alpine
RUN apk add --no-cache openssh-client
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
COPY --from=builder /usr/local/bin/rclone /usr/local/bin/rclone
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]

FROM gcr.io/distroless/static@sha256:034df5c1737e8241f6ed6423a12e720ee0f6d6f088609ee172f98f6f2166ef5d AS distroless
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
COPY --from=builder /usr/local/bin/rclone /usr/local/bin/rclone
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]
