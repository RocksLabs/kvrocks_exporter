FROM golang:1.23-alpine3.21 AS build

ARG TARGETARCH=amd64
ENV GOARCH=${TARGETARCH}

WORKDIR /opt

COPY . .

RUN go build . 

FROM alpine:3.21

RUN apk update && apk upgrade

WORKDIR /opt

COPY --from=build /opt/kvrocks_exporter kvrocks_exporter

COPY --from=build /opt/LICENSE .

EXPOSE 9121/tcp

ENTRYPOINT [ "/kvrocks_exporter" ]
