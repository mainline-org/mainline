ARG LAYERFS_BASE_IMAGE
FROM ${LAYERFS_BASE_IMAGE}
COPY --chmod=0755 mainline /usr/local/bin/mainline
RUN command -v bash && command -v git && command -v timeout && command -v base64 && command -v find && mainline version
