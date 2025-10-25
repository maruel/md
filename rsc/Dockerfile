# Copyright 2025 Marc-Antoine Ruel. All Rights Reserved. Use of this
# source code is governed by a BSD-style license that can be found in the
# LICENSE file.

# Defined in Dockerfile.base
FROM md.base

ARG ENV_FILE

# Settings from the script. We'll want to eventually just use whatever is the current latest.
ENV GO_VERSION=1.25.3

# Specialize the SSH configuration.
RUN rm -f /etc/ssh/ssh_host_*_key /etc/ssh/ssh_host_*_key.pub
COPY etc/ssh/ssh_host_ed25519_key /etc/ssh/ssh_host_ed25519_key
COPY etc/ssh/ssh_host_ed25519_key.pub /etc/ssh/ssh_host_ed25519_key.pub
COPY --chown=user:user home/user/.ssh/authorized_keys /home/user/.ssh/authorized_keys
RUN chmod 0400 /home/user/.ssh/authorized_keys

# Copy the file setting environment variables.
COPY --chown=user:user ${ENV_FILE:+$ENV_FILE} ${ENV_FILE:+/home/user/.env}
# Copy the aliases and setup scripts.
COPY --chown=user:user home/user/ /home/user/

# Install latest Neovim (as root)
RUN ARCH=$(uname -m | sed s/aarch64/arm64/) && \
    curl -sSL -o- https://github.com/neovim/neovim/releases/latest/download/nvim-linux-${ARCH}.tar.gz | tar xz -C /opt --strip-component=1 && \
    ln -s /opt/bin/nvim /usr/local/bin/nvim && \
    ln -s /opt/bin/nvim /usr/local/bin/vim && \
    ln -s /opt/bin/nvim /usr/local/bin/vi

# Install Go (as root)
RUN ARCH=$(uname -m | sed s/aarch64/arm64/ | sed s/x86_64/amd64/) && \
    curl -sSL -o- https://go.dev/dl/go${GO_VERSION}.linux-${ARCH}.tar.gz | tar -C /usr/local -xzf - && \
    echo PATH="/usr/local/go/bin:\${HOME}/go/bin:\${PATH}" >> /etc/profile.d/md.sh
# Install Go packages (as user)
RUN su user -c /home/user/setup/go.sh

# Install nvm, node.js, npm, Claude Code and Gemini CLI (as user)
RUN su user -c /home/user/setup/nodejs.sh
RUN su user -c /home/user/setup/llm.sh

# https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/configuration.md'
COPY etc/gemini-cli/settings.json /etc/gemini-cli/settings.json

# Start SSH server
CMD ["/root/start.sh"]
