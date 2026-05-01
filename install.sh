#!/usr/bin/env bash
set -euo pipefail

REPO="${MAINLINE_REPO:-mainline-org/mainline}"
VERSION="${MAINLINE_VERSION:-}"
INSTALL_DIR="${MAINLINE_INSTALL_DIR:-}"

log() {
  printf '%s\n' "$*"
}

fail() {
  printf 'mainline install: %s\n' "$*" >&2
  exit 1
}

have() {
  command -v "$1" >/dev/null 2>&1
}

fetch() {
  if have curl; then
    curl -fsSL "$1"
    return
  fi
  if have wget; then
    wget -qO- "$1"
    return
  fi
  fail "需要 curl 或 wget"
}

download_to() {
  if have curl; then
    curl -fsSL "$1" -o "$2"
    return
  fi
  if have wget; then
    wget -qO "$2" "$1"
    return
  fi
  fail "需要 curl 或 wget"
}

compute_sha256() {
  if have sha256sum; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  if have shasum; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  fail "需要 sha256sum 或 shasum"
}

detect_os() {
  case "$(uname -s)" in
    Linux) printf 'linux' ;;
    Darwin) printf 'darwin' ;;
    *) fail "暂不支持当前系统: $(uname -s)" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64' ;;
    arm64|aarch64) printf 'arm64' ;;
    *) fail "暂不支持当前架构: $(uname -m)" ;;
  esac
}

resolve_version() {
  if [ -n "$VERSION" ]; then
    printf '%s' "$VERSION"
    return
  fi

  latest_json="$(fetch "https://api.github.com/repos/$REPO/releases/latest")"
  tag="$(printf '%s' "$latest_json" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n1)"
  if [ -z "$tag" ]; then
    fail "无法解析最新 release 版本"
  fi
  printf '%s' "$tag"
}

default_install_dir() {
  if [ -n "$INSTALL_DIR" ]; then
    printf '%s' "$INSTALL_DIR"
    return
  fi

  for candidate in /usr/local/bin /opt/homebrew/bin; do
    if [ -d "$candidate" ] && [ -w "$candidate" ]; then
      printf '%s' "$candidate"
      return
    fi
  done

  printf '%s' "$HOME/.local/bin"
}

main() {
  os="$(detect_os)"
  arch="$(detect_arch)"
  version="$(resolve_version)"
  install_dir="$(default_install_dir)"

  archive="mainline_${version#v}_${os}_${arch}.tar.gz"
  base_url="https://github.com/$REPO/releases/download/$version"

  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  archive_path="$tmpdir/$archive"
  checksums_path="$tmpdir/checksums.txt"

  log "Downloading $archive"
  download_to "$base_url/$archive" "$archive_path"
  download_to "$base_url/checksums.txt" "$checksums_path"

  expected="$(awk -v name="$archive" '$2 == name { print $1 }' "$checksums_path")"
  [ -n "$expected" ] || fail "release checksums 中没有 $archive"

  actual="$(compute_sha256 "$archive_path")"
  [ "$expected" = "$actual" ] || fail "checksum 校验失败"

  mkdir -p "$tmpdir/unpack" "$install_dir"
  tar -xzf "$archive_path" -C "$tmpdir/unpack"
  install -m 0755 "$tmpdir/unpack/mainline" "$install_dir/mainline"

  log "Installed mainline to $install_dir/mainline"
  if ! printf '%s' ":$PATH:" | grep -q ":$install_dir:"; then
    log "注意：$install_dir 还不在 PATH 里"
  fi
}

main "$@"
