# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/selectdev/purros/api/internal/modules/platform.Version=${VERSION}" \
    -o /out/purros ./cmd/purros
RUN mkdir -p /out/state/backups /out/state/files

# debug-nonroot adds busybox `sh` so the Dokploy terminal can exec in and run `purros`.
FROM gcr.io/distroless/static-debian12:debug-nonroot
COPY --from=build /out/purros /usr/local/bin/purros
# Uploaded files and backups live here; mount a volume on it (see docker-compose.yml).
COPY --from=build --chown=65532:65532 /out/state /var/lib/purros
ENV PURROS_LISTEN=:8080 PURROS_STATE_DIR=/var/lib/purros
EXPOSE 8080
USER nonroot
ENTRYPOINT ["purros"]
CMD ["serve"]
