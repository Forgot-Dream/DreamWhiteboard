FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod ./
COPY . .
RUN go test ./... && go build -o /out/dreamwhiteboard ./cmd/server

FROM alpine:3.20
WORKDIR /app
COPY --from=build /out/dreamwhiteboard /usr/local/bin/dreamwhiteboard
COPY migrations ./migrations
RUN mkdir -p /app/uploads
EXPOSE 8080
CMD ["dreamwhiteboard"]

