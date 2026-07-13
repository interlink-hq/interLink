cat <<'EOFMESH' > $TMPDIR/mesh.sh
#!/bin/bash
set -e
set -m

export PATH=$PATH:$PWD:/usr/sbin:/sbin

# Prepare the temporary directory
TMPDIR=${SLIRP_TMPDIR:-/tmp/.slirp.$RANDOM$RANDOM}
mkdir -p $TMPDIR
cd $TMPDIR

# Set WireGuard interface name
WG_IFACE="{{.WGInterfaceName}}"

echo "=== Downloading binaries (outside namespace) ==="

download_with_retry() {
    url="$1"
    output="$2"
    max_attempts="${3:-5}"
    delay="${4:-1}"

    for attempt in $(seq 1 "$max_attempts"); do
        echo "Downloading $output (attempt $attempt/$max_attempts)..."
        if curl -L -f -k --connect-timeout 10 --max-time 120 "$url" -o "$output"; then
            chmod +x "$output"
            return 0
        fi

        if [ "$attempt" -lt "$max_attempts" ]; then
            echo "Download failed for $output, retrying in ${delay}s..."
            sleep "$delay"
            delay=$((delay * 2))
        fi
    done

    echo "ERROR: Failed to download $output from $url after $max_attempts attempts"
    return 1
}

download_with_retry "{{.WSTunnelExecutableURL}}" wstunnel
download_with_retry "{{.WireguardGoURL}}" wireguard-go
download_with_retry "{{.WgToolURL}}" wg
download_with_retry "{{.Slirp4netnsURL}}" slirp4netns

# Check if iproute2 is available
if ! command -v ip &> /dev/null; then
    echo "ERROR: 'ip' command not found. Please install iproute2 package"
    exit 1
fi

# Copy ip command to tmpdir for use in namespace
IP_CMD=$(command -v ip)
cp $IP_CMD $TMPDIR/ || echo "Warning: could not copy ip command"

echo "=== All binaries downloaded successfully ==="

# Create WireGuard config with dynamic interface name
cat <<'EOFWG' > $WG_IFACE.conf
{{.WGConfig}}
EOFWG

# Generate the execution script that will run inside the namespace
cat <<'EOFSLIRP' > $TMPDIR/slirp.sh
#!/bin/bash
set -e

# Derive TMPDIR from this script's own location. This script is written to
# $TMPDIR/slirp.sh and executed under unshare as a separate process where
# TMPDIR is not guaranteed to be exported.
TMPDIR="$(cd "$(dirname "$0")" && pwd)"

# Ensure PATH includes tmpdir
export PATH=$TMPDIR:$PATH:/usr/sbin:/sbin

# Get WireGuard interface name from parent
WG_IFACE="{{.WGInterfaceName}}"

echo "=== Inside network namespace ==="
echo "Using WireGuard interface: $WG_IFACE"

echo $$ > "$TMPDIR/netns.pid"

export WG_SOCKET_DIR="$TMPDIR"

# Override /etc/resolv.conf to avoid issues with read-only filesystems
# Not all environments support this; ignore errors
set -euo pipefail

HOST_DNS=$(grep "^nameserver" /etc/resolv.conf | head -1 | awk '{print $2}')

{
  mkdir -p /tmp/etc-override
  echo "search default.svc.cluster.local svc.cluster.local cluster.local" > /tmp/etc-override/resolv.conf
  echo "nameserver $HOST_DNS" >> /tmp/etc-override/resolv.conf
  echo "nameserver {{.DNSServiceIP}}" >> /tmp/etc-override/resolv.conf
  echo "nameserver 1.1.1.1" >> /tmp/etc-override/resolv.conf
  echo "nameserver 8.8.8.8" >> /tmp/etc-override/resolv.conf
  mount --bind /tmp/etc-override/resolv.conf /etc/resolv.conf
} || {
  rc=$?
  echo "ERROR: one of the commands failed (exit $rc)" >&2
  exit $rc
}

