FROM grafana/loki:3.0.0

COPY loki-local-config.yaml /etc/loki/local-config.yaml

CMD ["-config.file=/etc/loki/local-config.yaml"]
