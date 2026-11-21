package virtualkubelet

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testIP2 = "10.0.0.2"
	testIP3 = "10.0.0.3"
	testIP4 = "10.0.0.4"
	testIP5 = "10.0.0.5"
)

var testIPPool = []string{testIP2, testIP3, testIP4, testIP5}

func pickFromPool(used []string) string {
	return findFirstFreeIP(testIPPool, used, 2, 250)
}

func TestPodIPTracker_AllocateSkipsObservedAndReserved(t *testing.T) {
	var tracker podIPTracker
	tracker.refresh(tracker.begin(), []string{testIP2})

	assert.Equal(t, testIP3, tracker.allocate(pickFromPool))
	assert.Equal(t, testIP4, tracker.allocate(pickFromPool))
}

func TestPodIPTracker_AllocateWithoutFreeIPReservesNothing(t *testing.T) {
	var tracker podIPTracker

	assert.Empty(t, tracker.allocate(func([]string) string { return "" }))
	assert.Equal(t, testIP2, tracker.allocate(pickFromPool))
}

func TestPodIPTracker_RefreshKeepsReservationMadeDuringTheListing(t *testing.T) {
	var tracker podIPTracker

	snapshot := tracker.begin()
	// CreatePod reserves an address while the cluster pods are being listed.
	reserved := tracker.allocate(pickFromPool)
	tracker.refresh(snapshot, nil)

	assert.NotEqual(t, reserved, tracker.allocate(pickFromPool))
}

func TestPodIPTracker_RefreshReleasesReservationMadeBeforeTheListing(t *testing.T) {
	var tracker podIPTracker

	reserved := tracker.allocate(pickFromPool)
	tracker.refresh(tracker.begin(), nil)

	assert.Equal(t, reserved, tracker.allocate(pickFromPool))
}

func TestPodIPTracker_ObservedAddressStaysInUseAfterItsReservationIsReleased(t *testing.T) {
	var tracker podIPTracker

	reserved := tracker.allocate(pickFromPool)
	tracker.refresh(tracker.begin(), []string{reserved})

	assert.NotEqual(t, reserved, tracker.allocate(pickFromPool))
}

func TestPodIPTracker_RefreshReplacesObservedAddresses(t *testing.T) {
	var tracker podIPTracker
	tracker.refresh(tracker.begin(), []string{testIP2, testIP3})
	tracker.refresh(tracker.begin(), []string{testIP3})

	assert.Equal(t, testIP2, tracker.allocate(pickFromPool))
}

func TestPodIPTracker_ConcurrentAllocationsAreUnique(t *testing.T) {
	const workers = 50

	pool := make([]string, workers)
	for i := range pool {
		pool[i] = fmt.Sprintf("10.0.1.%d", i+2)
	}

	var (
		tracker podIPTracker
		wg      sync.WaitGroup
		mu      sync.Mutex
		got     = make(map[string]int)
	)
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ip := tracker.allocate(func(used []string) string {
				return findFirstFreeIP(pool, used, 2, 250)
			})
			mu.Lock()
			got[ip]++
			mu.Unlock()
		}()
	}
	wg.Wait()

	require.Len(t, got, workers)
	for ip, n := range got {
		assert.NotEmpty(t, ip)
		assert.Equal(t, 1, n, "IP %s was allocated more than once", ip)
	}
}
