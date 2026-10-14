# Local disposable Debian package validation only; no application deployment.
FROM debian:stable-slim
RUN . /etc/os-release && test "$ID" = debian && test "$VERSION_ID" = 13 \
    && apt-get update \
    && apt-get install -y --no-install-recommends adduser systemd openssl
