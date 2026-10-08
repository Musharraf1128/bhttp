### Server preface:

```
42 48 54 50 01 00 00 00     "BHTP" ver=1 reserved
```

### RESPONSE frame, 8-byte header + 0x69 (105) payload:

```
00 69 02 00 00 00 00 01     len=105 RESPONSE flags=0 (DATA follows) stream=1
00 c8                       status 200
8a 06 "bserve"              server (idx 10)
87 0c "6ac73906-f"          etag (idx 7), 12 bytes. Wait: see note below
89 1d "Thu, 08 Oct 2026 06:32:38 GMT"   last-modified (idx 9), 29 bytes
88 12 "public, max-age=60"  cache-control (idx 8), 18 bytes
85 18 "text/html; charset=utf-8"   content-type (idx 5), 24 bytes
86 02 "15"                  content-length (idx 6)
```

### DATA frame:

```
00 0f 03 01 00 00 00 01     len=15 DATA END_STREAM stream=1
3c 68 31 3e ... 0a          "<h1>hello</h1>\n"
```
