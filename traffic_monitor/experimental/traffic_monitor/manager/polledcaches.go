package manager

import (
	"github.com/Comcast/traffic_control/traffic_monitor/experimental/common/log"
	"github.com/Comcast/traffic_control/traffic_monitor/experimental/traffic_monitor/cache"
	"github.com/Comcast/traffic_control/traffic_monitor/experimental/traffic_monitor/enum"
	"sync"
)

// UnpolledCachesThreadsafe is a structure containing a map of caches which have yet to be polled, which is threadsafe for multiple readers and one writer.
// This could be made lock-free, if the performance was necessary
type UnpolledCachesThreadsafe struct {
	unpolledCaches *map[enum.CacheName]struct{}
	allCaches      *map[enum.CacheName]struct{}
	initialized    *bool
	m              *sync.RWMutex
	max            uint64
}

// NewUnpolledCachesThreadsafe returns a new UnpolledCachesThreadsafe object.
func NewUnpolledCachesThreadsafe() UnpolledCachesThreadsafe {
	b := false
	return UnpolledCachesThreadsafe{
		m:              &sync.RWMutex{},
		unpolledCaches: &map[enum.CacheName]struct{}{},
		allCaches:      &map[enum.CacheName]struct{}{},
		initialized:    &b,
	}
}

// Get returns the caches. Callers MUST NOT modify. If mutation is necessary, copy the map
func (t *UnpolledCachesThreadsafe) UnpolledCaches() map[enum.CacheName]struct{} {
	t.m.RLock()
	defer t.m.RUnlock()
	return *t.unpolledCaches
}

// Set sets the internal unpolled caches map. This is only safe for one thread of execution. This MUST NOT be called from multiple threads.
func (t *UnpolledCachesThreadsafe) setUnpolledCaches(v map[enum.CacheName]struct{}) {
	t.m.Lock()
	*t.initialized = true
	*t.unpolledCaches = v
	t.m.Unlock()
}

// SetNewCaches takes a list of new caches, which may overlap with the existing caches, diffs them, removes any `unpolledCaches` which aren't in the new list, and sets the list of `polledCaches` (which is only used by this func) to the `newCaches`. This is threadsafe with one writer, along with `setUnpolledCaches`.
func (t *UnpolledCachesThreadsafe) SetNewCaches(newCaches map[enum.CacheName]struct{}) {
	unpolledCaches := copyCaches(t.UnpolledCaches())
	allCaches := copyCaches(*t.allCaches) // not necessary to lock `allCaches`, as the single-writer is the only thing that accesses it.
	for cache, _ := range unpolledCaches {
		if _, ok := newCaches[cache]; !ok {
			delete(unpolledCaches, cache)
		}
	}
	for cache, _ := range allCaches {
		if _, ok := newCaches[cache]; !ok {
			delete(allCaches, cache)
		}
	}
	for cache, _ := range newCaches {
		if _, ok := allCaches[cache]; !ok {
			unpolledCaches[cache] = struct{}{}
			allCaches[cache] = struct{}{}
		}
	}
	*t.allCaches = allCaches
	t.setUnpolledCaches(unpolledCaches)
}

// AnyCachesUnpolled returns whether there are any caches marked as not polled. Also returns true if SetNewCaches() has never been called (assuming there exist caches, if this hasn't been initialized, we couldn't have polled any of them).
func (t *UnpolledCachesThreadsafe) Any() bool {
	t.m.Lock()
	defer t.m.Unlock()
	return !(*t.initialized) || len(*t.unpolledCaches) > 0
}

// copyCaches performs a deep copy of the given map.
func copyCaches(a map[enum.CacheName]struct{}) map[enum.CacheName]struct{} {
	b := map[enum.CacheName]struct{}{}
	for k, _ := range a {
		b[k] = struct{}{}
	}
	return b
}

// SetPolled sets cache which have been polled. This is used to determine when the app has fully started up, and we can start serving. Serving Traffic Router with caches as 'down' which simply haven't been polled yet would be bad. Therefore, a cache is set as 'polled' if it has received different bandwidths from two different ATS ticks, OR if the cache is marked as down (and thus we won't get a bandwidth).
// This is threadsafe for one writer, along with `Set`.
// This is fast if there are no unpolled caches. Moreover, its speed is a function of the number of unpolled caches, not the number of caches total.
func (t *UnpolledCachesThreadsafe) SetPolled(results []cache.Result, lastStatsThreadsafe LastStatsThreadsafe) {
	unpolledCaches := copyCaches(t.UnpolledCaches())
	numUnpolledCaches := len(unpolledCaches)
	if numUnpolledCaches == 0 {
		return
	}
	lastStats := lastStatsThreadsafe.Get()
	for cache, _ := range unpolledCaches {
	innerLoop:
		for _, result := range results {
			if result.Id != cache {
				continue
			}
			if !result.Available || len(result.Errors) > 0 {
				log.Infof("polled %v\n", cache)
				delete(unpolledCaches, cache)
				break innerLoop
			}
		}
		lastStat, ok := lastStats.Caches[cache]
		if !ok {
			continue
		}
		if lastStat.Bytes.PerSec != 0 {
			log.Infof("polled %v\n", cache)
			delete(unpolledCaches, cache)
		}
	}

	if len(unpolledCaches) == numUnpolledCaches {
		return
	}
	t.setUnpolledCaches(unpolledCaches)
	if len(unpolledCaches) != 0 {
		log.Infof("remaining unpolled %v\n", unpolledCaches)
	} else {
		log.Infof("all caches polled, ready to serve!\n")
	}
}
