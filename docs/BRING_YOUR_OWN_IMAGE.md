# Bring your own image

Goal: make it possible to run `md start -image <anything>` against a base image md did not
build, instead of requiring the `ghcr.io/caic-xyz/md-*` images. Startup-script delivery is
already decoupled from the base image; this document records the remaining runtime-contract
and portability work.

## Motivation — this is really about a runtime-agnostic contract

"Bring your own image" is the near-term framing, but the load-bearing reason to decouple
`start.sh` from the base is to **let md target runtimes other than Docker/Podman later — virtual
machines (QEMU as one example, not exclusively: cloud-hypervisor, Firecracker, a cloud VM,
etc.).** A VM is not a container, and that difference is what should color the solution choices:

- **No container injection primitives.** A VM has no `docker exec`, no `docker cp`, no image
  layers, no `COPY --from` build context. The BuildKit **cache-injection** model is Docker-only
  and does not port to a VM.
- **A VM boots a real init.** You don't "override `CMD`"; the kernel starts systemd/openrc.
  Provisioning happens via cloud-init / ignition / a seed disk / first-boot SSH — i.e. *an init
  the system already runs a hook for*. This **inverts** the container trade-off: systemd's
  weight is a liability in a container but a given in a VM.
- **Much of `start.sh` is container-specific.** The `/proc` remount + unmask for nested rootless
  podman, the `uid_map` detection + `usermod -aG root`, kvm-GID matching, `--userns=keep-id` —
  these exist only because of container constraints. In a VM with a real kernel, real `/proc`,
  real root, they are moot and must self-skip, not error.

The common denominator across containers *and* VMs is exactly two things md already has or can
define: **SSH as the connection model**, and **a provisioning contract the target satisfies**.
So the VM goal pushes the design toward: (1) treat the **contract (Layer B2)** as the durable,
runtime-agnostic abstraction — the thing a container image *or* a VM disk image conforms to;
(2) prefer delivery via *an init the target runs* plus a transport the target supports (SSH, a
seed disk), treating Docker-build-time injection as a container-only fast path, not the core
mechanism; (3) keep `start.sh` robust and capability-guarded (Layer B3) so the *same* script
runs unmodified in a VM, where the container-only blocks simply don't trigger. See
"Implications of the VM target" before the recommendations for how this re-weights each choice.

## Current state

- Startup scripts live exclusively in `rsc/specialized/root/`; generated specialized images copy
  that seed and set `CMD ["/root/start.sh"]`. Base images carry no md entrypoint.
- The `rsc/` tree is embedded in the binary (`//go:embed all:rsc`, `build.go`), so the script
  bytes are available host-side at runtime.
- md connects by SSH as `user` to a published `127.0.0.1::22` (`launchContainer`,
  `container.go`; `ssh.go` hardcodes `User user`). `start.sh` is also pid-1 keep-alive
  (`service ssh start` then `sleep infinity`).

The remaining work is in the runtime contract and SSH model. Treat "any image" as **"any
conforming glibc Debian/Ubuntu-family bash image"** — musl/alpine and distroless break the
Go/Rust/node tooling and the `service`/`/etc/init.d` assumptions regardless of this work.

## Layer B — the runtime contract start.sh assumes (hard)

`start.sh` is glue over a fat Debian base. It needs the base to already provide:

| Need | Source today | Breaks on | Mandatory? |
|---|---|---|---|
| `bash`, root default user | — | distroless, non-root USER | **core** |
| `sshd`, `service ssh`, `sshd_config.d/md.conf` | root pkgs | alpine/distroless/minimal | **core** |
| `user` acct, UID 1000, `/home/user/.ssh` | `4_create_user.sh` | image lacking it; UID 1000 collision | **core** (provisionable) |
| `getent`, `usermod`/`groupmod`/`useradd`/`chpasswd` | root pkgs | busybox/musl | core-ish (needed to provision the above) |
| `dbus`, `/etc/init.d/dbus`, `dbus-launch` | root pkgs | non-Debian, no sysvinit | degradable (GUI session bus only) |
| Xvnc/XFCE (with `--display`) | root pkgs | missing | degradable (only if `--display`) |
| `tailscaled`/`tailscale` (with `--tailscale`) | `3_extrepo.sh` | missing | degradable (only if `--tailscale`) |
| `jq`, `findmnt` (util-linux) | root pkgs | busybox/musl | degradable (used by optional paths) |
| `BASH_ENV=/etc/bash_env` + `bash.d/*` | root config | unset on foreign image | degradable (PATH convenience) |

The right column is the lever: most of the contract is **optional**. Three approaches, which
compose — B3 shrinks the surface that B1 must install or B2 must validate:

**B1 — inject at specialized-build time.** Add `RUN` steps to the generated Dockerfile to
`useradd`, install `openssh-server`+`dbus`, drop `md.conf`, set `BASH_ENV`. Pitfalls: needs a
package manager (apt vs apk vs none) and network during the *per-user* build; today that build
is deliberately fast and uses `--pull=never`. It re-runs on every cache/base change. This
pushes base-build cost into specialized-build, reintroducing the cold-start latency the cache
injection design exists to avoid. Make it opt-in behind a flag if implemented at all.

