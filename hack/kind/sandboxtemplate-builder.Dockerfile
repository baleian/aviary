# The builder of templates, with the certificate of the registry of the local
# cluster among those that it trusts. Fast Sandbox has no way to give its
# builder a certificate, so `make cluster-up` builds this from the builder
# of the release and has the cluster use it.
ARG BASE
FROM ${BASE}
COPY registry.crt /usr/local/share/ca-certificates/local-cluster-registry.crt
RUN update-ca-certificates
