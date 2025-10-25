# md

My Development container: enables you to work with multiple coding agents in parallel safely.

The goal of this project is to run common coding again tools (Claude Code, Codex, Amp CLI, Gemini CLI, Qwen
CLI, etc) in what is generally called _YOLO mode_ in a safe manner. Instead of using git worktree, you use
normal branches, enabling a simpler workspace. The container has a **complete separate git clone**, your local
checkout is not mapped in the container. Because each branch runs in a container, you can safely do multiple
changes in parallel, run test in parallel and not have branch switching issues. When you are done, just delete
the container!

## Usage

Each container has the name `md-<repo-name>-<branch-name>`. Thus if you are on a git checkout of the
repository [github.com/maruel/genai](https://github.com/maruel/genai), on a branch named `wip`, then the
container is named `md-genai-wip`.

You access the container via ssh. So to access the container, you do:

```
ssh md-genai-wip
```

Frequently, you'll use two ssh sessions, one to run the coding agent (cc, codex, etc), one to inspect the
results (e.g. `git diff`) and run tests or so manual fixups.

To pull changes from the container back in your local branch, you do:

```
md-pull
```

To push local changes to the container, let's say you rebased on `origin/main` and need to push that back into
the container, you do:

```
md-push
```

## Installation

`curl | sudo bash`? lol no, just clone and add to PATH:

```
git clone https://github.com/maruel/md
PATH=$PATH:$(pwd)/md
```

It is **highly recommended** to setup the git alias `git squash` from
[squash.ini](https://github.com/maruel/bin_pub/blob/main/configs/.config/git/squash.ini).


## Details

The container runs as account `user` that is mapped to your user ID.

The files under [rsc/](/rsc) are mapped as-is inside the container. The following files are generated:

- `rsc/etc/ssh/ssh_host_ed25519_key`
- `rsc/etc/ssh/ssh_host_ed25519_key.pub`
- `rsc/home/user/.ssh/authorized_keys`

Each container has host ssh keys that are used to make sure the container is what we expect. It does map a few things:

- `~/.amp`
- `~/.codex`
- `~/.claude`
- `~/.gemini`
- `~/.qwen`
- `~/.config/amp`
- `~/.config/goose`
- `~/.local/share/amp`
- `~/.local/share/goose`

For most of these, you need to manually tell it to use YOLO mode.

The container runs basically nothing, only sshd, for maximum efficiency. The container has everything needed
to do TypeScript, Go and Rust development preinstalled. It comes with Neovim.

Want a feature? Please send a PR!
