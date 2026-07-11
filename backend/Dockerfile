FROM golang:1.26.5-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go test ./... && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/dreamwhiteboard ./cmd/server

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata && addgroup -S -g 10001 dreamwhiteboard && adduser -S -D -H -u 10001 -G dreamwhiteboard dreamwhiteboard
WORKDIR /app
COPY --from=build /out/dreamwhiteboard /usr/local/bin/dreamwhiteboard
COPY --from=build /src/migrations ./migrations
RUN mkdir -p /app/uploads && chown -R dreamwhiteboard:dreamwhiteboard /app
USER dreamwhiteboard
EXPOSE 8080
CMD ["dreamwhiteboard"]
