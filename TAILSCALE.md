# Tailscale Integration

The `--tailscale` flag enables [Tailscale](https://tailscale.com) networking inside the container, allowing SSH access from any machine on your tailnet.

## Setup

### 1. Create an API Access Key

Create a key at https://login.tailscale.com/admin/settings/keys

- Select "API access token"
- Save the key

Set it in your environment:

```bash
export TAILSCALE_API_KEY=tskey-api-...
```

Without this key, you'll need to authenticate via browser each time you start a container.

### 2. Configure ACL Policy

Edit your ACL policy at https://login.tailscale.com/admin/acls

Add the `tag:md` tag owner:

```json
"tagOwners": {
  "tag:md": ["your-email@example.com"],
},
```

Add an SSH rule for `tag:md` nodes:

```json
"ssh": [
  {
    "action": "accept",
    "src":    ["autogroup:members"],
    "dst":    ["tag:md"],
    "users":  ["autogroup:nonroot"],
  },
],
```

## Usage

Start a container with Tailscale:

```bash
md start --tailscale
```

The container tailscale host name will often have a dash number suffix, e.g. `-2`, so look at the FQDN that is printed when you ssh in. You can also find it with `tailscale status`

### SSH Access

From any machine on your tailnet:

```bash
ssh user@<host>.<tailnet>.ts.net
```

SSH requires specifying the username. To avoid typing `user@` every time, add to your `~/.ssh/config`:

```
Host md-*.*.ts.net
    User user
```

Then you can simply:

```bash
ssh <host>.<tailnet>.ts.net
```

### VNC

Install a VNC client. Start a VNC session:

```bash
vncviewer <host>.<tailnet>.ts.net:5901
```

### Cleanup

When you run `md kill`:

- Ephemeral nodes (created with API key) are automatically removed from the tailnet
- Browser-authenticated nodes are deleted via the API (requires `TAILSCALE_API_KEY`)
