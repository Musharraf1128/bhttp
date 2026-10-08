# bhttp: HTTP in binary
- by Shah Musharaf ul islam
- Roll No 24bcs10447

A binary request/response protocol over TCP, with a Go server (`bserve`)
and client (`bcurl`). The protocol is defined in SPEC.md.

## Build
    go build -o bserve ./cmd/bserve
    go build -o bcurl  ./cmd/bcurl
    go build -o bload  ./cmd/bload

## Run
    ./bserve ./www 9000
    ./bcurl -v localhost:9000/index.html

## Test
    go vet ./... && go test ./... -race

## Fuzz
    go test ./frame/ -fuzz=FuzzDecode -fuzztime=10s
    go test ./frame/ -fuzz=FuzzParseRequest -fuzztime=15s
    go test ./hpack/ -fuzz=FuzzDecode -fuzztime=15s
    go test ./server/ -fuzz=FuzzOpen -fuzztime=20s
    go test ./server/ -fuzz=FuzzLive -fuzztime=30s

## Idle-connection memory
    ulimit -n 65536
    ./bserve -max-conns 20000 -idle 5m ./www 9000 &
    ./bload -n 10000 -pid $!

## Layout
    frame/   header, preface, payload codecs     hpack/   static table + literal
    server/  bserve logic                        client/  bcurl logic
    cmd/     binaries                            docs/    hexdump, interop, bench

## Interop (clean-room Python client, written from SPEC.md)
    dd if=/dev/urandom of=www/100k.bin bs=1k count=100; : > www/empty.txt
    ./bserve ./www 9000 &
    python3 interop/pyclient.py 127.0.0.1 9000 /index.html /nope /100k.bin /empty.txt /../etc/passwd /index.html

## Benchmarks (throughput by frame size; rewrites docs/bench.md's table, restores frame/header.go)
    ./bench.sh

## Docs
    SPEC.md              the protocol (the deliverable)
    docs/hexdump.md      annotated request + response, every byte
    docs/interop.md      second-implementation report and spec fixes
    docs/bench.md        wire bytes, throughput, idle memory, abuse tests
