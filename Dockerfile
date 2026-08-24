FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/venue-slot ./cmd/server

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/venue-slot /app/venue-slot
RUN mkdir -p /data && chown -R app:app /data /app
USER app
ENV HTTP_ADDR=:8080 DB_PATH=/data/venue-slot.db BUSINESS_TIMEZONE=Asia/Shanghai
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=2s --start-period=3s --retries=12 CMD wget -q -O - http://127.0.0.1:8080/healthz >/dev/null || exit 1
ENTRYPOINT ["/app/venue-slot"]
