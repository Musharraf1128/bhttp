#!/bin/bash
cd "$(dirname "$0")"
trap 'git checkout frame/header.go 2>/dev/null; rm -f bserve_* bcurl_*; rm -rf www_bench; pkill -f bserve_ 2>/dev/null' EXIT
mkdir -p www_bench && dd if=/dev/urandom of=www_bench/100m.bin bs=1M count=100 2>/dev/null
OUT=docs/bench.md
printf '# Benchmarks\n\n## Throughput by max frame size (100 MiB over loopback, best of 5)\n\n| MaxPayload | frames | header overhead | best time (s) | MiB/s |\n|---|---|---|---|---|\n' > $OUT
port=9200
for N in 4096 16384 65535; do
  echo ">> building MaxPayload=$N" >&2
  sed -i -E "s/^([[:space:]]*MaxPayload[[:space:]]*=[[:space:]]*)[0-9]+/\1$N/" frame/header.go
  grep -n "MaxPayload *=" frame/header.go >&2
  go build -o bserve_$N ./cmd/bserve || exit 1
  go build -o bcurl_$N ./cmd/bcurl || exit 1
  port=$((port+1))
  ./bserve_$N -host 127.0.0.1 ./www_bench $port >/dev/null 2>&1 &
  spid=$!
  sleep 0.5
  best=999
  for i in 1 2 3 4 5; do
    s=$(date +%s.%N)
    timeout 60 ./bcurl_$N 127.0.0.1:$port/100m.bin > /dev/null || echo "!! run $i failed" >&2
    e=$(date +%s.%N)
    t=$(echo "$e - $s" | bc -l)
    echo "   run $i: $t s" >&2
    if (( $(echo "$t < $best" | bc -l) )); then best=$t; fi
  done
  kill $spid 2>/dev/null; wait $spid 2>/dev/null
  frames=$(( (104857600 + N - 1) / N ))
  ovh=$(echo "scale=3; $frames*8*100/104857600" | bc -l)
  mibs=$(echo "scale=1; 100/$best" | bc -l)
  printf "| %s | %s | %s%% | %.3f | %s |\n" $N $frames $ovh $best $mibs >> $OUT
done
cat $OUT
