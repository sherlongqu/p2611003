# The runtime image contains only the statically compiled binary: no shell, no package manager.
# Build the binary on the host (see README), so the image build never pulls a base image.
FROM scratch

COPY pubdocs /app/pubdocs
COPY ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

WORKDIR /app
ENV PORT=8080 \
    DATA_DIR=/app/data \
    TZ=Asia/Shanghai \
    SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/app/pubdocs", "healthcheck"]

ENTRYPOINT ["/app/pubdocs"]
