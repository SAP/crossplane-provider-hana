FROM scratch

COPY package.yaml .

ARG BUILD_TIMESTAMP
ARG GIT_COMMIT

LABEL org.opencontainers.image.title="crossplane-provider-hana" \
      org.opencontainers.image.description="Configuration for hana provider" \
      org.opencontainers.image.documentation="https://pages.github.tools.sap/cloud-orchestration/" \
      org.opencontainers.image.url="https://github.com/SAP/crossplane-provider-hana" \
      org.opencontainers.image.source="https://github.com/SAP/crossplane-provider-hana.git" \
      org.opencontainers.image.vendor="SAP SE" \
      org.opencontainers.image.base.name="scratch" \
      org.opencontainers.image.revision="$GIT_COMMIT" \
      org.opencontainers.image.created="$BUILD_TIMESTAMP"