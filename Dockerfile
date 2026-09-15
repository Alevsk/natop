# syntax=docker/dockerfile:1
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/natop .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /out/natop /usr/local/bin/natop
ENV TERM=xterm-256color
USER 65532:65532
ENTRYPOINT ["natop"]
