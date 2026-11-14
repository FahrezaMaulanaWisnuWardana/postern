# Build the sync server as a static binary, then copy it into a minimal image.
FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# The server has no CGO dependencies, so it can be fully static.
ARG VERSION=0.1.0
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags "-s -w -X 'yourpm/core/brand.Version=${VERSION}'" \
    -o /out/yourpm-server ./cmd/yourpm-server

FROM alpine:3.22

RUN addgroup -S yourpm && adduser -S -G yourpm -h /data yourpm \
    && apk add --no-cache ca-certificates tzdata

COPY --from=build /out/yourpm-server /usr/local/bin/yourpm-server

# The server stores SQLite plus, if no secret is supplied, its JWT key here.
RUN mkdir -p /data && chown yourpm:yourpm /data
VOLUME ["/data"]

USER yourpm
ENV YOURPM_ADDR=:8080 \
    YOURPM_DATA=/data

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/readyz >/dev/null || exit 1

ENTRYPOINT ["/usr/local/bin/yourpm-server"]
