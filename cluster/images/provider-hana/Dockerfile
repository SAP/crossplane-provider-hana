FROM alpine:3.23.4@sha256:5b10f432ef3da1b8d4c7eb6c487f2f5a8f096bc91145e68878dd4a5019afde11
RUN apk --no-cache add ca-certificates bash

ARG TARGETOS
ARG TARGETARCH

ADD bin/${TARGETOS}_${TARGETARCH}/provider /usr/local/bin/crossplane-hana-provider

EXPOSE 8080
USER 1001
ENTRYPOINT ["crossplane-hana-provider"]

LABEL org.opencontainers.image.title="provider-hana" \
      org.opencontainers.image.description="Crossplane Provider for SAP HANA" \
      org.opencontainers.image.documentation="https://sap.github.io/crossplane-provider-docs/" \
      org.opencontainers.image.url="https://github.com/SAP/crossplane-provider-hana" \
      org.opencontainers.image.source="https://github.com/SAP/crossplane-provider-hana.git" \
      org.opencontainers.image.vendor="SAP SE" \
      org.opencontainers.image.base.name="docker.io/library/alpine:3.23.4"
