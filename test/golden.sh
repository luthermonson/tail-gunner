#!/usr/bin/env bash
# Golden compatibility suite: diff tail-gunner pipe mode against GNU tail.
# Run from the repo root: bash test/golden.sh
# Uses repo-relative paths (MSYS /tmp doesn't translate for native exes) and
# git hash-object for byte-exact comparison (no reliance on cmp/diff).
set -u
TG=./tail-gunner.exe
[ -f "$TG" ] || TG=./tail-gunner
GNU=tail

tmp=test/tmp
rm -rf "$tmp"; mkdir -p "$tmp"
trap 'rm -rf "$tmp"' EXIT

seq 1 20 > "$tmp/twenty.txt"
seq 1 3 > "$tmp/three.txt"
printf 'alpha\nbeta\ngamma' > "$tmp/nonewline.txt"   # no trailing newline
: > "$tmp/empty.txt"
printf '\n\n\n' > "$tmp/blanks.txt"

same() { [ "$(git hash-object "$1")" = "$(git hash-object "$2")" ]; }

pass=0; fail=0
report() { # desc wantcode gotcode
  if same "$tmp/want.out" "$tmp/got.out" && [ "$2" = "$3" ]; then
    pass=$((pass+1)); echo "ok   $1"
  else
    fail=$((fail+1)); echo "FAIL $1 (exit want=$2 got=$3)"
    echo "--- want:"; head -5 "$tmp/want.out"; echo "--- got:"; head -5 "$tmp/got.out"
  fi
}

check() {
  desc=$1; shift
  "$GNU" "$@" > "$tmp/want.out" 2>/dev/null; wantcode=$?
  "$TG"  "$@" > "$tmp/got.out"  2>/dev/null; gotcode=$?
  report "$desc" "$wantcode" "$gotcode"
}

check_stdin() {
  desc=$1; input=$2; shift 2
  "$GNU" "$@" < "$input" > "$tmp/want.out" 2>/dev/null; wantcode=$?
  "$TG"  "$@" < "$input" > "$tmp/got.out"  2>/dev/null; gotcode=$?
  report "$desc" "$wantcode" "$gotcode"
}

check "default last 10"           "$tmp/twenty.txt"
check "-n 5"                 -n 5 "$tmp/twenty.txt"
check "-n 0"                 -n 0 "$tmp/twenty.txt"
check "-n +15"               -n +15 "$tmp/twenty.txt"
check "-n +1 (whole file)"   -n +1 "$tmp/twenty.txt"
check "-n bigger than file"  -n 99 "$tmp/three.txt"
check "-c 25"                -c 25 "$tmp/twenty.txt"
check "-c +7"                -c +7 "$tmp/twenty.txt"
check "-c bigger than file"  -c 9999 "$tmp/three.txt"
check "no trailing newline"  -n 2 "$tmp/nonewline.txt"
check "empty file"           "$tmp/empty.txt"
check "blank lines only"     -n 2 "$tmp/blanks.txt"
check "multi-file headers"   -n 3 "$tmp/twenty.txt" "$tmp/three.txt"
check "multi-file -q"        -q -n 2 "$tmp/twenty.txt" "$tmp/three.txt"
check "single file -v"       -v -n 2 "$tmp/three.txt"
check "missing file"         "$tmp/does-not-exist.txt"
check "missing + existing"   -n 2 "$tmp/does-not-exist.txt" "$tmp/three.txt"

check_stdin "stdin default"        "$tmp/twenty.txt"
check_stdin "stdin -n 4"           "$tmp/twenty.txt" -n 4
check_stdin "stdin -n +18"         "$tmp/twenty.txt" -n +18
check_stdin "stdin -c 13"          "$tmp/twenty.txt" -c 13
check_stdin "stdin no newline"     "$tmp/nonewline.txt" -n 1
check_stdin "explicit - operand"   "$tmp/twenty.txt" -n 2 -

# follow smoke test: appended lines must come through in pipe mode
seq 1 5 > "$tmp/grow.txt"
"$TG" -f -n 2 "$tmp/grow.txt" > "$tmp/follow.out" 2>/dev/null &
tgpid=$!
sleep 1
printf 'six\nseven\n' >> "$tmp/grow.txt"
sleep 2
kill $tgpid 2>/dev/null
if [ "$(cat "$tmp/follow.out")" = "$(printf '4\n5\nsix\nseven')" ]; then
  pass=$((pass+1)); echo "ok   -f picks up appended lines"
else
  fail=$((fail+1)); echo "FAIL -f follow"; cat "$tmp/follow.out"
fi

echo "----------------------------------------"
echo "passed: $pass  failed: $fail"
[ "$fail" = 0 ]
