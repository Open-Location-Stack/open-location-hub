FROM golang:1.27.1-alpine3.24 AS build
RUN apk add --no-cache build-base pkgconf proj-dev
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN go build -o /out/open-location-hub ./cmd/hub

FROM alpine:3.24.2
RUN apk add --no-cache proj
RUN adduser -D -u 10001 appuser
USER appuser
WORKDIR /app
COPY --from=build /out/open-location-hub /app/open-location-hub
EXPOSE 8080
ENTRYPOINT ["/app/open-location-hub"]
