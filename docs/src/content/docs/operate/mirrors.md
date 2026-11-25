---
title: Mirrors
description: How to build and run Aviary in a network that does not reach the public registries.
---

`make` downloads images and packages from public registries, and the chart
of agent-sandbox from its repository, when it builds the images, runs the
tests and creates the local cluster. In a network that does not reach them,
name their mirrors in the environment, on the command line of `make`, or in
`.env`, a file at the root of the repository that Git ignores, with lines of
`NAME=value` without quotes:

| Variable | Mirror of |
| --- | --- |
| `DOCKER_IO`, `GCR_IO`, `GHCR_IO`, `QUAY_IO`, `REGISTRY_K8S_IO` | The registry of that name: a registry, with a path if the mirror has one, such as `mirror.example.com/docker-hub`. The nodes of the local cluster pull the images of these registries from the mirrors as well, over HTTPS. |
| `GOPROXY` | The Go module proxy. Go's own variable. |
| `NPM_CONFIG_REGISTRY` | The npm registry. npm's own variable. |
| `CRATES_IO` | crates.io: a registry as Cargo names one, such as `sparse+https://mirror.example.com/crates/index/`. |
| `DEB_DEBIAN_ORG` | `http://deb.debian.org`, for the Debian packages of the harness images: an address that serves `debian` and `debian-security`, such as `https://mirror.example.com/deb`. The images keep it as the source of their packages. |
| `AGENT_SANDBOX_REPO` | The repository of agent-sandbox, `https://github.com/kubernetes-sigs/agent-sandbox`, for its chart, which is in the source of its release. |

Most images are pulled by digests that the repository pins, so a mirror has
to serve the images of its registry unchanged. The builds read the mirrors
of the packages, and the nodes those of the registries, without credentials;
both trust the certificate authorities of their images. rustup takes the
Rust toolchain from the server in its own variable, `RUSTUP_DIST_SERVER`.

A cluster of your own pulls the images that the values of the chart name:
`supervisor.image`, `egressProxy.image`, `agent-sandbox.image.repository`
and the `image` of each harness in `runtime.harnesses`.
