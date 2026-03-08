# Container Outbound Network Restrictions

The `md` container uses Docker's default bridge network, which allows full outbound internet
access. Port bindings are `127.0.0.1`-only (inbound restriction), but outbound is unrestricted.

The following options are available to restrict outbound connectivity.

## Host OS trade-offs

### Linux

Docker runs natively; the container's virtual network interfaces (`docker0`, `veth*`) are
visible directly to the host kernel. This makes host-side iptables/nftables rules (options 2
and 4) straightforward and low-overhead. In-container rules (option 3) also work without
caveats. All six options are fully supported.

### macOS

Docker Desktop (and alternatives such as OrbStack, Colima) run containers inside a Linux VM.
The host macOS kernel has no visibility into the VM's network interfaces, so:

- **Options 2 and 4** (host-side iptables, transparent proxy) must be applied _inside the
  VM_, not on macOS directly. This requires shelling into the VM, which is fragile and
  reset on VM restart.
- **Option 3** (in-container rules) works normally — it is the most practical host-agnostic
  approach on macOS.
- **Option 1** (`--internal` network) and **option 5** (custom DNS) work normally.
- **Option 6** (Tailscale ACLs) works normally and is the most operationally convenient
  choice when Tailscale is already in use.

### Windows

Same constraints as macOS: Docker Desktop runs inside a WSL 2 Linux VM (or Hyper-V). The
Windows host has no direct access to the container bridge interface. Additionally:

- Windows Defender Firewall rules do not apply to WSL 2 VM traffic.
- Host-side filtering (options 2 and 4) requires rules inside the WSL 2 VM, which are lost
  on WSL restart unless scripted into WSL startup.
- **Option 3** (in-container nftables) is again the most reliable portable approach.
- **Options 1, 5, and 6** work normally regardless of host OS.

## 1. Docker `--internal` network

Create a Docker network with no outbound routing:

```bash
docker network create --internal restricted
```

Blocks all outbound traffic at the Docker level. Breaks SSH access unless combined with a
host-side proxy or Tailscale.

## 2. Host-side iptables/nftables rules

Add rules on the host targeting the container's bridge interface (e.g. `docker0`) to
allowlist or blocklist specific destinations:

```bash
# Example: block all outbound from container subnet except api.anthropic.com
iptables -I FORWARD -s 172.17.0.0/16 -j DROP
iptables -I FORWARD -s 172.17.0.0/16 -d <resolved-ip> -j ACCEPT
```

Managed entirely on the host; no container changes required.

## 3. In-container nftables/iptables rules

Run firewall rules at container startup in `rsc/root/start.sh`. Requires `--cap-add=NET_ADMIN`
(already added when `--tailscale` is used). Keeps policy inside the container definition and
allows per-container customization.

## 4. Transparent proxy (HTTP/HTTPS filtering)

Run a filtering proxy (e.g. Squid, mitmproxy) on the host. Redirect container traffic via
iptables transparent proxy rules and set `http_proxy`/`https_proxy` env vars. Enables
URL-based allowlisting and request logging.

## 5. DNS-based blocking

Set a custom `--dns` server that only resolves allowed domains, combined with an iptables rule
blocking direct IP connections:

```bash
docker run --dns=<filtering-dns-ip> ...
```

Simpler to manage but bypassable by hardcoded IP addresses.

## 6. Tailscale ACLs

When the container is connected via Tailscale (`--tailscale`), outbound access can be
controlled through [Tailscale ACL policies](https://tailscale.com/kb/1018/acls/) in the
tailnet admin console. Applies uniformly across all containers sharing the tailnet.
