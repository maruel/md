# md: Parallel Development Containers for AI Coding Agents

**Safe parallel work with multiple AI coding agents.** Run Claude Code, Codex,
Amp CLI, Gemini CLI, Pi, and other tools in isolated containers without branch
conflicts, file interference, or environmental headaches.

## The Problem

AI coding agents work best when given full command execution (YOLO mode). But running them locally is risky:

- **Branch conflicts** - Agent changes on one branch interfere with your local checkout
- **Test conflicts** - Running tests simultaneously causes failures and race conditions
- **Environment pollution** - Dependencies and state accumulate, causing hidden bugs
- **Context switching** - Switching branches while an agent is working loses progress

## The Solution

`md` gives each AI agent a **complete, isolated container** with a full git clone. You can:

- Run agents on multiple branches simultaneously
- Switch local branches without affecting running agents
- Run tests in parallel without conflicts
- Keep your local checkout clean
- Delete containers cleanly when done

## Quick Start

```bash
# Start container for your current branch
git checkout -b wip origin/main
md start

# SSH in and run your coding agent, where "wip" is the branch associated with this container
ssh md-<repo>-wip
amp
# Exit from the ssh session into the container (or use a separate terminal)
exit

# Pull changes back when done
md pull
# Verify changes
git log -1

# Clean up the container
md kill
```

## Installation

```bash
git clone https://github.com/maruel/md
export PATH="$PATH:$(pwd)/md"
```

**Recommended:** Also install [git-maruel](https://github.com/maruel/git-maruel) for the `git squash` and `git rb` helpers.

### Readme for agents (https://agents.md)

**For coding agents:** The container includes `/home/user/AGENTS.md` which provides information about
preinstalled tools and system configuration. Reference it in your AGENTS.md to help agents understand the
available development environment. Here's the locations:

- [amp](https://ampcode.com/manual#AGENTS.md): `~/.config/amp/AGENTS.md`
- [claude](https://www.anthropic.com/engineering/claude-code-best-practices): `~/.claude/CLAUDE.md`
- [codex](https://developers.openai.com/codex/guides/agents-md/): `~/.codex/AGENTS.md`
- [gemini](https://geminicli.com/docs/cli/gemini-md/): `~/.gemini/GEMINI.md`
- [opencode](https://opencode.ai/docs/rules/): `~/.config/opencode/AGENTS.md`
- [pi](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/README.md): `~/.pi/agent/AGENTS.md`
- [qwen](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/settings/#example-context-file-content-eg-qwenmd): `~/.qwen/QWEN.md`

### Skills (https://agentskills.io)

- [amp](https://ampcode.com/manual#agent-skills): `~/.config/agents/skills/**/SKILL.md` (recursive)
- [claude](https://code.claude.com/docs/en/skills): `~/.claude/skill/<name>/SKILL.md`
- [codex](https://developers.openai.com/codex/skills): `~/.codex/skill/**/SKILL.md` (recursive)
- [gemini](https://geminicli.com/docs/cli/skills/): `~/.gemini/skill/<name>/SKILL.md`
- [opencode](https://opencode.ai/docs/skills/): `~/.config/opencode/skill/<name>/SKILL.md`
- [pi](https://github.com/badlogic/pi-mono/blob/main/packages/coding-agent/README.md#skills): `~/.pi/skill/**/SKILL.md` (recursive)
- [qwen](https://qwenlm.github.io/qwen-code-docs/en/users/features/skills/): `~/.qwen/skill/<name>/SKILL.md`

## How It Works

### Container Setup

Each container is named `md-<repo-name>-<branch-name>` with:

- **Isolated git clone** - `/app` inside the container is a git clone of your local repository. It tracks branch `base` which matches your local branch. This is useful for commit-happy agents like Codex to track pending changes.
- **User-mapped permissions** - Container runs as your local user ID for proper file permissions
- **SSH access** - Connect via `ssh md-<repo>-<branch>`
- **Minimal overhead** - Only sshd runs; no unnecessary background services

### Preinstalled Tools

- TypeScript, Go, Rust, Node.js, Python development environments
- Neovim editor
- Android SDK and ADB on x64
- And more! See [rsc/root/setup/](rsc/root/setup/) and [rsc/home/user/setup/](rsc/home/user/setup/).

### Configuration

Agent configurations and credentials are automatically mounted:

- Agent configurations: `~/.amp`, `~/.claude`, `~/.codex`, `~/.gemini`, `~/.pi`, `~/.qwen`,
  `~/.config/agents`, `~/.config/opencode`, `~/.local/state/opencode`, `~/.local/share/opencode`
- Android ADB keys: `~/.android`

Environment variables can be passed via:

1. `.env` file in your repository (auto-mapped)
2. `~/.config/md/env` on your local machine (applies to all containers)

For example:

```bash
# ~/.config/md/env
ANTHROPIC_API_KEY=your_key
OPENAI_API_KEY=your_key
```

## Commands

| Command | Purpose |
|---------|---------|
| `md start` | Create and start a container for the current branch |
| `md list` | List all md containers |
| `ssh md-<repo>-<branch>` | Access the container |
| `md diff` | Show all changes in the container (runs `git diff base`) |
| `md pull` | Pull changes from container back to local branch |
| `md push` | Push local changes to the container |
| `md kill` | Stop and remove the container |

## Contributing

Made with ❤️  by [Marc-Antoine Ruel](https://maruel.ca). Contributions are very appreciated! Thanks in advance! 🙏
