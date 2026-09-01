#!/bin/sh
set -eu

repo="${REPO:-lscythe/domain-probe}"
version="${VERSION:-latest}"
install_dir="${INSTALL_DIR:-$HOME/.local/bin}"
os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  arm64|aarch64) arch=arm64 ;;
  *) echo "unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in darwin|linux) ;; *) echo "unsupported OS: $os" >&2; exit 1 ;; esac
if [ "$version" = latest ]; then version=$(curl -fsSL "https://api.github.com/repos/$repo/releases/latest" | sed -n 's/.*"tag_name": "\([^"]*\)".*/\1/p') ; fi
[ -n "$version" ] || { echo "could not determine release" >&2; exit 1; }
archive="domain-probe_${version#v}_${os}_${arch}.tar.gz"
tmp=$(mktemp -d); trap 'rm -rf "$tmp"' EXIT
curl -fsSL "https://github.com/$repo/releases/download/$version/$archive" -o "$tmp/$archive"
tar -xzf "$tmp/$archive" -C "$tmp"
mkdir -p "$install_dir"
install "$tmp/domain-probe" "$install_dir/domain-probe"
printf 'installed domain-probe %s to %s\n' "$version" "$install_dir/domain-probe"
