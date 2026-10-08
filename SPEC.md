## 1. Frame Header

All data after the connection preface is carried in frames. Every frame
begins with a fixed 8-byte header, followed by Length bytes of payload.
All multi-byte integers are big-endian.

    +---------------+-------+-------+-+---------------------------+
    |  Length (16)  |Type(8)|Flg(8) |R|     Stream ID (31)        |
    +---------------+-------+-------+-+---------------------------+

Length:    Payload size in bytes, excluding the 8-byte header. A sender
           MUST NOT send Length > 16384 (MAX_PAYLOAD). A receiver that
           reads Length > 16384 MUST treat it as a connection error
           (see 4) and MUST NOT allocate or read the payload.
Type:      Frame type. A receiver that does not recognise Type MUST read
           and discard Length bytes and continue with the next frame.
Flags:     Type-specific. Senders MUST set undefined bits to 0.
           Receivers MUST ignore undefined bits.
R:         Reserved. Senders MUST send 0. Receivers MUST ignore.
Stream ID: Client-initiated streams use odd IDs, strictly increasing
           per connection. Server-initiated IDs are even and reserved
           for future use. ID 0 is reserved for connection-level frames.
           A client MUST NOT reuse or wrap IDs; when IDs near 2^31-1 it
           MUST close and reconnect.

### 1.1 Rationale
- Fixed size: the receiver can always find the next frame boundary,
  which is what makes skipping unknown types safe.
- 16-bit Length with a 16 KiB limit: header overhead is 0.049% at the
  limit; 64 KiB would quadruple worst-case buffer memory for 0.037
  points of overhead saved. 1M connections x 16 KiB = 16 GiB, and
  implementations are expected to allocate payload buffers only while
  a frame is in flight.
- 31-bit Stream ID: at 1000 req/s, 12.4 days to exhaustion; 16 bits
  would last 33 seconds.
- Version is negotiated once in the preface, not per frame.

### 1.2 Rejected alternatives
- 24-bit Length (as in HTTP/2): unusable range at v1 limits.
- Varint length/type: variable header breaks one-read parsing and
  unknown-frame skipping.
- Per-frame version byte: 1 byte x every frame, redundant after preface.

## 2. Connection Preface

Each endpoint sends an 8-byte preface before any frame:

    +-----------+---------+-------------+
    | "BHTP" (4)| Ver (1) | Reserved (3)|
    +-----------+---------+-------------+

The client sends first. The server replies with the highest version it
supports that is <= the client's. This document defines version 1.
Reserved bytes: senders MUST send 0, receivers MUST ignore. A receiver
whose peer's magic is not "BHTP" MUST close the connection without
sending a frame. A client receiving a server version it does not
support MUST close.

## 3. Frame Types

| Type | Name     | Dir | Stream | Payload                           |
|------|----------|-----|--------|-----------------------------------|
| 0x01 | REQUEST  | C>S | odd    | method u8, pathlen u16, path, hdrs|
| 0x02 | RESPONSE | S>C | = req  | status u16, hdrs                  |
| 0x03 | DATA     | both| = req  | body bytes                        |
| 0x04 | ERROR    | both| 0      | code u16, optional UTF-8 text     |

Flag 0x01 (END_STREAM) is defined on REQUEST, RESPONSE and DATA. It
marks the last frame the sender will send on that stream.
REQUEST and RESPONSE MUST fit in a single frame; there is no
continuation mechanism in version 1.
Method 0x01 is GET. A RESPONSE with END_STREAM set has an empty body.
A RESPONSE without it is followed by DATA frames on the same stream,
the last carrying END_STREAM. Status codes use HTTP semantics.

### 3.1 Payload layouts

REQUEST payload:  method (u8) | pathlen (u16) | path | header block
RESPONSE payload: status (u16) | header block

