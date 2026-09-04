FROM --platform=$BUILDPLATFORM golang:1.27.1-alpine@sha256:cf6fca6641884b8433441b2b0652976f975e1d0fdd26d177eaaf8596087f3125 AS builder

RUN apk add --no-cache make git tzdata unzip

ARG TARGETOS
ARG TARGETARCH
ARG RESTIC_VERSION=0.19.1
RUN wget -q "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" && \
    bunzip2 "restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" && \
    mv "restic_${RESTIC_VERSION}_linux_${TARGETARCH}" /usr/local/bin/restic && \
    chmod +x /usr/local/bin/restic

ARG RCLONE_VERSION=1.75.1
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

FROM alpine:3.24.1@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b AS alpine
RUN apk add --no-cache openssh-client
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
COPY --from=builder /usr/local/bin/rclone /usr/local/bin/rclone
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]

FROM gcr.io/distroless/static@sha256:f2ea2709ac8db56323cbd7d014277f32cb572d9ea124b0076f7aafe5980678fe AS distroless
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
COPY --from=builder /usr/local/bin/rclone /usr/local/bin/rclone
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]
