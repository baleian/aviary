FROM nousresearch/hermes-agent:v0.23.1@sha256:276d44685c65c0cb2a25f190b63c65c8906f9dc76486fb1c05ae71b5826bda08
COPY config.yaml /etc/hermes/config.yaml
# The base image confines the file tools to its own HERMES_HOME, so the limit
# has to move with it to the session home.
ENV HERMES_HOME=/home/.hermes \
    HERMES_WRITE_SAFE_ROOT=/home \
    AVIARY_HARNESS_COMMAND="hermes acp"