Path MUST be non-empty and begin with '/'. Method MUST be 0x01.
Status MUST be in 100..599. Header blocks are defined in 5.
A payload that violates any of these is malformed (stream error, 4).
A sender whose encoded payload would exceed 16384 bytes MUST fail
locally and MUST NOT send a truncated or oversized frame.
Servers MUST NOT assume the path is safe; mapping a path to a file
is outside the wire format (see 7).

### 3.2 Worked example (GET /index.html, Host localhost:9000)

    00 1e 01 01 00 00 00 01   header: len=30 REQUEST END_STREAM stream=1
    01                        method GET
    00 0b 2f 69 6e 64 65 78   pathlen=11, "/index"
    2e 68 74 6d 6c            ".html"
    81 0e 6c 6f 63 61 6c 68   host (idx 1), vlen=14, "localh"
    6f 73 74 3a 39 30 30 30   "ost:9000"

38 bytes on the wire; the HTTP/1.1 text form is 50.

### 3.3 Direction and body rules

A v1 REQUEST MUST set END_STREAM (a GET has no body). A server that
receives one without it MUST answer 400 (stream error).
A server MUST discard DATA frames sent by a client, except that DATA on
stream 0 is a connection error. A RESPONSE sent to a server, or a REQUEST
sent to a client, is a connection error (PROTOCOL).
A 200 response with a body MUST carry content-length, and its DATA frames
MUST total exactly that many bytes, END_STREAM on the last. If a server
cannot finish a body it has started (for example the file shrank), it MUST
send ERROR(INTERNAL) and close. A client that sees EOF or ERROR before
END_STREAM, or a body whose length differs from content-length, MUST treat
the response as failed.

## 4. Errors

A *stream error* is a fault in a frame whose boundaries were intact.
The receiver MUST answer with a RESPONSE carrying status 400 on that
stream and MUST keep the connection open.

A *connection error* is a fault after which the next frame boundary
cannot be trusted, or a protocol violation. The receiver SHOULD send an
ERROR frame on stream 0 and MUST then close the connection.

Connection errors: Length > 16384; EOF or timeout inside a frame;
REQUEST with an even, zero, or non-increasing stream ID; REQUEST,
RESPONSE or DATA on stream 0; ERROR on a non-zero stream.

Unknown frame types are neither: the receiver MUST discard Length bytes
and continue. Unknown ERROR codes MUST be treated as PROTOCOL (1).

### 4.1 Rationale
- The split exists because a stream error leaves us able to resync and
  a connection error does not. Closing is the only safe response to
  lost framing.
- ERROR is best-effort: the peer may already be gone.
- 404 (not 403) for paths outside the root avoids confirming existence.

### 4.2 Rejected alternatives
- Per-stream RST_STREAM frame: nothing to cancel with one request in
  flight; added in v2 with multiplexing.
- CONTINUATION frames: header blocks fit in 16 KiB; saves a state
  machine.

### 4.3 Timeouts and closing

A receiver SHOULD close a connection that sends no frame header within an
idle timeout, and one whose frame is not completed within a frame timeout.
It SHOULD send ERROR(TIMEOUT) first. A sender SHOULD bound the time of
every write.
An endpoint that sends ERROR SHOULD half-close its write side, drain
incoming bytes for a short bounded time, and then close, so the ERROR is
not destroyed by a TCP reset caused by unread input.

## 5. Header Blocks

REQUEST (after method and path) and RESPONSE (after status) carry a header
block: zero or more fields packed back to back until the end of the payload.
The first byte of each field selects its representation.

    Indexed name:  1xxxxxxx | vlen (8) | value
                   xxxxxxx is a static table index, 1..10.
    Literal name:  00000000 | nlen (8) | name | vlen (8) | value

Static table:

    1 host           2 user-agent      3 accept        4 if-none-match
    5 content-type   6 content-length  7 etag          8 cache-control
    9 last-modified 10 server

Names are ASCII. Senders MUST lowercase them; receivers MUST lowercase
literal names. Values are opaque bytes. Name and value lengths are at most
255 bytes. A literal name MUST NOT be empty. Duplicate names are allowed
and order is preserved. A sender SHOULD use the indexed form whenever the
name is in the table.

