# rumpty-agent

`rumpty-agent` is a minimal guest-side component included in RumptyCloud images.

The first supported surface is local metrics extraction:

```bash
rumpty-agent metrics once --pretty
rumpty-agent daemon
```

The agent reports guest telemetry from Linux kernel interfaces such as `/proc` and `statfs`.
It does not read user files, environment variables, shell history, SSH keys, or application
secrets.

## Commands

```bash
rumpty-agent daemon [--vsock-cid 2] [--vsock-port 5000]
rumpty-agent metrics once [--pretty] [--sample-window 1s]
rumpty-agent version
```

`metrics once` returns JSON using the `rumpty.agent.metrics.v1` schema. CPU and network rates
are sampled over the configured `--sample-window`.

`daemon` samples metrics continuously and pushes bounded batches over VSOCK to a node-side
Rumpty metrics collector. It does not talk to the public backend and does not need backend
credentials inside the VM.

Default daemon settings:

```text
VSOCK CID:       2
VSOCK port:      5000
sample interval: 15s
flush interval:  30s
max batch size:  8 samples
```

If the collector is unavailable, the daemon keeps at most `max batch size` samples in memory
and drops the oldest sample first. This intentionally prefers bounded memory usage over
unbounded buffering inside the guest.

## Guest Image Verification

The systemd unit is intentionally hardened. After baking the agent into a golden image, verify
the unit on the real guest image because systemd sandboxing can vary by distro/version:

```bash
sudo bash deploy/systemd/verify-agent.sh
```

At minimum, confirm:

- `rumpty-agent metrics once` can read `/proc`, `statfs`, and network counters
- `systemd-analyze verify` accepts the unit
- `rumpty-agent.service` starts
- VSOCK dialing works when a collector is listening on CID `2`, port `5000`

The unit allows only `AF_UNIX` and `AF_VSOCK`. If a target image blocks the network interface
enumeration path used by Go, the agent falls back to `/proc/net/dev` and still reports non-loopback
interfaces.

## Development

```bash
go test ./...
go run ./cmd/rumpty-agent metrics once --pretty
```
