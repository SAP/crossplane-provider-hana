FROM alpine:3.24.2@sha256:294b683cb724975bec92580e1e685676bd4b50bda910ddb8c51d4cabeaec77e6
RUN apk --no-cache add ca-certificates bash

ARG TARGETOS
ARG TARGETARCH

ADD bin/${TARGETOS}_${TARGETARCH}/provider /usr/local/bin/crossplane-hana-provider

EXPOSE 8080
USER 65532
ENTRYPOINT ["crossplane-hana-provider"]

LABEL org.opencontainers.image.title="provider-hana" \
      org.opencontainers.image.description="Crossplane Provider for SAP HANA" \
      org.opencontainers.image.documentation="https://sap.github.io/crossplane-provider-docs/" \
      org.opencontainers.image.url="https://github.com/SAP/crossplane-provider-hana" \
      org.opencontainers.image.source="https://github.com/SAP/crossplane-provider-hana.git" \
      org.opencontainers.image.vendor="SAP SE" \
      org.opencontainers.image.base.name="docker.io/library/alpine:3.23.4"
