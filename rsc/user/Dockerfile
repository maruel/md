# syntax=docker/dockerfile:1.6

# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by an Apache License, Version 2.0 that can be found
# in the LICENSE file.
#
# Dockerfile to create md-user: user-level tooling built on top of
# md-root. Rebuilt weekly; the root layer is inherited from md-root.

ARG BASE_ROOT_IMAGE=ghcr.io/caic-xyz/md-root:latest
FROM ${BASE_ROOT_IMAGE} AS final
LABEL org.opencontainers.image.source=https://github.com/caic-xyz/md \
      org.opencontainers.image.licenses=Apache-2.0

USER user
WORKDIR /home/user
SHELL ["/bin/bash", "-lc"]

# Copy all /home/user files.
COPY --chown=user:user home/user/ /home/user/

RUN mkdir -p /home/user/.ssh /home/user/.cache/go-build /home/user/go/pkg && chmod 0700 /home/user/.ssh

# Install tools (as user) in parallel (set MD_SERIAL_SETUP=1 for serial execution)
ARG MD_SERIAL_SETUP=0
ENV MD_SERIAL_SETUP=${MD_SERIAL_SETUP}
RUN --mount=type=cache,target=/home/user/.cache/go-build,uid=1000 \
    --mount=type=cache,target=/home/user/go/pkg/mod,uid=1000 \
    --mount=type=secret,id=github_token,uid=1000 \
    /home/user/setup/run_all.sh

# Generate tool version report
SHELL ["/bin/bash", "-ilc"]
RUN measure_exec.sh 'Version Report' /home/user/setup/generate_version_report.sh > /home/user/src/tool_versions.md

USER root
SHELL ["/bin/bash", "-c"]

# Start SSH server by default
CMD ["/root/start.sh"]

# Stage to export reports
FROM scratch AS reports
COPY --from=final /home/user/src/tool_versions.md /
COPY --from=final /var/log/build_timings.md /

FROM final