# Make filesystem private to allow bind mounts
mount --make-rprivate / 2>/dev/null || true

# Create writable /var/run with wireguard subdirectory
mkdir -p $TMPDIR/var-run/wireguard
mount --bind $TMPDIR/var-run /var/run

cat > $TMPDIR/resolv.conf <<EOF
search default.svc.cluster.local svc.cluster.local cluster.local
nameserver {{.DNSServiceIP}}
nameserver 1.1.1.1
EOF
export LOCALDOMAIN=$TMPDIR/resolv.conf


wait_for_wstunnel_server() {
    echo "Waiting for wstunnel server to be stably ready..."
    consecutive=0
    attempt=0
    max_attempts=20
    readiness_protocol="http"
    if [ "{{.IngressProtocol}}" = "wss" ]; then
        readiness_protocol="https"
    fi

    READINESS_LOG=$TMPDIR/wstunnel-readiness.log
    : > "$READINESS_LOG"

    while [ "$consecutive" -lt 3 ] && [ "$attempt" -lt "$max_attempts" ]; do
        attempt=$((attempt + 1))
        {
            echo "=== readiness attempt $attempt @ $(date -u +%FT%TZ) ==="
        } >> "$READINESS_LOG"
        http_code=$(curl -s -k -v -o /dev/null -w "%{http_code}" \
            -H "Upgrade: websocket" \
            -H "Connection: Upgrade" \
            -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
            -H "Sec-WebSocket-Version: 13" \
            --connect-timeout 2 \
            --max-time 3 \
            "$readiness_protocol://{{.IngressEndpoint}}:{{.IngressPort}}/{{.RandomPassword}}" 2>> "$READINESS_LOG" || echo "000")

        echo "wstunnel readiness attempt $attempt/$max_attempts: HTTP status=$http_code (stable $consecutive/3)"
        if [ "$http_code" != "000" ] && [ "$http_code" != "503" ]; then
            consecutive=$((consecutive + 1))
        else
            consecutive=0
            sleep 1
        fi
    done

    if [ "$consecutive" -lt 3 ]; then
        echo "ERROR: wstunnel server did not become stable after $attempt attempts"
        echo "--- last readiness probe transcript ($READINESS_LOG) ---"
        tail -n 60 "$READINESS_LOG" 2>/dev/null || true
        echo "--- end readiness probe transcript ---"
        return 1
    fi
}

start_wstunnel() {
    WSTUNNEL_LOG=$TMPDIR/wstunnel.log
    echo "wstunnel client version: $(./wstunnel --version 2>&1)"

    WSTUNNEL_PROXY_URL="${WSTUNNEL_HTTP_PROXY:-${https_proxy:-${HTTPS_PROXY:-${http_proxy:-${HTTP_PROXY:-}}}}}"
    WSTUNNEL_PROXY_ARGS=""
    if [ -n "$WSTUNNEL_PROXY_URL" ]; then
        echo "Detected proxy in environment ($WSTUNNEL_PROXY_URL), routing wstunnel through it via --http-proxy"
        WSTUNNEL_PROXY_ARGS="--http-proxy $WSTUNNEL_PROXY_URL"
    fi

    WSTUNNEL_CMD="./wstunnel client -L 'udp://127.0.0.1:51821:127.0.0.1:51820?timeout_sec=0' --http-upgrade-path-prefix {{.RandomPassword}} $WSTUNNEL_PROXY_ARGS {{.IngressProtocol}}://{{.IngressEndpoint}}:{{.IngressPort}}"
    echo "Running: RUST_LOG=${WSTUNNEL_LOG_LEVEL:-debug} $WSTUNNEL_CMD"
    RUST_LOG="${WSTUNNEL_LOG_LEVEL:-debug}" ./wstunnel client \
        -L 'udp://127.0.0.1:51821:127.0.0.1:51820?timeout_sec=0' \
        --http-upgrade-path-prefix {{.RandomPassword}} \
        $WSTUNNEL_PROXY_ARGS \
        {{.IngressProtocol}}://{{.IngressEndpoint}}:{{.IngressPort}} > "$WSTUNNEL_LOG" 2>&1 &
    WSTUNNEL_PID=$!
    echo "wstunnel started with PID $WSTUNNEL_PID"
}

