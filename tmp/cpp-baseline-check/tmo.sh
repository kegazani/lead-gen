#!/bin/zsh
# tmo.sh <seconds> <cmd...> — portable timeout replacement (no coreutils/gtimeout on this box)
secs=$1; shift
"$@" &
pid=$!
( sleep "$secs"; kill -TERM "$pid" 2>/dev/null; sleep 2; kill -KILL "$pid" 2>/dev/null ) &
wd=$!
wait "$pid"; rc=$?
kill "$wd" 2>/dev/null
wait "$wd" 2>/dev/null
if [ $rc -ge 128 ]; then echo "[tmo.sh] command hit ${secs}s limit (rc=$rc)" >&2; fi
exit $rc
