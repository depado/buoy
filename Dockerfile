FROM golang:1.26.4-alpine@sha256:3ad57304ad93bbec8548a0437ad9e06a455660655d9af011d58b993f6f615648 AS builder

RUN apk add --no-cache make git

ARG RESTIC_VERSION=0.19.0
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
RUN make

FROM gcr.io/distroless/static@sha256:3592aa8171c77482f62bbc4164e6a2d141c6122554ace66e5cc910cadb961ff0
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
WORKDIR /data
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]
