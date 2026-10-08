# Interop report

No second team was available, so the second implementation is
`interop/pyclient.py`: a Python client written from SPEC.md, sharing no
code with the Go packages (struct packing only, own header decoder).

## Run

    python3 interop/pyclient.py 127.0.0.1 9000 /index.html /nope /100k.bin \
        /empty.txt /../etc/passwd /index.html

    server preface version 1
    /index.html -> 200 15 body bytes OK
    /nope -> 404 0 body bytes OK
    /100k.bin -> 200 102400 body bytes OK
    /empty.txt -> 200 0 body bytes OK
    /../etc/passwd -> 404 0 body bytes OK
    /index.html -> 200 15 body bytes OK

One connection, stream ids 1,3,5,7,9,11. Every request was preceded by an
unknown-type frame (0x7f, stream 0) and the third request (stream 3)
sent `host` as a literal name instead of static index 1. The server
skipped and decoded both correctly.

## Checklist

| Check | Result |
|---|---|
| GET 200, body matches content-length | pass |
| 404, connection stays open | pass (next request served) |
| 100 KiB file, DATA chunking | pass (7 frames, 102400 bytes) |
| 0-byte file: RESPONSE with END_STREAM, no DATA | pass |
| unknown frame type skipped | pass |
| literal name where a static index exists | pass |
| ".." path | 404, pass |

## Ambiguities found (spec fixes)

1. Static table indices are 1-based and index 0 is invalid. Section 5
   lists the table but never says "1-based". Fix: add the sentence.
2. last-modified format (IMF-fixdate, GMT) is not stated. Fix in section 6.
3. The reserved top bit of Stream ID must be masked by receivers
   (`st & 0x7fffffff`). Spec says "ignore" but not that the ID is the
   low 31 bits. Fix: state it in section 1.
4. A response with a body but no content-length is not forbidden for
   non-200 codes. Fix: say content-length is REQUIRED whenever DATA follows.
5. Client behavior for DATA on another stream is only implied. Fix: say
   receivers MUST ignore frames on streams they did not open.

## Limits of this test

The Python client was written by the same author as the server, so it
shares the author's reading of the spec. It cannot find ambiguities the
author resolves identically by habit. A second human team would. The
report lists five found anyway.
