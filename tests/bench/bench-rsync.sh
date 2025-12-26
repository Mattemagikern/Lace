#!/usr/bin/env bash
# Benchmark rsync throughput over SSH to localhost.
#
# Mirrors bench.sh exactly so the two are directly comparable. Defaults to
# rsync's whole-file mode (-aW) since lace doesn't do delta transfer either —
# delta would be an apples-to-oranges advantage for rsync. Override RSYNC_FLAGS
# to test other modes.
#
# Reuses WORKDIR/src if it already contains files, so you can do:
#   KEEP=1 ./bench/bench.sh mixed
#   ./bench/bench-rsync.sh mixed
# and both tools run on the exact same data.

set -euo pipefail

RSYNC="${RSYNC:-rsync}"
RSYNC_FLAGS="${RSYNC_FLAGS:--aW}"
SSH_CIPHER="${SSH_CIPHER:-aes128-gcm@openssh.com}"
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
  RSYNC         path to rsync                 (default: rsync)
  RSYNC_FLAGS   rsync mode flags              (default: -aW)
                  -aW   archive + whole-file (no delta) — fair vs lace
                  -a    archive + delta — rsync's default; wins on edits
                  -aWz  archive + whole-file + zlib compression
  SSH_CIPHER    SSH cipher                    (default: aes128-gcm@openssh.com)
  SSH_TARGET    ssh destination               (default: \$USER@localhost)
  WORKDIR       benchmark scratch dir         (default: /tmp/lace-bench)
  RUNS          number of timed runs          (default: 1)
  KEEP          keep scratch dir after run    (default: 0)

Pre-reqs:
  - sshd running locally with key auth: 'ssh \$SSH_TARGET true' must work.
  - rsync on local and remote PATH.
EOF
}

case "${1:-}" in
    -h|--help) usage; exit 0 ;;
esac

if ! command -v "$RSYNC" >/dev/null; then
    echo "rsync not found at: $RSYNC" >&2
    exit 1
fi

if ! ssh -o BatchMode=yes -o ConnectTimeout=5 "$SSH_TARGET" true 2>/dev/null; then
    echo "Cannot ssh non-interactively to $SSH_TARGET." >&2
    echo "  ssh-keygen && ssh-copy-id $SSH_TARGET" >&2
    exit 1
fi

if ! ssh "$SSH_TARGET" 'command -v rsync >/dev/null'; then
    echo "rsync not found on remote $SSH_TARGET." >&2
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
    "$RSYNC" $RSYNC_FLAGS -e "ssh -c $SSH_CIPHER" "$src/" "$SSH_TARGET:$dst/" >/dev/null
    end_ns=$(date +%s%N)
    echo $((end_ns - start_ns))
}

if [[ -d "$src" ]] && [[ -n "$(find "$src" -mindepth 1 -maxdepth 4 -type f -print -quit 2>/dev/null)" ]]; then
    echo "===== Reusing existing dataset in $src ====="
else
    echo "===== Generating dataset: $PROFILE ====="
    generate
fi

bytes=$(du -sb "$src" | awk '{print $1}')
files=$(find "$src" -type f | wc -l)

echo
echo "===== Benchmark (rsync): $PROFILE ====="
printf "Files:        %s\n" "$files"
printf "Bytes:        %s (%s)\n" "$bytes" "$(human_bytes "$bytes")"
printf "Target:       %s\n" "$SSH_TARGET"
printf "Workdir:      %s\n" "$WORKDIR"
printf "Rsync flags:  %s\n" "$RSYNC_FLAGS"
printf "SSH cipher:   %s\n" "$SSH_CIPHER"
if sudo -n true 2>/dev/null; then
    echo "Cache:        page cache dropped before each run"
else
    echo "Cache:        no sudo; not dropped (warm runs after first)"
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
