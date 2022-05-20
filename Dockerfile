FROM alpine
COPY  /kvrocks_exporter /kvrocks_exporter

EXPOSE 9121

ENTRYPOINT [ "/kvrocks_exporter" ]
