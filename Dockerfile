FROM golang:1.19-alpine3.18 as build

ARG TARGETARCH

RUN apk update && apk add make
WORKDIR /kvrocks_exporter

COPY . .
RUN make build-binaries ARCH=${TARGETARCH}


FROM alpine:3.18

COPY --from=build /kvrocks_exporter/.build/kvrocks_exporter /kvrocks_exporter

EXPOSE 9121

ENTRYPOINT [ "/kvrocks_exporter" ]
