FROM golang:1.26.4-alpine@sha256:f1ddd9fe14fffc091dd98cb4bfa999f32c5fc77d2f2305ea9f0e2595c5437c14 AS builder

RUN apk add --no-cache make git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
RUN go mod verify
COPY . .
RUN make

ARG RESTIC_VERSION=0.19.0
RUN wget -q "https://github.com/restic/restic/releases/download/v${RESTIC_VERSION}/restic_${RESTIC_VERSION}_linux_amd64.bz2" && \
    bunzip2 "restic_${RESTIC_VERSION}_linux_amd64.bz2" && \
    mv "restic_${RESTIC_VERSION}_linux_amd64" /usr/local/bin/restic && \
    chmod +x /usr/local/bin/restic

FROM gcr.io/distroless/static@sha256:3592aa8171c77482f62bbc4164e6a2d141c6122554ace66e5cc910cadb961ff0
COPY --from=builder /app/buoy /usr/local/bin/buoy
COPY --from=builder /usr/local/bin/restic /usr/local/bin/restic
ENTRYPOINT ["/usr/local/bin/buoy"]
CMD ["run"]