Any of the following makes the header block malformed, which is a stream
error (400): a first byte that is neither 0x00 nor 1xxxxxxx; index 0 or
greater than 10; an empty literal name; a length that runs past the end of
the payload. Because the enclosing frame has been fully read, framing is
intact and the connection stays open.

### 5.1 Rationale
- Two mechanisms only (static index, literal): stateless, so a malformed
  or lost frame cannot corrupt later ones, and per-connection memory is
  zero beyond the frame buffer.
- The ten names are exactly those a static file server and client send.
  etag, if-none-match, last-modified and cache-control carry the cache
  semantics a CDN needs.
- First bytes 0x01-0x7F are reserved for future representations. They
  cannot be skipped (their length is unknown), so a peer must not use one
  until the preface version says it may.

### 5.2 Rejected alternatives
- Dynamic table: stateful, per-connection memory, desync risk.
- Huffman coding: small gain, bit-level decoder, more fuzz surface.
- Varint lengths: u8 is enough for v1; v2 can add a representation.

## 6. Static File Mapping (bserve)

The request path is opaque bytes: no percent-decoding, no query string.
It is mapped to a regular file beneath a configured root. A server MUST
NOT serve any file outside the root, including through symbolic links.
A path that is empty after the leading '/', contains '.', '..' or empty
segments, ends in '/', contains NUL, does not exist, is a directory, is
not a regular file, or resolves outside the root MUST yield 404 with an
empty body, so that the response does not reveal which case applied.
A 200 response carries content-type, content-length, etag, last-modified,
cache-control and server. etag is "<mtime hex>-<size hex>". A request
whose if-none-match lists the current etag (or "*") receives 304 with
END_STREAM and no body.

## 7. Resource Limits and Abuse Resistance

A receiver MUST NOT allocate for a payload until Length has been
validated against 16384. Memory committed per connection MUST NOT
exceed one maximum-size frame, and SHOULD be zero while the connection
is idle.

A server SHOULD enforce four independent time bounds: handshake (peer
preface), idle (waiting for a frame header), frame (completing a frame
once its header has arrived), and write (each frame sent). A peer that
exceeds any of them SHOULD receive ERROR(TIMEOUT) where a write is
still possible, and the connection MUST be closed.

A server SHOULD bound concurrent connections. When the bound is reached
it SHOULD stop accepting rather than accept and reject, so that excess
peers wait in the kernel backlog.

### 7.1 Known limits
Time bounds limit how long a hostile peer holds a connection slot, not
how many it can hold at once. Per-address limits are out of scope for
version 1.

## 8. Design Decisions

| Decision | Reason | Rejected |
|---|---|---|
| 8-byte fixed header | one aligned read; Length always at offset 0 so unknown types are skippable | variable header: breaks resync |
| 16-bit Length, 16 KiB cap | 0.049% overhead at cap; bounded memory (16 GiB at 1M conns worst case, ~0 idle) | H2's 24 bits: unusable range |
| 31-bit Stream ID | 12.4 days at 1000 req/s; odd/even avoids negotiation | 16-bit: 33 s |
| Version in preface | zero per-frame cost; fails fast | per-frame version byte |
| Static table + literal only | stateless: a lost frame cannot corrupt later ones | dynamic table, Huffman |
| Stream vs connection error | split by "can we still find the next boundary?" | one error class |
| 404 for every path failure | doesn't reveal what exists outside the root | 403 |
| os.Root for path mapping | containment enforced inside open(); no check-then-open race | Clean+EvalSymlinks+prefix |

## 9. Version 2 Wishlist

Multiplexing (stream IDs already present), RST_STREAM and GOAWAY,
flow control (WINDOW_UPDATE), dynamic table and indexed name+value pairs
(first bytes 0x01-0x7F reserved), Range requests, varint lengths for long
header values, per-address connection limits (7.1), server-initiated
streams (even IDs).
