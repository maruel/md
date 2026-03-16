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

# Set GITHUB_TOKEN for setup scripts if the secret is available.
# Each RUN gets its own layer for better caching and smaller diffs.
RUN mkdir -p /home/user/.ssh /home/user/.cache/go-build /home/user/go/pkg && chmod 0700 /home/user/.ssh

RUN --mount=type=cache,target=/home/user/.cache/go-build,uid=1000 \
    --mount=type=cache,target=/home/user/go/pkg/mod,uid=1000 \
    --mount=type=secret,id=github_token,uid=1000 \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN=$(cat /run/secrets/github_token); fi && \
    measure_exec.sh 'Go' /home/user/setup/1_go.sh

RUN --mount=type=secret,id=github_token,uid=1000 \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN=$(cat /run/secrets/github_token); fi && \
    measure_exec.sh 'Node.js' /home/user/setup/2_nodejs.sh

RUN --mount=type=secret,id=github_token,uid=1000 \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN=$(cat /run/secrets/github_token); fi && \
    measure_exec.sh 'Bun' /home/user/setup/3_bun.sh

RUN --mount=type=secret,id=github_token,uid=1000 \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN=$(cat /run/secrets/github_token); fi && \
    measure_exec.sh 'Android SDK' /home/user/setup/4_android.sh

RUN --mount=type=secret,id=github_token,uid=1000 \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN=$(cat /run/secrets/github_token); fi && \
    measure_exec.sh 'Rust' /home/user/setup/5_rust.sh

RUN --mount=type=secret,id=github_token,uid=1000 \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN=$(cat /run/secrets/github_token); fi && \
    measure_exec.sh 'Python' /home/user/setup/6_python.sh

RUN --mount=type=secret,id=github_token,uid=1000 \
    if [ -f /run/secrets/github_token ]; then export GITHUB_TOKEN=$(cat /run/secrets/github_token); fi && \
    measure_exec.sh 'LLM Tools' /home/user/setup/7_llm_tools.sh

# Remove lines that installers (nvm, bun, opencode) appended to .bashrc.
RUN measure_exec.sh 'Bashrc Cleanup' /home/user/setup/bashrc_cleanup.sh

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
