#!/usr/bin/env python3
"""Owner-only TCP front for the reverse tunnel's unix sockets.

A TCP listener on a loopback address can be reached by every user of the compute
node. This relay accepts a connection only when the connecting socket belongs to the
user running it, read from /proc/net/tcp or tcp6, and then pipes it to a unix
socket that only that user can open.

The job starts it before its workload and it holds the addresses for the whole job,
so nobody else can take them while the tunnel is down. It refuses to start when one
is already taken, writes rev-relay.ready next to itself once all are bound, and
exits when the job's shell or its own directory goes away.

    rev-relay.py <bind address> <port>=<unix socket> [...]
"""
import os, socket, sys, threading, time

ALLOW = os.getuid()
lock = threading.Lock()
counts = {}


def note(key, msg):
    """Logs the first of a kind of failure and every 1000th after it: the log is
    the job's output, and anyone on the node can connect in a loop."""
    with lock:
        n = counts[key] = counts.get(key, 0) + 1
    if n == 1 or n % 1000 == 0:
        print(f"relay: {msg} ({n} so far)", file=sys.stderr, flush=True)


def hexip(ip):
    return "".join("%02X" % b for b in reversed(socket.inet_aton(ip)))


def peer_uid(conn):
    (pip, pport), (lip, lport) = conn.getpeername(), conn.getsockname()
    # A client on an IPv6 socket reaches this IPv4 listener through a v4-mapped
    # address and is listed in tcp6, as ::ffff:<ip> in the same word order.
    for table, prefix in (("/proc/net/tcp", ""), ("/proc/net/tcp6", "0000000000000000FFFF0000")):
        want_local = "%s%s:%04X" % (prefix, hexip(pip), pport)
        want_remote = "%s%s:%04X" % (prefix, hexip(lip), lport)
        try:
            f = open(table)
        except FileNotFoundError:
            continue
        with f:
            next(f)
            for line in f:
                c = line.split()
                if c[1] == want_local and c[2] == want_remote:
                    return int(c[7])
    return None


def pump(src, dst):
    try:
        while True:
            data = src.recv(1 << 16)
            if not data:
                break
            dst.sendall(data)
    except OSError:
        pass
    finally:
        try:
            dst.shutdown(socket.SHUT_WR)
        except OSError:
            pass


def handle(conn, path):
    try:
        uid = peer_uid(conn)
        if uid != ALLOW:
            note(("uid", uid), f"refused connections from uid {uid}")
            return
        with socket.socket(socket.AF_UNIX) as up:
            try:
                up.connect(path)
            except OSError as e:
                note(path, f"{path}: {e}")
                return
            with lock:
                counts.pop(path, None)
            t = threading.Thread(target=pump, args=(up, conn), daemon=True)
            t.start()
            pump(conn, up)
            t.join()
    except (OSError, RuntimeError) as e:
        # Out of descriptors or threads, or a peer gone before it was looked up.
        note("drop", f"dropped a connection: {e}")
    finally:
        conn.close()


def serve(s, path):
    # A listener whose thread died would stay bound and leave connections hanging
    # in its backlog for the rest of the job, so no error may end this loop.
    while True:
        try:
            conn, _ = s.accept()
        except OSError as e:
            note("accept", f"accept: {e}")
            time.sleep(1)
            continue
        try:
            conn.setsockopt(socket.IPPROTO_TCP, socket.TCP_NODELAY, 1)
            threading.Thread(target=handle, args=(conn, path), daemon=True).start()
        except (OSError, RuntimeError) as e:
            note("drop", f"dropped a connection: {e}")
            conn.close()


here = os.path.dirname(os.path.abspath(__file__))
parent = os.getppid()
bind = sys.argv[1]
listeners = []
for spec in sys.argv[2:]:
    port, path = spec.split("=", 1)
    s = socket.socket()
    s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    try:
        s.bind((bind, int(port)))
        s.listen(128)
    except OSError as e:
        print(f"relay: cannot listen on {bind}:{port}: {e}", file=sys.stderr, flush=True)
        sys.exit(1)
    listeners.append((s, path))
for s, path in listeners:
    threading.Thread(target=serve, args=(s, path), daemon=True).start()
open(os.path.join(here, "rev-relay.ready"), "w").close()
print(f"relay up on {bind}, allowing uid {ALLOW}", file=sys.stderr, flush=True)
while os.getppid() == parent and os.path.isdir(here):
    time.sleep(5)
