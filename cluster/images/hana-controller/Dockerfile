ARG BUILDENV=upbound

FROM scratch AS upbound
ADD provider /provider

FROM scratch AS piper
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ADD provider-$TARGETOS.$TARGETARCH /provider

FROM ${BUILDENV} AS buildenv

FROM alpine:3.23.3@sha256:25109184c71bdad752c8312a8623239686a9a2071e8825f20acb8f2198c3f659
RUN apk --no-cache add ca-certificates bash

COPY --from=buildenv /provider /usr/local/bin/crossplane-hana-provider

EXPOSE 8080
USER 1001
ENTRYPOINT ["crossplane-hana-provider"]

ARG BUILD_TIMESTAMP
ARG GIT_COMMIT

LABEL org.opencontainers.image.title="crossplane-provider-hana-controller" \
      org.opencontainers.image.description="Crossplane controller for hana in BTP" \
      org.opencontainers.image.documentation="https://pages.github.tools.sap/cloud-orchestration/" \
      org.opencontainers.image.url="https://github.com/SAP/crossplane-provider-hana" \
      org.opencontainers.image.source="https://github.com/SAP/crossplane-provider-hana.git" \
      org.opencontainers.image.vendor="SAP SE" \
      org.opencontainers.image.base.name="docker.io/library/alpine:3.22.2" \
      org.opencontainers.image.base.digest="sha256:4b7ce07002c69e8f3d704a9c5d6fd3053be500b7f1c69fc0d80990c2ad8dd412" \
      org.opencontainers.image.revision="$GIT_COMMIT" \
      org.opencontainers.image.created="$BUILD_TIMESTAMP"