# syntax=docker/dockerfile:1
# Isolated amd64 test fixture, never included in product releases. The upstream
# container repositories no longer publish this version; use its official binary.
FROM alpine:3.21
ADD --checksum=sha256:53e2a2cb16c5366ea6fbbc479c19ddb4c6a0948273e752f740fb1fbf27bb817c --chmod=755 https://github.com/minio/minio/releases/download/RELEASE.2025-04-22T22-12-26Z/minio.linux-amd64.RELEASE.2025-04-22T22-12-26Z /usr/local/bin/minio
ADD --checksum=sha256:01f866e9c5f9b87c2b09116fa5d7c06695b106242d829a8bb32990c00312e891 --chmod=755 https://github.com/minio/mc/releases/download/RELEASE.2025-08-13T08-35-41Z/mc.linux-amd64.RELEASE.2025-08-13T08-35-41Z /usr/local/bin/mc
ENV MINIO_UPDATE=off
ENTRYPOINT ["/usr/local/bin/minio"]