ensure_wstunnel_running() {
    for attempt in $(seq 1 10); do
        if ! kill -0 "$WSTUNNEL_PID" 2>/dev/null; then
            echo "wstunnel exited, restarting (attempt $attempt)..."
            echo "--- last wstunnel.log before restart ---"
            tail -n 60 "$WSTUNNEL_LOG" 2>/dev/null || true
            echo "--- end wstunnel.log ---"
            start_wstunnel
        elif grep -qE "Invalid status code: 503|Invalid protocol version" "$WSTUNNEL_LOG" 2>/dev/null; then
            echo "wstunnel reported a protocol/status error, restarting (attempt $attempt)..."
            echo "--- matching wstunnel.log lines ---"
            grep -E "Invalid status code: 503|Invalid protocol version" "$WSTUNNEL_LOG" 2>/dev/null || true
            echo "--- end matching lines ---"
            kill "$WSTUNNEL_PID" 2>/dev/null || true
            : > "$WSTUNNEL_LOG"
            start_wstunnel
        else
            return 0
        fi
        sleep 1
    done

    echo "ERROR: wstunnel did not stay healthy"
    echo "--- final wstunnel.log ---"
    tail -n 100 "$WSTUNNEL_LOG" 2>/dev/null || true
    echo "--- end final wstunnel.log ---"
    return 1
}

wait_for_wireguard_interface() {
    iface="$1"
    pid="$2"
    max_attempts="${3:-40}"
    for attempt in $(seq 1 "$max_attempts"); do
        if ip link show "$iface" >/dev/null 2>&1; then
            return 0
        fi
        if ! kill -0 "$pid" 2>/dev/null; then
            echo "ERROR: wireguard-go exited before interface $iface appeared"
            return 1
        fi
        sleep 0.25
    done
    echo "ERROR: interface $iface did not appear"
    return 1
}

wait_for_wstunnel_server

# Start wstunnel in background
echo "Starting wstunnel..."
cd $TMPDIR
start_wstunnel
ensure_wstunnel_running

# Start WireGuard
echo "Starting WireGuard on interface $WG_IFACE..."
WG_I_PREFER_BUGGY_USERSPACE_TO_POLISHED_KMOD=1 WG_SOCKET_DIR=$TMPDIR  ./wireguard-go $WG_IFACE &
WG_PID=$!

wait_for_wireguard_interface "$WG_IFACE" "$WG_PID"

# Configure WireGuard interface
echo "Configuring WireGuard interface $WG_IFACE..."
ip link set $WG_IFACE up
ip addr add 10.7.0.2/32 dev $WG_IFACE
./wg setconf $WG_IFACE $WG_IFACE.conf
ip link set dev $WG_IFACE mtu {{.WGMTU}}

# Add routes for pod and service CIDRs
echo "Adding routes..."
ip route add 10.7.0.0/16 dev $WG_IFACE || true
ip route add 10.96.0.0/16 dev $WG_IFACE || true
ip route add {{.PodCIDRCluster}} dev $WG_IFACE || true
ip route add {{.ServiceCIDR}} dev $WG_IFACE || true

echo "=== Full mesh network configured successfully ==="
echo "Testing connectivity..."
ping_success=0
for attempt in $(seq 1 10); do
    echo "Ping attempt $attempt/10 to WireGuard server..."
    if ping -c 1 -W 2 10.7.0.1 >/dev/null 2>&1; then
        ping_success=1
        break
    fi
    sleep 1
done

