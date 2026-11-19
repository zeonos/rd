FROM golang:1.24-alpine AS build
RUN apk add --no-cache git
# go.mod maps golang.org/x/* and gopkg.in/* to their GitHub mirrors; fetch directly.
ENV GOPROXY=direct
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rd-api ./cmd/rd-api

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H -u 10001 rd \
    && mkdir -p /data && chown rd /data
COPY --from=build /out/rd-api /usr/local/bin/rd-api
USER rd
EXPOSE 8080
ENTRYPOINT ["rd-api"]
CMD ["serve"]