**B2 — define a contract and validate it (recommended).** Document the required surface; probe
it at `md start` with a fast pre-flight; fail with a clear message otherwise. Cheap and honest.
"Any image" becomes "any conforming image."

Tasks (B2):
- [ ] Write the contract: bash, an sshd reachable on 22, `user`@UID 1000 with writable
      `/home/user`, glibc, root-capable first boot.
- [ ] Add a pre-flight probe: inspect the image (`docker run --rm <img> sh -c '...'`) for
      `getent passwd user`, `command -v sshd bash`, UID 1000. Fail fast with remediation text.
- [ ] Probe *requested* optional deps too and fail before container creation: `--display` ⇒
      Xvnc/XFCE present, `--tailscale` ⇒ tailscaled present. Failing at probe time is even
      cleaner than start.sh's in-container fail (no container to clean up).

**B3 — make `start.sh` degrade and self-provision (shrink the contract).** Rather than demand a
fat base, make the script tolerant of a thin one: skip what's optional, create what's missing
but mandatory. This is the highest-leverage approach because it *reduces* the contract B2 probes
and the packages B1 would install — ideally down to just `bash` + `sshd` + root-at-boot. Two
moves:

- **Detect-and-degrade for optional subsystems.** Gate each on capability + intent:
  `have() { command -v "$1" >/dev/null; }`. dbus, X/VNC, tailscale, kvm-GID match, USB/plugdev,
  `/proc` remount, `BASH_ENV` — wrap each. Crucial distinction: **silent** skip when the feature
  wasn't requested (no `MD_DISPLAY`); **hard fail** (non-zero exit) when it *was* requested but
  the dep is absent (`MD_DISPLAY=1` but no Xvnc). This matches `start.sh`'s existing
  "Intentionally fail-fast" stance: a warning the user has to notice in log noise is worse than
  an explicit failure at `md start`. So "degrade" means *only* "skip features the user didn't
  ask for" — a base without X still gives a working SSH dev box; asking for `--display` on that
  base errors out, it does not silently come up GUI-less.
- **Detect-and-provision for the mandatory few.** The `user` account and `/home/user`,
  `/home/user/.ssh`, `/run/md` dirs can be created at boot if absent (`getent passwd user ||
  useradd -m -u 1000 -s /bin/bash user`). Must be idempotent (re-runs on every revive). The
  specialized build uses UID/GID 1000 for rootless Podman and numeric host ownership for other
  runtimes. Boot still needs a `user` account initially at UID/GID 1000; non-rootless-Podman paths
  may rewrite it to the host identity. `sshd` itself is **not** boot-provisioned —
  installing a package on every boot is too slow and needs network; that stays a build-time (B1) or
  contract (B2) concern.

Tasks (B3):
- [ ] Add a `have()` guard and wrap dbus, tailscale, kvm-GID, USB, sudo/`proc` blocks; replace
      bare `usermod`/`groupmod`/`findmnt`/`jq` calls with guarded ones.
- [ ] Per optional block: "not requested" → silent skip; "requested but unavailable" → exit
      non-zero with a clear message naming the missing dep and the flag that needs it.
- [ ] Add idempotent user/dir provisioning at the top of `start.sh`, gated on `getent passwd user`.
- [ ] Expand smoke tests to a base matrix: full image, no-dbus, no-X, no-`user` (see pitfalls).

Pitfalls specific to B3:
- **Build-time COPY vs runtime user creation (chicken-and-egg).** The specialized Dockerfile
  copies user-owned files with numeric `--chown` (`generateDockerfile`, `client.go`): UID/GID 1000
  for rootless Podman, or the host identity for other runtimes. Numeric ownership avoids a
  build-time name lookup, but `start.sh` still needs a `user` account before SSH starts. Other
  runtimes may rewrite that account and repair critical paths. A root-owned staging path plus a
  boot-time move remains the purer "any image"
  route, but adds a runtime placement step.
- **UID collision.** If the foreign image already uses UID 1000 for a different account,
  `useradd -u 1000` fails and cache ownership (chowned to 1000 at build) is wrong. Detect and
  fail loudly, or pick/echo a UID and feed it back into the chown — but a dynamic UID undermines
  the prebuilt-cache model. Easiest honest answer: require UID 1000 free, validate in B2.
- **Username is hardcoded `user`.** md assumes `user` across `ssh.go` (`User user`) and git
  remotes (`user@<name>:`). B3 force-creates `user` even if the base ships its own `node`/
  `ubuntu`/`vscode` account. Adapting to the image's existing user instead would touch `ssh.go`,
  remote URLs, and every `chown user:user` — larger change, deferred. Note it as a Layer-C-
  adjacent option.
- **Complexity / silent-failure surface.** More branches mean more to test and more paths that
  can mask real breakage. The fail-on-request discipline above is what keeps degradation safe:
  silent-skip is allowed *only* for features the user didn't request. Without that rule B3 would
  trade clear "missing dep" errors for confusing half-working containers.

