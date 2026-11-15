FROM rust:1.99.1-alpine3.24@sha256:44819b27be4c97b0e4d7203bd2e02c4820f97700211b6a37fb4c246a2de59383 AS build
ENV RUSTUP_AUTO_INSTALL=0
WORKDIR /src
COPY rust-toolchain.toml Cargo.toml Cargo.lock ./
COPY runner runner
COPY proto proto
RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/src/target \
    cargo build --release --locked --package aviary-runner --example fake-supervisor && \
    cp target/release/examples/fake-supervisor /fake-supervisor

FROM scratch
COPY --from=build /fake-supervisor /fake-supervisor
ENTRYPOINT ["/fake-supervisor"]
