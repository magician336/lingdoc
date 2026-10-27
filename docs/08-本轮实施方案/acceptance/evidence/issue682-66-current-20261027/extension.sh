#!/bin/sh
set -eu
artifacts=/artifacts/pr755-66
Xvfb -displayfd 3 -screen 0 1600x1200x24 -nolisten tcp 3> "$artifacts/xvfb-display.txt" > "$artifacts/xvfb.log" 2>&1 &
display_pid=$!
cleanup() {
    kill "$display_pid" 2>/dev/null || true
    wait "$display_pid" 2>/dev/null || true
}
trap cleanup EXIT
for attempt in $(seq 1 100); do
    if [ -s "$artifacts/xvfb-display.txt" ]; then
        break
    fi
    sleep 0.1
done
test -s "$artifacts/xvfb-display.txt"
DISPLAY=:$(cat "$artifacts/xvfb-display.txt")
export DISPLAY
export BROWSERSKILL_TEST_HEADED=1
python3 "$artifacts/run.py" browser-extension