---

## Layer C — the SSH model itself (architectural ceiling)

md *is* "SSH into a long-lived container as `user`": publishes `127.0.0.1::22`, hardcodes
`User user`, every git remote is `user@<name>:...`, and `start.sh` supplies both sshd and the
`sleep infinity` keep-alive. A foreign image's own `CMD` would run instead, likely exit
immediately, and carry no sshd.

Consequence: md **always** replaces the base image's `CMD`. You cannot honor a foreign
entrypoint *and* keep md's connection model. Document this as an invariant rather than trying
to solve it. If honoring foreign entrypoints ever matters, it requires a sidecar/bootstrap that
supplies keep-alive + sshd independently of the base — out of scope here.

---

## Cross-cutting pitfalls

- **UID/ownership**: the generated Dockerfile numerically owns caches and dirs. Rootless Podman
  keeps `user` at UID/GID 1000 and maps the host user there. Other runtimes may move `user` to the
  host UID/GID and repair `/home/user` ownership without crossing bind mounts. A foreign image still
  needs a UID/GID-1000 `user` account and a Debian-compatible user/group toolchain.
- **Debian-isms in start.sh**: `service ssh start`, `/etc/init.d/dbus`, apt layout. Porting to
  non-Debian means rewriting these, not just installing packages.
- **Privileged first boot**: `groupmod` (kvm/plugdev GID match), `/proc` remount (nested
  podman), `chpasswd` (sudo) all need root + caps even if the image's default USER is non-root.
- **Tests pin the layout**: build tests assert the specialized image `CMD` and staged startup
  scripts. Any move updates these.

---

## Implications of the VM target (re-weighting the choices)

Holding the runtime-agnostic goal next to the remaining analysis shifts the verdicts:

- **Layer B — the contract is the trunk.** B2 stops being merely "recommended" and becomes the
  abstraction that makes a container image and a VM image interchangeable. Write it
  runtime-neutrally (bash, sshd, `user`@1000, an init that runs our bootstrap, root-at-first-
  boot) and avoid Docker-specific phrasing.
- **Layer B3 — guards are what make one `start.sh` run everywhere.** The container-only blocks
  (`/proc` unmask, `uid_map`/`usermod -aG root`, kvm-GID, `--userns=keep-id`) must be gated on
  *the condition that makes them necessary*, not just tool presence — so they self-skip in a VM
  rather than firing pointlessly. This is a stronger reason to do B3 than the container story
  alone gave.
- **Cache injection is Docker-only and needs a parallel VM path.** `COPY --from=cache-*` /
  `buildSpecializedImage` / `userImageName()` do not translate. A VM seeds caches via a mounted
  disk, virtiofs/9p, rsync-over-SSH, or baking into the disk image. Out of scope here, but the
  contract should not assume the container cache mechanism.
- **B1 (install at build) is the least portable.** It is inherently a container-image-build
  step. A VM equivalent is "build the disk image" — a different pipeline entirely. Keep B1 an
  opt-in container fast path, not a load-bearing dependency.
- **Pre-flight probe needs a transport-neutral form.** `docker run --rm <img>` to probe the
  contract is container-only; for a VM the equivalent is checking the image/manifest or probing
  over the first SSH connection. Same contract, different inspection transport.

## Recommended sequencing

Ordered so the runtime-agnostic trunk (the contract + an init-run hook + a portable `start.sh`)
lands first, and container-only conveniences stay leaves:

1. **Layer B3** — make `start.sh` degrade-and-self-provision, gating container-only blocks on
   the condition that makes them necessary so the same script runs in a VM. Do this *before* B2,
   because it shrinks the contract B2 has to validate down to roughly `bash` + `sshd` +
   root-at-boot. Incremental and testable per optional subsystem; biggest leverage per unit work.
2. **Layer B2** — write the (now-smaller) contract doc + add the pre-flight probe. Makes "bring
   your own image" real and safe with no per-image install cost.
3. **Layer B1** — only if minimal bases are genuinely needed; opt-in flag, accept build cost.
4. **Layer C** — leave as a documented invariant: md owns pid 1 and replaces the base `CMD`.

## Observations for later

- The four VNC/monitor scripts only matter under `--display`. Consider COPYing them only when
  display is requested, to keep non-display specialized images smaller.
- `start.sh` mixes concerns (perms, dbus, vnc, tailscale, sshd, nested-podman /proc fixups).
  Splitting into composable units would make a non-Debian port tractable, let the pre-flight
  probe reuse the same dependency list, and make the B3 per-subsystem guards fall out naturally.
- B3's degrade guards and B2's probe want the *same* capability list. Generating both — guards,
  probe, and docs — from one machine-readable manifest avoids drift between "what we skip" and
  "what we require".
- A machine-readable manifest of the contract (the table in Layer B) could drive both the
  pre-flight probe and the docs from one source.

### Base-image user invariant

The bundled base image explicitly creates `user` with UID/GID 1000. Bring-your-own images must
provide the same account identity so rootless Podman can map the host user to it without an
expensive recursive ownership repair.
