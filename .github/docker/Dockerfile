# The Dibbla CLI as a CI image — the default, shell-bearing variant.
#
# Assembled from the *published* release archives rather than compiled here, so
# the binary this image ships is byte-identical to the one on the GitHub release
# and on install.dibbla.com. That is also why the `container` job in release.yml
# hangs off `checksums` rather than `release` — see CLAUDE.md, "release.yml job
# order is load-bearing", rule 3.
#
# The build context is the staging directory that job prepares:
#   stage/linux_amd64/dibbla
#   stage/linux_arm64/dibbla
#
# There are deliberately no RUN steps: nothing executes in the target
# architecture, so the multi-arch build needs no QEMU emulation. Alpine's base
# rootfs already carries /etc/ssl/certs/ca-certificates.crt, so the CLI can
# speak TLS without an `apk add` — which is what keeps this a pure COPY.

# Alpine, not distroless, and that is the correction DIB-418 records.
#
# This image shipped distroless first, on the reasoning that a CI image holding
# a deploy token should carry no scripting surface. That argument is sound for a
# long-running service; it is wrong for a CI tool image, and it made the image
# unusable for the runner it was built for. Woodpecker compiles a step's
# `commands:` into ["/bin/sh","-c", ...], so every `commands:` step died with
#
#     exec: "/bin/sh": stat /bin/sh: no such file or directory
#
# before the CLI was reached — including the example the README published. The
# same is true of GitLab CI and of any runner that generates a script.
#
# It is also the industry norm: hashicorp/terraform, amazon/aws-cli, alpine/helm
# and bitnami/kubectl all ship a shell and keep the tool as the entrypoint. The
# security gain from removing it is small — a CI container is ephemeral and runs
# the pipeline's own commands — and the cost is that no pipeline can loop, pipe
# or branch around a CLI call.
#
# The distroless build is kept as an opt-in variant for single-invocation steps:
# see Dockerfile.distroless and the `-distroless` tag.
#
# Pinned by digest, not by tag. Pinning a credential-adjacent base image to a
# mutable tag is the finding DIB-122/123 record against our own workflows, and
# this image goes to customers' CI.
# Refresh with:  crane digest alpine:3.21
FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

ARG TARGETARCH
ARG VERSION

LABEL org.opencontainers.image.title="dibbla" \
      org.opencontainers.image.description="Dibbla CLI — deploy and manage Dibbla applications from CI" \
      org.opencontainers.image.source="https://github.com/dibbla-agents/dibbla-cli" \
      org.opencontainers.image.url="https://dibbla.com" \
      org.opencontainers.image.vendor="Dibbla Agents" \
      org.opencontainers.image.version="${VERSION}"

# Plain COPY, no --chmod: GoReleaser's archives already carry the binary as
# 0755 and tar preserves it, so the mode needs no fixing — and avoiding
# --chmod is what lets this Dockerfile build on the builtin BuildKit frontend
# instead of fetching docker/dockerfile:1 at build time. The staging step
# re-asserts the mode so a future archive change cannot ship a non-executable
# image.
COPY stage/linux_${TARGETARCH}/dibbla /usr/local/bin/dibbla

# Lets the CLI recognise that it is running from a Dibbla CI image, so the
# subcommands that shell out to git/go/npm can explain themselves rather than
# failing with a bare "executable file not found in $PATH". Set on both
# variants: neither carries git, go or npm.
# See internal/preflight/container.go.
ENV DIBBLA_IN_CONTAINER=1

# The tool is the entrypoint, matching terraform/aws/helm. A runner that wants
# to drive a shell instead overrides it — which is exactly what Woodpecker's
# `commands:` does, and why the shell has to be here for that to work.
ENTRYPOINT ["/usr/local/bin/dibbla"]
