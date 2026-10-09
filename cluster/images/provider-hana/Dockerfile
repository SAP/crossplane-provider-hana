# renovate: datasource=docker depName=gcr.io/distroless/static
ARG DISTROLESS_DIGEST="sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0"

FROM gcr.io/distroless/static@${DISTROLESS_DIGEST}
ARG TARGETOS
ARG TARGETARCH

COPY bin/${TARGETOS}_${TARGETARCH}/provider /usr/local/bin/crossplane-hana-provider

EXPOSE 8080
USER 65532
ENTRYPOINT ["crossplane-hana-provider"]

ARG DISTROLESS_DIGEST
ARG GIT_COMMIT

LABEL org.opencontainers.image.title="provider-hana" \
      org.opencontainers.image.description="Crossplane Provider for SAP HANA" \
      org.opencontainers.image.documentation="https://sap.github.io/crossplane-provider-docs/" \
      org.opencontainers.image.url="https://github.com/SAP/crossplane-provider-hana" \
      org.opencontainers.image.source="https://github.com/SAP/crossplane-provider-hana.git" \
      org.opencontainers.image.vendor="SAP SE" \
      org.opencontainers.image.base.name="gcr.io/distroless/static" \
      org.opencontainers.image.base.digest="$DISTROLESS_DIGEST" \
      org.opencontainers.image.revision="$GIT_COMMIT"
