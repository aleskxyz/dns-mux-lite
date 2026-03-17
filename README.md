# dns-mux-lite

`dns-mux-lite` is a simple DNS proxy that load-balances queries across a list of upstream resolvers, with health tracking tuned for DNS tunneling use cases.

It is a stripped-down version of the main DNS Multiplexer: **proxy-only**, **UDP-only**, no tunnel management, and no cache.

## Features

- UDP DNS proxy (listens on a single address:port)
- Round-robin selection across upstream resolvers, with one retry on failure
- Active health tracking:
  - Startup compatibility probe using tests 0–6 (NS/TXT/random subdomain/DPI/EDNS/NXDOMAIN)
  - Live traffic evicts resolvers after repeated failures
  - Background health checks periodically re-test only failed resolvers and re-add recovered ones
- Supports hundreds of upstream resolvers

## Build

```bash
go build -o dns-mux-lite .
```

## Usage

```bash
./dns-mux-lite \
  --listen=0.0.0.0:53 \
  --resolvers-file=resolvers.txt \
  --scan-domain=google.com \
  --health-check=true \
  --health-interval=1m \
  --log-level=INFO
```

### Flags

- `--listen` (string, default `0.0.0.0:53`): UDP listen address for the proxy.
- `--resolvers-file` (string, **required**): Path to the resolvers list file.
- `--scan-domain` (string, default `google.com`): Domain used for compatibility tests 0–6.
- `--health-check` (bool, default `true`): Enable background health checks for failed resolvers.
- `--health-interval` (duration, default `1m`): Interval between background health checks (e.g. `30s`, `1m`, `5m`).
- `--log-level` (string, default `INFO`): `DEBUG`, `INFO`, `WARN`, or `ERROR`.

### Resolvers file format

`--resolvers-file` expects a plain text file with one resolver per line:

- `IP` or `IP:port` (IPv4 only for now)
- Lines starting with `#` are comments and are ignored
- If no port is specified, `:53` is assumed

Example:

```text
# resolvers.txt
8.8.8.8
1.1.1.1:53
9.9.9.9
```

## Docker

A multi-stage `Dockerfile` is provided in this directory.

Build a local image:

```bash
docker build -t dns-mux-lite .
```

Run:

```bash
docker run --rm \
  -p 53:53/udp \
  -v $(pwd)/resolvers.txt:/etc/dns-mux-lite/resolvers.txt:ro \
  ghcr.io/aleskxyz/dns-mux-lite:latest \
  --listen=0.0.0.0:53 \
  --resolvers-file=/etc/dns-mux-lite/resolvers.txt
```

## License

`dns-mux-lite` is licensed under the GNU General Public License v3.0. See `LICENSE` for details.

This project is inspired by and simplified from [`anonvector/DNS-Multiplexer`](https://github.com/anonvector/DNS-Multiplexer).


