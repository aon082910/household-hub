# Household Hub server image (Unraid / Docker).
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=1.0.0
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/hhub ./cmd/hhub

FROM alpine:3
RUN apk add --no-cache ca-certificates tzdata tesseract-ocr tesseract-ocr-data-eng poppler-utils && mkdir -p /data /statements /backup-copy && chown 99:100 /data /statements /backup-copy
COPY --from=build /out/hhub /usr/local/bin/hhub
ENV HHUB_ADDR=:8080 HHUB_DATA=/data HHUB_WATCH_ROOT=/statements TZ=America/New_York
VOLUME /data /statements
EXPOSE 8080
USER 99:100
HEALTHCHECK --interval=60s --timeout=5s CMD wget -qO- http://127.0.0.1:8080/api/ping || exit 1
ENTRYPOINT ["/usr/local/bin/hhub", "serve"]
