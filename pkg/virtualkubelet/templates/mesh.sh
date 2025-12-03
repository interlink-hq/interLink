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

# Download wstunnel
echo "Downloading wstunnel..."
if ! curl -L -f -k {{.WSTunnelExecutableURL}} -o wstunnel; then
    echo "ERROR: Failed to download wstunnel"
    exit 1
fi
chmod +x wstunnel

# Download wireguard-go
echo "Downloading wireguard-go..."
if ! curl -L -f -k {{.WireguardGoURL}} -o wireguard-go; then
    echo "ERROR: Failed to download wireguard-go"
    exit 1
fi
chmod +x wireguard-go

# Download and build wg tool
echo "Downloading wg tool..."
if ! curl -L -f -k {{.WgToolURL}} -o wg; then
    echo "ERROR: Failed to download wg tools"
    exit 1
fi
chmod +x wg

# Download slirp4netns
echo "Downloading slirp4netns..."
if ! curl -L -f -k {{.Slirp4netnsURL}} -o slirp4netns; then
    echo "ERROR: Failed to download slirp4netns"
    exit 1
fi
chmod +x slirp4netns

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

# Ensure PATH includes tmpdir
export PATH=$TMPDIR:$PATH:/usr/sbin:/sbin

# Get WireGuard interface name from parent
WG_IFACE="{{.WGInterfaceName}}"

echo "=== Inside network namespace ==="
echo "Using WireGuard interface: $WG_IFACE"

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


# Start wstunnel in background
echo "Starting wstunnel..."
cd $TMPDIR
./wstunnel client -L 'udp://127.0.0.1:51821:127.0.0.1:51820?timeout_sec=0' --http-upgrade-path-prefix {{.RandomPassword}} ws://{{.IngressEndpoint}}:80 &
WSTUNNEL_PID=$!

# Give wstunnel time to establish connection
sleep 3

# Start WireGuard
echo "Starting WireGuard on interface $WG_IFACE..."
WG_I_PREFER_BUGGY_USERSPACE_TO_POLISHED_KMOD=1 WG_SOCKET_DIR=$TMPDIR  ./wireguard-go $WG_IFACE &
WG_PID=$!

# Give WireGuard time to create interface
sleep 2

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
ping -c 1 -W 2 10.7.0.1 || echo "Warning: Cannot ping WireGuard server"

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
sleep 0.1
JOBPID=$!
echo "$JOBPID" > /tmp/slirp_jobpid

# Wait for the job pid to be established
sleep 1

# Create the tap0 device with slirp4netns
echo "Starting slirp4netns..."
./slirp4netns --api-socket /tmp/slirp4netns_$JOBPID.sock --configure --mtu=65520 --disable-host-loopback $JOBPID tap0 &
SLIRPPID=$!

# Wait a bit for slirp4netns to be ready
sleep 5

# Bring the main job to foreground and wait for completion
echo "=== Bringing job to foreground ==="
fg 1

EOFMESH