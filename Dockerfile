FROM golang:1.23-alpine3.21 AS build

ARG TARGETARCH=amd64
ENV GOARCH=${TARGETARCH}

WORKDIR /opt

COPY . .

RUN go build . 

FROM alpine:3.21

RUN apk update && apk upgrade && apk add --no-cache curl

WORKDIR /opt

COPY --from=build /opt/kvrocks_exporter kvrocks_exporter

COPY --from=build /opt/LICENSE .

EXPOSE 9121/tcp

HEALTHCHECK --interval=30s --timeout=1s --start-period=5s --retries=3 CMD curl --fail -s http://localhost:9121/health | grep 'ok' || exit 1

ENTRYPOINT [ "/kvrocks_exporter" ]
