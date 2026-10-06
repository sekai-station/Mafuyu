# syntax=docker/dockerfile:1
FROM golang:1.26.6-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG GO_BUILD_TAGS=""
RUN CGO_ENABLED=0 go build -tags="${GO_BUILD_TAGS}" -trimpath -ldflags="-s -w" -o /server ./cmd/server

FROM alpine:3.23
RUN apk add --no-cache ca-certificates tzdata wget
WORKDIR /app
COPY --from=build /server /app/server
EXPOSE 8888
STOPSIGNAL SIGTERM
ENTRYPOINT ["/app/server"]
CMD ["-config", "/app/config.yaml"]
