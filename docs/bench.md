# Benchmarks

## Throughput by max frame size (100 MiB over loopback, best of 5)

| MaxPayload | frames | header overhead | best time (s) | MiB/s |
|---|---|---|---|---|
| 4096 | 25600 | .195% | 0.410 | 243.9 |
| 16384 | 6400 | .048% | 0.149 | 671.3 |
| 65535 | 1601 | .012% | 0.096 | 1043.7 |

Machine: loopback, single client, page cache warm, best of 5. Header.go was
changed only for the measurement and restored (git shows no diff).

### Reading the table

Header overhead (bytes) falls from 0.195% to 0.012%, but that cannot explain
a 2.7x then 1.55x speedup. Throughput tracks the frame COUNT, not the byte
overhead: each frame costs one writev on the server and two reads on the
client (header, payload; there is no bufio by design, see Step 5), plus
scheduler wake-ups. 4x fewer frames gives ~1.55x throughput between 16K and
64K; between 4K and 16K it gives ~2.7x.

This contradicts the prediction in the design notes that 16K vs 64K would
barely differ. The prediction used byte overhead only (Calc A). Per-frame
CPU cost is a second axis that Calc A did not model.

### Why 16 KiB is still the v1 limit

- Loopback hides the network: 671 MiB/s is ~5.6 Gbit/s, well above a
  single-stream link. On 1 Gbit/s (~119 MiB/s) even 4 KiB frames (244 MiB/s
  here) are not the bottleneck.
- Buffer memory per in-flight frame is 4x larger at 64 KiB (Calc B: 6.4 GiB
  vs 1.6 GiB at 100k active connections).
- Large frames add head-of-line delay once v2 multiplexes streams.
- The Length field is 16 bits, so raising the limit later needs no wire change.

## Wire bytes (measured)

| Item | Binary | HTTP/1.1 text | Saved |
|---|---|---|---|
| minimal GET /index.html + Host | 38 | 50 | 24% |
| bcurl request (3 headers) | 52 | 84 | 38% |
| 4 request headers (hpack test) | 40 | 81 | 51% |
| full exchange, server to client | 144 (incl. 8 preface) | n/a | |

## Idle connection memory (bload, RSS only, kernel buffers excluded)

| Idle conns | RSS growth | Per connection |
|---|---|---|
| 1,000 | +6.8 MiB | 6.8 KiB (fixed costs inflate it) |
| 10,000 | +46.5 MiB | 4.8 KiB |

Fresh GET under 10k idle connections: 431 us. Linear extrapolation (not
measured): 100k = ~470 MiB, 1M = ~4.7 GiB of Go heap/stacks, plus kernel
socket buffers, which would dominate. Loopback from one IP caps near 28k
connections (ephemeral ports), so 10k was the practical ceiling.

## Abuse resistance (measured)

| Test | Result |
|---|---|
| 20 stalled peers fill 20 slots (handshake timeout 300 ms) | legit client served after 303 ms |
| slow reader on a 64 MiB file (write timeout 300 ms) | cut; next client served after 245 ms |
| FuzzOpen | 66k execs, 0 root escapes |
| FuzzLive | 87k execs, 0 panics, 0 hangs, 0 malformed output |
| frame / hpack fuzz | 340k / 357k / 456k execs, 0 failures |
