package virtualkubelet

import "sync"

// podIPTracker keeps track of the IP addresses that cannot be handed out to a new pod
// attached to the VPN. Its zero value is ready to use.
//
// Two kinds of addresses are tracked separately:
//   - observed: addresses found in the interlink.eu/pod-ip annotation of the pods listed
//     from the cluster. Each refresh replaces the whole set.
//   - reserved: addresses handed out by allocate that the cluster may not report yet.
//
// Refresh releases a reservation once its address is observed. Otherwise it remains
// reserved until its owner is explicitly retired.
type podIPTracker struct {
	mu             sync.Mutex
	latestSnapshot uint64
	applied        uint64
	observed       map[string]struct{}
	reserved       map[string]string // IP -> pod UID
	byOwner        map[string]string // pod UID -> IP
}

// begin must be called right before listing the pods from the cluster. The returned
// snapshot is passed to refresh together with the list result.
func (t *podIPTracker) begin() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.latestSnapshot++
	return t.latestSnapshot
}

// refresh replaces the observed addresses and releases reservations whose addresses
// are now visible in the cluster. Superseded list results are ignored.
func (t *podIPTracker) refresh(snapshot uint64, observed []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if snapshot < t.latestSnapshot || snapshot <= t.applied {
		return
	}
	t.applied = snapshot

	t.observed = make(map[string]struct{}, len(observed))
	for _, ip := range observed {
		t.observed[ip] = struct{}{}
	}

	for ip, owner := range t.reserved {
		if _, ok := t.observed[ip]; ok {
			delete(t.reserved, ip)
			delete(t.byOwner, owner)
		}
	}
}

// allocate picks a free IP and reserves it in a single critical section, so that
// concurrent callers are never given the same address. pick receives the addresses that
// are in use and returns the chosen IP, or an empty string if none is available.
func (t *podIPTracker) allocate(owner string, pick func(used []string) string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	if ip, ok := t.byOwner[owner]; owner != "" && ok {
		return ip
	}

	used := make([]string, 0, len(t.observed)+len(t.reserved))
	for ip := range t.observed {
		used = append(used, ip)
	}
	for ip := range t.reserved {
		if _, ok := t.observed[ip]; !ok {
			used = append(used, ip)
		}
	}

	ip := pick(used)
	if ip == "" {
		return ""
	}

	if t.reserved == nil {
		t.reserved = make(map[string]string)
	}
	t.reserved[ip] = owner
	if owner != "" {
		if t.byOwner == nil {
			t.byOwner = make(map[string]string)
		}
		t.byOwner[owner] = ip
	}
	return ip
}

// retire releases any address reserved for owner. Observed addresses remain in use
// until a later refresh confirms they have disappeared from the cluster.
func (t *podIPTracker) retire(owner string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if ip, ok := t.byOwner[owner]; ok {
		delete(t.reserved, ip)
		delete(t.byOwner, owner)
	}
}
