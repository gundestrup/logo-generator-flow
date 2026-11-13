#!/bin/sh
# Per-package coverage floor for internal/ packages (default 80%).
# Runs without -race: coverage instrumentation is what matters here.
MIN=${MIN:-80}
out=$(go test -cover ./internal/... 2>&1) || { echo "$out"; exit 1; }
echo "$out"
echo "$out" | awk -v min="$MIN" '
/^ok.*coverage: .*% of statements/ {
  match($0, /coverage: [0-9.]+/);
  pct = substr($0, RSTART + 10, RLENGTH - 10) + 0;
  if (pct < min) { printf "BELOW FLOOR: %s\n", $0; bad = 1 }
}
END {
  if (bad) {
    printf "coverage floor is %s%% per package (MIN= to override)\n", min > "/dev/stderr";
    exit 1
  }
}'