if [ "$ping_success" -ne 1 ]; then
    echo "ERROR: WireGuard server 10.7.0.1 is unreachable"
    echo "--- wstunnel client status ---"
    if kill -0 "$WSTUNNEL_PID" 2>/dev/null; then
        echo "wstunnel client (PID $WSTUNNEL_PID) is still running"
    else
        echo "wstunnel client (PID $WSTUNNEL_PID) is NOT running"
    fi
    echo "--- full wstunnel.log ($WSTUNNEL_LOG) ---"
    cat "$WSTUNNEL_LOG" 2>/dev/null || echo "(log not found)"
    echo "--- end wstunnel.log ---"
    if grep -q "Cannot connect to tcp endpoint" "$WSTUNNEL_LOG" 2>/dev/null; then
        echo "wstunnel reported raw TCP connect failures to the ingress endpoint."
        echo "Running a one-off curl probe against the same endpoint right now for comparison..."
        diag_protocol="http"
        if [ "{{.IngressProtocol}}" = "wss" ]; then
            diag_protocol="https"
        fi
        curl -s -k -v -o /dev/null \
            -w "diag probe result: HTTP status=%{http_code} time_connect=%{time_connect}s time_total=%{time_total}s\n" \
            --connect-timeout 5 --max-time 8 \
            "$diag_protocol://{{.IngressEndpoint}}:{{.IngressPort}}/{{.RandomPassword}}" \
            > "$TMPDIR/wstunnel-tcp-diag.log" 2>&1
        cat "$TMPDIR/wstunnel-tcp-diag.log"
        echo "--- end diag probe ---"
    fi
    echo "--- wireguard-go status ---"
    if kill -0 "$WG_PID" 2>/dev/null; then
        echo "wireguard-go (PID $WG_PID) is still running"
    else
        echo "wireguard-go (PID $WG_PID) is NOT running"
    fi
    ./wg show "$WG_IFACE" 2>&1 || true
    exit 1
fi

# Execute the original command passed as arguments
$@
EOFSLIRP

chmod +x $TMPDIR/slirp.sh

echo "=== Starting network namespace ==="

# Detect best unshare strategy for this environment
# Priority: 1) Config file setting, 2) Environment variable, 3) Default (auto)
# Valid values: auto, map-root, map-user, none
CONFIG_UNSHARE_MODE="{{.UnshareMode}}"
UNSHARE_MODE="${SLIRP_USERNS_MODE:-$CONFIG_UNSHARE_MODE}"
UNSHARE_FLAGS=""

echo "Unshare mode from config: $CONFIG_UNSHARE_MODE"
echo "Active unshare mode: $UNSHARE_MODE"

case "$UNSHARE_MODE" in
    "none")
        echo "User namespace disabled (mode=none)"
        echo "WARNING: Running without user namespace. Some operations may fail."
        UNSHARE_FLAGS=""
        ;;
    
    "map-root")
        echo "Using --map-root-user mode (mode=map-root)"
        UNSHARE_FLAGS="--user --map-root-user"
        ;;
    
    "map-user")
        echo "Using --map-user/--map-group mode (mode=map-user)"
        UNSHARE_FLAGS="--user --map-user=$(id -u) --map-group=$(id -g)"
        ;;
    
    "auto"|*)
        echo "Auto-detecting user namespace configuration (mode=auto)"
        
        # Check if user namespaces are allowed
        if [ -e /proc/sys/kernel/unprivileged_userns_clone ]; then
            USERNS_ALLOWED=$(cat /proc/sys/kernel/unprivileged_userns_clone 2>/dev/null || echo "1")
        else
            USERNS_ALLOWED="1"  # Assume allowed if file doesn't exist
        fi
        
        if [ "$USERNS_ALLOWED" != "1" ]; then
            echo "User namespaces are disabled on this system"
            UNSHARE_FLAGS=""
        else
            # Check for newuidmap/newgidmap and subuid/subgid support
            if command -v newuidmap &> /dev/null && command -v newgidmap &> /dev/null && [ -f /etc/subuid ] && [ -f /etc/subgid ]; then
                SUBUID_START=$(grep "^$(id -un):" /etc/subuid 2>/dev/null | cut -d: -f2)
                SUBUID_COUNT=$(grep "^$(id -un):" /etc/subuid 2>/dev/null | cut -d: -f3)
                
                if [ -n "$SUBUID_START" ] && [ -n "$SUBUID_COUNT" ] && [ "$SUBUID_COUNT" -gt 0 ]; then
                    echo "Using user namespace with UID/GID mapping (subuid available)"
                    UNSHARE_FLAGS="--user --map-user=$(id -u) --map-group=$(id -g)"
                else
                    echo "Using user namespace with root mapping (no subuid)"
                    UNSHARE_FLAGS="--user --map-root-user"
                fi
            else
                echo "Using user namespace with root mapping (no newuidmap/newgidmap)"
                UNSHARE_FLAGS="--user --map-root-user"
            fi
        fi
        ;;
