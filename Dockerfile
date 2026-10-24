# Build the single Titra binary, then ship it on a minimal base.
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=$(cat VERSION 2>/dev/null || echo docker)" -o /titra .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 titra
COPY --from=build /titra /usr/local/bin/titra
USER titra
ENV TITRA_ADDR=:8080 TITRA_DATA=/data
VOLUME /data
EXPOSE 8080
# The server face of the binary. (The same binary also guards CI jobs.)
ENTRYPOINT ["titra", "serve"]
