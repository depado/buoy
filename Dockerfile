FROM golang:1.26.5-alpine@sha256:99e12cfb19b753915f9b9fdc5a99f1869a24a69d3a0955832d5702e7fa68f1be AS builder

RUN apk add --no-cache make git

ARG RESTIC_VERSION=0.19.1
ARG TARGETARCH
RUN wget -q "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" && \
    bunzip2 "restic_${RESTIC_VERSION}_linux_${TARGETARCH}.bz2" && \
    mv "restic_${RESTIC_VERSION}_linux_${TARGETARCH}" /usr/local/bin/restic && \
    chmod +x /usr/local/bin/restic

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
RUN go mod verify
COPY . .
RUN make daemon

FROM gcr.io/distroless/static@sha256:d5f030ca7c5793784e9ea4178a116da360250411d13921a5af27c6cb5a5949bf
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]