esac

echo "Unshare flags: $UNSHARE_FLAGS"

# Execute the script within unshare
unshare $UNSHARE_FLAGS --net --mount $TMPDIR/slirp.sh "$@" &
JOBPID=$!
echo "$JOBPID" > /tmp/slirp_jobpid

for attempt in $(seq 1 20); do
    if kill -0 "$JOBPID" 2>/dev/null; then
        break
    fi
    sleep 0.1
done

if ! kill -0 "$JOBPID" 2>/dev/null; then
    echo "ERROR: unshare job did not start"
    exit 1
fi

# Wait for slirp.sh to report the PID that actually owns the new
# net/user namespaces. This can differ from $JOBPID: 'unshare
# --map-root-user'/'--map-user' forks internally to write the uid/gid
# map before exec'ing into slirp.sh, so '$!' on the outer job is not a
# reliable handle for slirp4netns to join.
NETNS_PID_FILE=$TMPDIR/netns.pid
NETNS_PID=""
for attempt in $(seq 1 50); do
    if [ -s "$NETNS_PID_FILE" ]; then
        NETNS_PID=$(cat "$NETNS_PID_FILE")
        break
    fi
    if ! kill -0 "$JOBPID" 2>/dev/null; then
        echo "ERROR: unshare job exited before reporting its namespace PID"
        exit 1
    fi
    sleep 0.1
done

if [ -z "$NETNS_PID" ]; then
    echo "ERROR: timed out waiting for namespace PID from $NETNS_PID_FILE"
    exit 1
fi

echo "unshare wrapper PID=$JOBPID, actual namespace-owning PID=$NETNS_PID"

# Create the tap0 device with slirp4netns
echo "Starting slirp4netns..."
SLIRP_SOCKET=/tmp/slirp4netns_$NETNS_PID.sock
SLIRP_USERNS_ARG=""
if echo "$UNSHARE_FLAGS" | grep -q -- "--user"; then
    SLIRP_USERNS_ARG="--userns-path /proc/$NETNS_PID/ns/user"
fi
./slirp4netns $SLIRP_USERNS_ARG --api-socket $SLIRP_SOCKET --configure --mtu=65520 --disable-host-loopback $NETNS_PID tap0 &
SLIRPPID=$!

for attempt in $(seq 1 50); do
    if [ -S "$SLIRP_SOCKET" ]; then
        break
    fi
    if ! kill -0 "$SLIRPPID" 2>/dev/null; then
        echo "ERROR: slirp4netns exited before becoming ready"
        exit 1
    fi
    sleep 0.1
done

if [ ! -S "$SLIRP_SOCKET" ]; then
    echo "ERROR: slirp4netns socket $SLIRP_SOCKET was not created"
    exit 1
fi

# Bring the main job to foreground and wait for completion
echo "=== Bringing job to foreground ==="
fg 1

EOFMESH
