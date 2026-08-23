# The Dibbla CLI as a CI image.
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
# architecture, so the multi-arch build needs no QEMU emulation.

# distroless static gives us ca-certificates (the CLI only ever speaks HTTPS to
# the API), /etc/passwd and tzdata. No shell and no package manager, which is
# most of the point — a CI image holding a deploy token should not carry a
# scripting surface.
#
# Pinned by digest, not by tag. Pinning a credential-adjacent base image to a
# mutable tag is precisely the finding DIB-122/123 record against our own
# workflows; this image goes to customers' CI, so it holds itself to that rule.
# Refresh with:  crane digest gcr.io/distroless/static-debian12:latest
#
# This is the root variant, not `:nonroot`, and that is a decision rather than
# an oversight. `dibbla db dump -o <path>` (internal/cmd/deploy/db.go) writes a
# caller-chosen path into the CI workspace, which is typically root-owned; a
# nonroot image turns that into a permission error the user cannot fix from
# their pipeline. Root inside an ephemeral CI container is the same posture as
# the official kubectl/aws-cli/gh images. A pipeline that wants otherwise can
# still run the image with an explicit user.
FROM gcr.io/distroless/static-debian12:latest@sha256:d75cdd72874d4790092fcb1b058493ecf6bb5bf2b2b897045b00ff01d91843f2

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
# --chmod is what lets this Dockerfile build on the builtin BuildKit
# frontend instead of fetching docker/dockerfile:1 at build time. The
# staging step re-asserts the mode so a future archive change cannot ship a
# non-executable image.
COPY stage/linux_${TARGETARCH}/dibbla /usr/local/bin/dibbla

# Lets the CLI recognise that it is running from this image, so the subcommands
# that shell out to git/go/npm can explain themselves instead of failing with a
# bare "executable file not found in $PATH". See internal/preflight/container.go.
ENV DIBBLA_IN_CONTAINER=1

ENTRYPOINT ["/usr/local/bin/dibbla"]
