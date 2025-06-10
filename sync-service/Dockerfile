FROM alpine:latest

COPY ZscalerRootCertificate-2048-SHA256.crt /tmp/

# Update ca-certificates and package index first
RUN cat /tmp/ZscalerRootCertificate-2048-SHA256.crt >> /etc/ssl/certs/ca-certificates.crt && \
    apk update && \
    apk add --no-cache ca-certificates && \
    mv /tmp/ZscalerRootCertificate-2048-SHA256.crt  /usr/local/share/ca-certificates && \
    update-ca-certificates

# Install required packages
RUN apk add --no-cache \
    curl \
    jq \
    bash \
    coreutils \
    findutils

# Create a non-root user for security
RUN addgroup -g 1000 syncuser && \
    adduser -D -s /bin/bash -u 1000 -G syncuser syncuser

# Create directories for scripts and config
RUN mkdir -p /scripts /home/syncuser/.totalrecall

# Copy scripts (will be mounted from host)
# COPY scripts/ /scripts/

# Set proper permissions
RUN chown -R syncuser:syncuser /home/syncuser

# Switch to non-root user
USER syncuser

# Set working directory
WORKDIR /home/syncuser

# Default command (can be overridden in docker-compose)
CMD ["/bin/bash", "/scripts/config-sync-service.sh"]
