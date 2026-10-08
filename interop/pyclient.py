#!/usr/bin/env python3
"""Independent bhttp client written from SPEC.md only. Usage: pyclient.py host port path [path...]
All paths are fetched on ONE connection (stream ids 1,3,5...)."""
import socket, struct, sys

STATIC = ["", "host", "user-agent", "accept", "if-none-match", "content-type",
          "content-length", "etag", "cache-control", "last-modified", "server"]

def recv_exact(s, n):
    b = b""
    while len(b) < n:
        c = s.recv(n - len(b))
        if not c:
            raise EOFError("closed mid-frame")
        b += c
    return b

def frame(t, flags, sid, payload):
    return struct.pack(">HBBI", len(payload), t, flags, sid) + payload

def hdrs(b):
    out, i = [], 0
    while i < len(b):
        c = b[i]; i += 1
        if c & 0x80:
            name = STATIC[c & 0x7f]
        elif c == 0:
            n = b[i]; i += 1
            name = b[i:i+n].decode().lower(); i += n
        else:
            raise ValueError("reserved header byte %#x" % c)
        n = b[i]; i += 1
        out.append((name, b[i:i+n].decode(errors="replace"))); i += n
    return out

def request(path, host, literal_host=False):
    p = path.encode()
    if literal_host:   # literal form of a name that has a static index
        h = bytes([0, 4]) + b"host" + bytes([len(host)]) + host.encode()
    else:
        h = bytes([0x81, len(host)]) + host.encode()
    return b"\x01" + struct.pack(">H", len(p)) + p + h

def main():
    host, port, paths = sys.argv[1], int(sys.argv[2]), sys.argv[3:]
    s = socket.create_connection((host, port), timeout=10)
    s.sendall(b"BHTP\x01\x00\x00\x00")
    pre = recv_exact(s, 8)
    assert pre[:4] == b"BHTP", "bad server preface %r" % pre
    print("server preface version", pre[4])
    sid, rc = 1, 0
    for path in paths:
        s.sendall(frame(0x7f, 0, 0, b"future"))          # unknown type must be skipped
        s.sendall(frame(1, 1, sid, request(path, "%s:%d" % (host, port), literal_host=(sid == 3))))
        status, body, end, h = None, b"", False, []
        while not end:
            ln, t, fl, st = struct.unpack(">HBBI", recv_exact(s, 8))
            p = recv_exact(s, ln)
            st &= 0x7fffffff
            if t == 2 and st == sid:
                status = struct.unpack(">H", p[:2])[0]; h = hdrs(p[2:]); end = bool(fl & 1)
            elif t == 3 and st == sid:
                body += p; end = bool(fl & 1)
            elif t == 4:
                print("ERROR frame", struct.unpack(">H", p[:2])[0], p[2:]); sys.exit(1)
            # unknown types and other streams: skipped
        cl = dict(h).get("content-length")
        ok = status == 200 and cl is not None and int(cl) == len(body)
        print("%s -> %s %d body bytes %s" % (path, status, len(body), "OK" if ok or status != 200 else "LENGTH MISMATCH"))
        if status >= 400: rc = 1
        sid += 2
    sys.exit(rc)

main()
