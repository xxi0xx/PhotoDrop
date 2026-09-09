# syntax=docker/dockerfile:1
FROM node:22-alpine AS frontend
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run check && npm run build

FROM golang:1.26-alpine AS backend
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/photodrop ./cmd/photodrop

FROM alpine:3.23 AS runtime
RUN apk add --no-cache su-exec \
    && addgroup -g 10001 photodrop \
    && adduser -D -H -u 10001 -G photodrop photodrop \
    && mkdir /data \
    && chown photodrop:photodrop /data
COPY --from=backend /out/photodrop /usr/local/bin/photodrop
COPY --chmod=755 docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
ENV PHOTODROP_LISTEN_ADDR=:8080 PHOTODROP_DATA_DIR=/data
EXPOSE 8080
VOLUME ["/data"]
STOPSIGNAL SIGTERM
HEALTHCHECK --interval=15s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/photodrop", "healthcheck"]
ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD ["/usr/local/bin/photodrop"]
