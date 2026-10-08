# Annotated hexdump: GET /index.html

One TCP connection, stream 1. All bytes are hex unless marked (dec).
Captured with `./bcurl -v localhost:9000/index.html`. The prefaces are not
shown by `-v` (they are not frames); they were captured separately with nc.

## 1. Prefaces (8 bytes each way)

    42 48 54 50   01   00 00 00
    "BHTP"        ver  reserved (sender 0, receiver ignores)

Client sends first; server replies with the same bytes (version 1).

## 2. REQUEST frame: 52 bytes = 8 header + 44 payload

Frame header:

    00 2c   01   01   00 00 00 01
    len=44  type=REQUEST  flags=END_STREAM  R=0, stream id=1

Payload (offsets within the payload):

| Offset | Bytes | Meaning |
|---|---|---|
| 0 | 01 | method GET |
| 1-2 | 00 0b | pathlen = 11 (dec) |
| 3-13 | 2f 69 6e 64 65 78 2e 68 74 6d 6c | "/index.html" |
| 14-15 | 81 0e | indexed name, 0x81 & 0x7f = 1 = host; vlen = 14 |
| 16-29 | 6c 6f 63 61 6c 68 6f 73 74 3a 39 30 30 30 | "localhost:9000" |
| 30-31 | 82 07 | indexed name 2 = user-agent; vlen = 7 |
| 32-38 | 62 63 75 72 6c 2f 31 | "bcurl/1" |
| 39-40 | 83 03 | indexed name 3 = accept; vlen = 3 |
| 41-43 | 2a 2f 2a | "*/*" |

Check: 1 + 2 + 11 + 16 + 9 + 5 = 44. HTTP/1.1 text equivalent: 84 bytes.

## 3. RESPONSE frame: 113 bytes = 8 header + 105 payload

Frame header:

    00 69   02   00   00 00 00 01
    len=105 type=RESPONSE flags=0 (no END_STREAM: DATA follows) stream=1

Payload:

| Offset | Bytes | Meaning |
|---|---|---|
| 0-1 | 00 c8 | status 200 (dec) |
| 2-3 | 8a 06 | 0x8a & 0x7f = 10 = server; vlen 6 |
| 4-9 | 62 73 65 72 76 65 | "bserve" |
| 10-11 | 87 0c | index 7 = etag; vlen 12 |
| 12-23 | 22 36 61 63 37 33 39 30 36 2d 66 22 | "6ac73906-f" (quotes are part of the value) = mtime hex, size hex (0xf = 15) |
| 24-25 | 89 1d | index 9 = last-modified; vlen 29 |
| 26-54 | 54 68 75 2c 20 30 38 20 4f 63 74 20 32 30 32 36 20 30 36 3a 33 32 3a 33 38 20 47 4d 54 | "Thu, 08 Oct 2026 06:32:38 GMT" |
| 55-56 | 88 12 | index 8 = cache-control; vlen 18 |
| 57-74 | 70 75 62 6c 69 63 2c 20 6d 61 78 2d 61 67 65 3d 36 30 | "public, max-age=60" |
| 75-76 | 85 18 | index 5 = content-type; vlen 24 |
| 77-100 | 74 65 78 74 2f 68 74 6d 6c 3b 20 63 68 61 72 73 65 74 3d 75 74 66 2d 38 | "text/html; charset=utf-8" |
| 101-102 | 86 02 | index 6 = content-length; vlen 2 |
| 103-104 | 31 35 | "15" |

Check: 2 + 8 + 14 + 31 + 20 + 26 + 4 = 105.

## 4. DATA frame: 23 bytes = 8 header + 15 payload

    00 0f   03   01   00 00 00 01
    len=15  type=DATA  flags=END_STREAM  stream=1

Payload: 3c 68 31 3e 68 65 6c 6c 6f 3c 2f 68 31 3e 0a = "<h1>hello</h1>\n"
(15 bytes, matches content-length 15 above).

## 5. Totals

| Direction | Bytes |
|---|---|
| client to server | 8 (preface) + 52 = 60 |
| server to client | 8 (preface) + 113 + 23 = 144 |

Header overhead in the response: 6 fields x 2 bytes (index + vlen) = 12 bytes
of framing, versus 49+ bytes of names and separators in HTTP/1.1 text.

## 6. Holes found while annotating

1. last-modified text format is not stated in the spec. Fix: SPEC section 6
   must say "IMF-fixdate, e.g. Thu, 08 Oct 2026 06:32:38 GMT, always GMT".
2. content-type value includes "; charset=utf-8" for text types. The spec does
   not say what content-type values a server must send. Fix: state that
   values are opaque to the protocol and clients MUST NOT parse them.
3. `-v` does not print the prefaces, so they cannot be annotated from the
   bcurl dump alone. Fix (optional): trace the prefaces in bcurl.
4. Header order in the response (server, etag, last-modified, cache-control,
   content-type, content-length) differs from the order listed in SPEC
   section 6. Fix: the "order carries no meaning" sentence in section 4.
5. flags=0x00 on RESPONSE means "DATA frames follow". Stated in 3, but
   a reader must connect it to END_STREAM. Fix: add the sentence to section 3.

Every other byte was decodable from the spec text alone.
