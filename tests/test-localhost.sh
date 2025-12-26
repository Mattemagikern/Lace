#!/usr/bin/env bash
set -euo pipefail

LACE="${LACE:-lace}"
SSH_TARGET="${SSH_TARGET:-$USER@localhost}"
WORKDIR="${WORKDIR:-/tmp/lace-test}"
KEEP="${KEEP:-0}"

pass=0
fail=0
failed_tests=()

red()   { printf '\033[31m%s\033[0m' "$*"; }
green() { printf '\033[32m%s\033[0m' "$*"; }
bold()  { printf '\033[1m%s\033[0m' "$*"; }

usage() {
    cat <<EOF
Usage: $0

End-to-end correctness tests for lace over SSH to localhost.

Each test creates a source tree, transfers it with lace, and verifies the
destination matches byte-for-byte (sha256 manifest comparison).

Env vars:
  LACE         path to lace binary           (default: lace)
  SSH_TARGET   ssh destination               (default: \$USER@localhost)
  WORKDIR      scratch dir                   (default: /tmp/lace-test)
  KEEP         keep scratch dir after run    (default: 0)

Pre-reqs:
  - sshd running locally with key auth: 'ssh \$SSH_TARGET true' must work.
  - lace installed on the remote PATH (see README for setup).
EOF
}

case "${1:-}" in
    -h|--help) usage; exit 0 ;;
esac

if ! command -v "$LACE" >/dev/null 2>&1 && [[ ! -x "$LACE" ]]; then
    echo "lace not found at: $LACE" >&2
    exit 1
fi

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

manifest() {
    local root=$1
    if [[ ! -d "$root" ]]; then
        echo "MISSING-DIR"
        return
    fi
    ( cd "$root" && find . -type f -print0 \
        | LC_ALL=C sort -z \
        | xargs -0 -r sha256sum 2>/dev/null )
}

assert_match() {
    local name=$1 src=$2 dst=$3
    local sm dm
    sm=$(manifest "$src")
    dm=$(manifest "$dst")
    if [[ "$sm" == "$dm" ]]; then
        echo "  $(green PASS) $name"
        pass=$((pass + 1))
    else
        echo "  $(red FAIL) $name"
        diff <(echo "$sm") <(echo "$dm") | sed 's/^/      /' | head -20
        fail=$((fail + 1))
        failed_tests+=("$name")
    fi
}

assert_empty_dir() {
    local name=$1 dir=$2
    if [[ -d "$dir" ]] && [[ -z "$(ls -A "$dir" 2>/dev/null)" ]]; then
        echo "  $(green PASS) $name"
        pass=$((pass + 1))
    else
        echo "  $(red FAIL) $name (expected empty, got: $(ls -A "$dir" 2>/dev/null | head -3))"
        fail=$((fail + 1))
        failed_tests+=("$name")
    fi
}

assert_exit() {
    local name=$1 expected=$2 actual=$3
    if [[ "$expected" == "$actual" ]]; then
        echo "  $(green PASS) $name"
        pass=$((pass + 1))
    else
        echo "  $(red FAIL) $name (expected exit=$expected, got exit=$actual)"
        fail=$((fail + 1))
        failed_tests+=("$name")
    fi
}

fresh_dirs() {
    rm -rf "$WORKDIR/src" "$WORKDIR/dst"
    mkdir -p "$WORKDIR/src" "$WORKDIR/dst"
}

rm -rf "$WORKDIR"
mkdir -p "$WORKDIR"

echo "$(bold "===== lace localhost test suite =====")"
printf "lace:    %s\n" "$LACE"
printf "target:  %s\n" "$SSH_TARGET"
printf "workdir: %s\n" "$WORKDIR"
echo

echo "$(bold "[1] Push: nested tree, mixed file sizes")"
fresh_dirs
mkdir -p "$WORKDIR/src/a/b/c" "$WORKDIR/src/a/b/d" "$WORKDIR/src/e"
echo "hello world" > "$WORKDIR/src/top.txt"
head -c 0          /dev/urandom > "$WORKDIR/src/empty.bin"
head -c 1          /dev/urandom > "$WORKDIR/src/one-byte.bin"
head -c 4096       /dev/urandom > "$WORKDIR/src/a/4k.bin"
head -c $((1<<20)) /dev/urandom > "$WORKDIR/src/a/b/1m.bin"
head -c $((5<<20)) /dev/urandom > "$WORKDIR/src/a/b/c/5m.bin"
head -c $((50<<20)) /dev/urandom > "$WORKDIR/src/e/50m.bin"
"$LACE" "$WORKDIR/src" "$SSH_TARGET:$WORKDIR/dst" >/dev/null
assert_match "nested tree pushed and matches" "$WORKDIR/src" "$WORKDIR/dst"

