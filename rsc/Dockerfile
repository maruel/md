# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

ARG BASE_IMAGE=ghcr.io/maruel/md:latest
FROM ${BASE_IMAGE}

ARG BASE_IMAGE
ARG BASE_IMAGE_DIGEST=unknown
ARG CONTEXT_SHA=unknown

ARG ENV_FILE

LABEL \
	md.base_image="${BASE_IMAGE}" \
	md.base_digest="${BASE_IMAGE_DIGEST}" \
	md.context_sha="${CONTEXT_SHA}"

# Specialize the SSH configuration.
RUN rm -f /etc/ssh/ssh_host_*_key /etc/ssh/ssh_host_*_key.pub
COPY etc/ssh/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
COPY etc/ssh/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ed25519_key.pub
COPY --chown=user:user home/user/.ssh/authorized_keys /home/user/.ssh/authorized_keys
RUN chmod 0400 /home/user/.ssh/authorized_keys

# Copy the file setting environment variables.
COPY --chown=user:user ${ENV_FILE:+$ENV_FILE} ${ENV_FILE:+/home/user/.env}
