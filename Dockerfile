FROM golang:1.23-alpine3.21 AS build

ARG TARGETARCH

RUN apk update && apk upgrade && apk add make

WORKDIR /opt

COPY . .

RUN make build-binaries ARCH=${TARGETARCH}

FROM alpine:3.21

RUN apk update && apk upgrade

COPY --from=build /kvrocks_exporter/.build/kvrocks_exporter /kvrocks_exporter

COPY --from=build ./LICENSE /

EXPOSE 9121/tcp

ENTRYPOINT [ "/kvrocks_exporter" ]
