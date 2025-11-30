# md: Parallel Development Containers for AI Coding Agents

A development container system that enables you to work with multiple coding agents in parallel safely. Run AI
coding tools (Claude Code, Codex, Amp CLI, Gemini CLI, Qwen CLI, etc.) without branch conflicts or
interference.

## Overview

`md` runs common coding agent tools in what is generally called _YOLO mode_ (all prompts to run commands and
sandboxing disabled) in a safe manner.

Instead of using git worktree, you use normal branches. Each container runs a **complete separate git clone**
of your repository. Your local checkout is never mapped into the container. Because each branch runs in an
isolated container with a different name, you can safely:

- Make changes in parallel on different branches
- Run tests simultaneously without conflicts
- Switch branches locally without affecting running containers
- Delete containers cleanly when done

## Prerequisites

- **Docker** installed and running

## Installation

Clone the repository and add it to your PATH:

```bash
git clone https://github.com/maruel/md
export PATH="$PATH:$(pwd)/md/bin"
```

It is **highly recommended** to setup the git alias `git squash` from
[squash.ini](https://github.com/maruel/bin_pub/blob/main/configs/.config/git/squash.ini).

## Quick Start

Here's a complete workflow example:

```bash
# Start a container for your current branch
md start

# Inside the container, run your coding agent
# (e.g., amp, claude, codex, etc.)

# Exit when done
exit

# Back on your local machine, pull changes
md pull

# Clean up the container
md kill
```

## Usage

### Starting a Container

```bash
md start
```

Each container is named `md-<repo-name>-<branch-name>`. For example, if you're on the `wip` branch of
[github.com/maruel/genai](https://github.com/maruel/genai), the container is named `md-genai-wip`.

### Accessing the Container

Access the container via SSH:

```bash
# SSH into the container (in another terminal window)
ssh md-<repo-name>-<branch-name>
```

**Tip:** Use two SSH sessions—one for running the coding agent and one for inspecting results (e.g., `git diff`) and running tests.

### Pulling Changes Back

To pull changes from the container into your local branch:

```bash
md pull
```

### Pushing Changes to the Container

If you've rebased on `origin/main` or made other local changes that need to be synced to the container:

```bash
md push
```

### Cleaning Up

When done with a container:

```bash
md kill
```

## How It Works

### User and Permissions

The container runs as the user account `user`, which is mapped to your local user ID, ensuring proper file permissions.

### Resource Mappings

Files under [rsc/](/rsc) are copied as-is inside the container. The system generates the following files automatically:

- `rsc/etc/ssh/ssh_host_ed25519_key`
- `rsc/etc/ssh/ssh_host_ed25519_key.pub`
- `rsc/home/user/.ssh/authorized_keys`

Host SSH keys ensure you're connecting to the expected container.

### Configuration and Credentials

The following directories from your local machine are mounted into each container for agent configurations and credentials:

- `~/.amp` - Amp CLI configuration
- `~/.codex` - Codex configuration
- `~/.claude` - Claude configuration
- `~/.gemini` - Gemini CLI configuration
- `~/.qwen` - Qwen CLI configuration
- `~/.config/amp` - Amp tool config
- `~/.config/goose` - Goose configuration
- `~/.local/share/amp` - Amp data
- `~/.local/share/goose` - Goose data

### Preinstalled Tools

The container runs with minimal overhead—only sshd is running to maximize efficiency. However, it comes preinstalled with everything needed for:

- TypeScript development
- Go development
- Neovim editor

## Contributing

Want a feature or found a bug? Contributions are welcome! Send a PR. Thanks!
