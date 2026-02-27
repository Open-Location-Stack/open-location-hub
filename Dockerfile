FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download || true
COPY . .
RUN go build -o /out/open-rtls-hub ./cmd/hub

FROM alpine:3.22
RUN adduser -D -u 10001 appuser
USER appuser
WORKDIR /app
COPY --from=build /out/open-rtls-hub /app/open-rtls-hub
EXPOSE 8080
ENTRYPOINT ["/app/open-rtls-hub"]
