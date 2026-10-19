FROM golang:1.27-alpine3.24 AS build

ARG TARGETARCH
ARG VERSION
ARG BUILD_DATE
ARG COMMIT_SHA
ENV GOARCH=${TARGETARCH}

ENV TZ=Etc/UTC
RUN ln -snf /usr/share/zoneinfo/$TZ /etc/localtime && echo $TZ > /etc/timezone

WORKDIR /opt

COPY . .

RUN go build -ldflags "-X main.BuildVersion=${VERSION} -X main.BuildDate=${BUILD_DATE} -X main.BuildCommitSha=${COMMIT_SHA}" .

FROM alpine:3.24

RUN apk update && apk upgrade && apk add --no-cache ca-certificates tzdata curl && apk -v cache clean

WORKDIR /opt

COPY --from=build /opt/kvrocks_exporter kvrocks_exporter

COPY --from=build /opt/LICENSE .

EXPOSE 9121/tcp

HEALTHCHECK --interval=30s --timeout=1s --start-period=5s --retries=3 CMD curl --fail -s http://localhost:9121/health | grep 'ok' || exit 1

ENTRYPOINT [ "/opt/kvrocks_exporter" ]
