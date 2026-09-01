# domain-checker

A fast terminal utility for checking domain availability across RDAP and WHOIS, with pricing and lifecycle details.

## Features

- RDAP lookups with WHOIS fallback
- Expand bare names across selected TLDs
- Detect available, taken, expiring, and dropping domains
- Show first-year and renewal pricing from Porkbun
- Optional Dynadot aftermarket auction listings
- Interactive terminal browser for larger result sets
- Plain tab-separated output for scripts and pipelines

## Install

### Install the latest release

```sh
curl -fsSL https://raw.githubusercontent.com/lscythe/domain-checker/main/scripts/install.sh | sh
```

The script installs the latest archive into `~/.local/bin` by default. Override the destination with `INSTALL_DIR`:

```sh
INSTALL_DIR=/usr/local/bin curl -fsSL https://raw.githubusercontent.com/lscythe/domain-checker/main/scripts/install.sh | sh
```

### Go install

```sh
go install github.com/lscythe/domain-checker/cmd/domain-checker@latest
```

### Build from source

Requires Go 1.24 or newer.

```sh
git clone https://github.com/lscythe/domain-checker.git
cd domain-checker
go build -o domain-checker ./cmd/domain-checker
```

## Usage

```sh
domain-checker example
# Checks example.com, example.net, example.org, example.io, and example.dev

domain-checker example.com example.dev

domain-checker -tld popular acme

domain-checker -f names.txt
cat names.txt | domain-checker

domain-checker -plain acme.com example.dev
```

A token containing a dot is checked as-is. Bare names are expanded across the TLDs selected with `-tld`. Use `-tld all` to query every TLD with an RDAP service.

When running interactively, more than 20 results opens the browser view. Press `/` to filter, Enter to open a purchase page, and `q` to quit. Use `-table` to force a static table.

### Flags

| Flag | Description | Default |
| --- | --- | --- |
| `-tld` | TLD list, `popular`, or `all` | `com,net,org,io,dev` |
| `-f` | File containing one name or domain per line | — |
| `-c` | Concurrent lookups | `8` |
| `-plain` | Tab-separated output without styling | `false` |
| `-table` | Force the static table | `false` |
| `-timeout` | Per-lookup timeout | `10s` |
| `-auction` | Include Dynadot aftermarket listings | `false` |
| `-expiring-in` | Mark domains expiring within this window | `720h` |

### Auction listings

Auction lookups require a Dynadot API key:

```sh
DYNADOT_API_KEY=your-key domain-checker -auction example
```

## Development

```sh
go test ./...
go vet ./...
go build ./cmd/domain-checker
```

Releases are published automatically when a version tag such as `v1.0.0` is pushed. GitHub Actions builds archives for Linux and macOS on amd64 and arm64, then publishes checksums.

## License

Copyright © 2026 lscythe. Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE).
