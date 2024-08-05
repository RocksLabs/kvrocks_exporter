FROM golang:1.22-alpine3.20 AS build

ARG TARGETARCH

RUN apk update && apk upgrade && apk add make
WORKDIR /kvrocks_exporter

COPY . .
RUN make build-binaries ARCH=${TARGETARCH}


FROM alpine:3.20

COPY --from=build /kvrocks_exporter/.build/kvrocks_exporter /kvrocks_exporter

EXPOSE 9121

ENTRYPOINT [ "/kvrocks_exporter" ]
