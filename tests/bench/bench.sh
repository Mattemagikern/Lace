#!/usr/bin/env bash
# Benchmark lace throughput over SSH to localhost.
#
# Strips network variability (LAN/WiFi/jitter) but keeps everything else real:
# the same SSH ciphers, the same protocol, the same disk syscalls. Useful for
# comparing program changes; not useful for predicting LAN throughput.
#
# Caveat: server and client share CPU on localhost, so compression-bound
# workloads see slightly worse numbers than two real machines would.

set -euo pipefail

LACE="${LACE:-lace}"
SSH_TARGET="${SSH_TARGET:-$USER@localhost}"
WORKDIR="${WORKDIR:-/tmp/lace-bench}"
KEEP="${KEEP:-0}"
RUNS="${RUNS:-1}"
PROFILE="${1:-mixed}"

usage() {
    cat <<EOF
Usage: $0 [large|medium|small|mixed]

Profiles (each ~1 GiB total):
  large    10 x 100 MB                  bandwidth + compression bound
  medium   1000 x 1 MB                  balanced
  small    20000 x 50 KB                per-file overhead bound
  mixed    5x100MB + 500x1MB + 50000x10KB

Env vars:
  LACE         path to lace binary           (default: ./lace)
  SSH_TARGET   ssh destination               (default: \$USER@localhost)
  WORKDIR      benchmark scratch dir         (default: /tmp/lace-bench)
  RUNS         number of timed runs          (default: 1)
  KEEP         keep scratch dir after run    (default: 0)

Pre-reqs:
  - sshd running locally with key auth: 'ssh \$SSH_TARGET true' must work.
  - lace installed on the remote PATH (see README for setup).
EOF
}

case "${1:-}" in
    -h|--help) usage; exit 0 ;;
esac

if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH_TARGET" true 2>/dev/null; then
    echo "Cannot ssh non-interactively to $SSH_TARGET." >&2
    echo "  ssh-keygen && ssh-copy-id $SSH_TARGET" >&2
    exit 1
fi

if ! ssh "$SSH_TARGET" 'command -v lace >/dev/null'; then
    echo "lace not found on remote PATH for $SSH_TARGET." >&2
    echo "  install it on the remote host first (see README)." >&2
    exit 1
fi

src="$WORKDIR/src"
dst="$WORKDIR/dst"

gen_chunk() {
    local total_bytes=$1
    local file_count=$2
    local subdir=$3
    local per=$(( total_bytes / file_count ))

    mkdir -p "$src/$subdir"
    head -c "$total_bytes" /dev/urandom > "$WORKDIR/.pool"
    ( cd "$src/$subdir" && split -b "$per" -d -a 6 "$WORKDIR/.pool" "f-" )
    rm "$WORKDIR/.pool"
}

generate() {
    rm -rf "$WORKDIR"
    mkdir -p "$src" "$dst"
    case "$PROFILE" in
        large)  gen_chunk $((1024*1024*1024)) 10    large ;;
        medium) gen_chunk $((1024*1024*1024)) 1000  medium ;;
        small)  gen_chunk $((1024*1024*1024)) 20000 small ;;
        mixed)
            gen_chunk $((500*1024*1024)) 5     mixed/big
            gen_chunk $((300*1024*1024)) 300   mixed/med
            gen_chunk $((200*1024*1024)) 50000 mixed/sm
            ;;
        *) echo "Unknown profile: $PROFILE" >&2; exit 1 ;;
    esac
}

drop_cache() {
    sync
    if sudo -n true 2>/dev/null; then
        sudo sh -c 'echo 3 > /proc/sys/vm/drop_caches'
    fi
}

human_bytes() {
    numfmt --to=iec-i --suffix=B "$1" 2>/dev/null || echo "$1 B"
}

run_once() {
    rm -rf "$dst"
    mkdir -p "$dst"
    drop_cache

    local start_ns end_ns
    start_ns=$(date +%s%N)
    "$LACE" "$src" "$SSH_TARGET:$dst" >/dev/null
    end_ns=$(date +%s%N)
    echo $((end_ns - start_ns))
}

echo "===== Generating dataset: $PROFILE ====="
generate

bytes=$(du -sb "$src" | awk '{print $1}')
files=$(find "$src" -type f | wc -l)

echo
echo "===== Benchmark: $PROFILE ====="
printf "Files:   %s\n" "$files"
printf "Bytes:   %s (%s)\n" "$bytes" "$(human_bytes "$bytes")"
printf "Target:  %s\n" "$SSH_TARGET"
printf "Workdir: %s\n" "$WORKDIR"
if sudo -n true 2>/dev/null; then
    echo "Cache:   page cache dropped before each run"
else
    echo "Cache:   no sudo; not dropped (warm runs after first)"
fi
echo

best=0
for ((i=1; i<=RUNS; i++)); do
    elapsed_ns=$(run_once)
    elapsed_s=$(awk -v ns="$elapsed_ns" 'BEGIN{printf "%.3f", ns/1e9}')
    mbps=$(awk -v b="$bytes" -v ns="$elapsed_ns" 'BEGIN{printf "%.2f", b/1048576/(ns/1e9)}')
    fps=$(awk -v f="$files" -v ns="$elapsed_ns" 'BEGIN{printf "%.0f", f/(ns/1e9)}')
    printf "Run %d/%d: %ss  %s MB/s  %s files/s\n" "$i" "$RUNS" "$elapsed_s" "$mbps" "$fps"
    if [[ $best -eq 0 || $elapsed_ns -lt $best ]]; then
        best=$elapsed_ns
    fi
done

if (( RUNS > 1 )); then
    best_s=$(awk -v ns="$best" 'BEGIN{printf "%.3f", ns/1e9}')
    best_mbps=$(awk -v b="$bytes" -v ns="$best" 'BEGIN{printf "%.2f", b/1048576/(ns/1e9)}')
    echo
    printf "Best:    %ss  %s MB/s\n" "$best_s" "$best_mbps"
fi

if [[ "$KEEP" != "1" ]]; then
    rm -rf "$WORKDIR"
fi
