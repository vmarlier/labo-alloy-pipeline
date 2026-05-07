FROM victoriametrics/victoria-metrics:v1.101.0

# We expose the standard Prometheus port that VM mimics
EXPOSE 8428

# Set retention to 1 day for the lab
ENTRYPOINT ["/victoria-metrics-prod", "-retentionPeriod=1d"]
