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
// A reservation is released by refresh only when it was made before the pod list was
// taken (see begin): such a pod, if it exists, is part of that list. A reservation made
// while the list was in flight is kept, so it cannot be handed out twice.
type podIPTracker struct {
	mu       sync.Mutex
	seq      uint64
	observed map[string]struct{}
	reserved map[string]uint64 // IP -> value of seq when it was reserved
}

// begin must be called right before listing the pods from the cluster. The returned
// snapshot is passed to refresh together with the list result.
func (t *podIPTracker) begin() uint64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.seq
}

// refresh replaces the observed addresses and releases the reservations made up to
// snapshot. Reservations made after it are kept.
func (t *podIPTracker) refresh(snapshot uint64, observed []string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.observed = make(map[string]struct{}, len(observed))
	for _, ip := range observed {
		t.observed[ip] = struct{}{}
	}

	for ip, seq := range t.reserved {
		if seq <= snapshot {
			delete(t.reserved, ip)
		}
	}
}

// allocate picks a free IP and reserves it in a single critical section, so that
// concurrent callers are never given the same address. pick receives the addresses that
// are in use and returns the chosen IP, or an empty string if none is available.
func (t *podIPTracker) allocate(pick func(used []string) string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

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
		t.reserved = make(map[string]uint64)
	}
	t.seq++
	t.reserved[ip] = t.seq
	return ip
}