echo
echo "$(bold "[2] Pull: ssh:remote -> local")"
fresh_dirs
mkdir -p "$WORKDIR/src/sub"
head -c $((2<<20)) /dev/urandom > "$WORKDIR/src/payload.bin"
echo "pulled" > "$WORKDIR/src/sub/note.txt"
"$LACE" "$SSH_TARGET:$WORKDIR/src" "$WORKDIR/dst" >/dev/null
assert_match "pull from remote matches" "$WORKDIR/src" "$WORKDIR/dst"

echo
echo "$(bold "[3] Single-file directory push")"
fresh_dirs
head -c 12345 /dev/urandom > "$WORKDIR/src/lonely.bin"
"$LACE" "$WORKDIR/src" "$SSH_TARGET:$WORKDIR/dst" >/dev/null
assert_match "single-file dir matches" "$WORKDIR/src" "$WORKDIR/dst"

echo
echo "$(bold "[4] Idempotent re-sync")"
fresh_dirs
mkdir -p "$WORKDIR/src/dir1"
head -c $((3<<20)) /dev/urandom > "$WORKDIR/src/dir1/a.bin"
head -c $((1<<20)) /dev/urandom > "$WORKDIR/src/dir1/b.bin"
"$LACE" "$WORKDIR/src" "$SSH_TARGET:$WORKDIR/dst" >/dev/null
"$LACE" "$WORKDIR/src" "$SSH_TARGET:$WORKDIR/dst" >/dev/null
assert_match "second push leaves dst identical" "$WORKDIR/src" "$WORKDIR/dst"

echo
echo "$(bold "[5] Dry-run does not write to destination")"
fresh_dirs
echo "shouldn't appear" > "$WORKDIR/src/ghost.txt"
head -c $((1<<20)) /dev/urandom > "$WORKDIR/src/ghost.bin"
"$LACE" --dryrun "$WORKDIR/src" "$SSH_TARGET:$WORKDIR/dst" >/dev/null || true
assert_empty_dir "dry-run leaves dst untouched" "$WORKDIR/dst"

echo
echo "$(bold "[6] Tricky filenames: spaces, unicode, dotfiles")"
fresh_dirs
mkdir -p "$WORKDIR/src/with space" "$WORKDIR/src/.hidden-dir"
echo "spaced"   > "$WORKDIR/src/with space/file with spaces.txt"
echo "unicode"  > "$WORKDIR/src/résumé-π.txt"
echo "dotfile"  > "$WORKDIR/src/.hidden-dir/.dotfile"
echo "dashed"   > "$WORKDIR/src/-leading-dash.txt"
"$LACE" "$WORKDIR/src" "$SSH_TARGET:$WORKDIR/dst" >/dev/null
assert_match "tricky filenames preserved" "$WORKDIR/src" "$WORKDIR/dst"

echo
echo "$(bold "[7] Local -> local copy (no SSH)")"
fresh_dirs
mkdir -p "$WORKDIR/src/x"
head -c $((512<<10)) /dev/urandom > "$WORKDIR/src/x/half-meg.bin"
echo "local" > "$WORKDIR/src/x/note.txt"
"$LACE" "$WORKDIR/src" "$WORKDIR/dst" >/dev/null
assert_match "local-to-local matches" "$WORKDIR/src" "$WORKDIR/dst"

echo
echo "$(bold "[8] Both endpoints remote is rejected")"
set +e
"$LACE" "$SSH_TARGET:/tmp/x" "$SSH_TARGET:/tmp/y" >/dev/null 2>&1
rc=$?
set -e
if [[ $rc -ne 0 ]]; then
    echo "  $(green PASS) double-remote rejected (exit=$rc)"
    pass=$((pass + 1))
else
    echo "  $(red FAIL) double-remote should have failed but exited 0"
    fail=$((fail + 1))
    failed_tests+=("double-remote rejected")
fi

echo
echo "$(bold "===== summary =====")"
total=$((pass + fail))
printf "%d/%d passed\n" "$pass" "$total"

if (( fail > 0 )); then
    echo "$(red "FAILED:")"
    for t in "${failed_tests[@]}"; do
        echo "  - $t"
    done
fi

if [[ "$KEEP" != "1" ]]; then
    rm -rf "$WORKDIR"
fi

exit $(( fail > 0 ? 1 : 0 ))
