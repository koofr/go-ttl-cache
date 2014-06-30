package ttlcache

import (
	"sync"
	"time"
)

type TtlCache struct {
	gcInterval time.Duration
	cache      map[string]ttlCacheEntry
	lock       *sync.RWMutex
	exit       chan struct{}
}

type ttlCacheEntry struct {
	value     interface{}
	timestamp *time.Time
}

func NewTtlCache(gcInterval time.Duration) *TtlCache {
	var lock sync.RWMutex
	cache := &TtlCache{
		gcInterval: gcInterval,
		cache:      make(map[string]ttlCacheEntry),
		lock:       &lock,
		exit:       make(chan struct{}, 1),
	}
	go cache.startCleaner()
	return cache
}

func (cache *TtlCache) Close() {
	select {
	case cache.exit <- struct{}{}:
	default:
	}
}

func (cache *TtlCache) startCleaner() {
	ticker := time.NewTicker(cache.gcInterval)
	for {
		select {
		case _ = <-cache.exit:
			ticker.Stop()
		case now := <-ticker.C:
			cache.lock.Lock()
			for id, entry := range cache.cache {
				if entry.timestamp.Before(now) {
					delete(cache.cache, id)
				}
			}
			cache.lock.Unlock()
		}

	}
}

func (cache *TtlCache) unsafeGet(id string) interface{} {
	value, ok := cache.cache[id]
	if ok && value.timestamp.After(time.Now()) {
		return value.value
	} else {
		return nil
	}
}

func (cache *TtlCache) unsafeSet(id string, value interface{}, ttl time.Duration) {
	expiry := time.Now().Add(ttl)
	entry := ttlCacheEntry{value, &expiry}
	cache.cache[id] = entry
}

func (cache *TtlCache) unsafeDelete(id string) {
	delete(cache.cache, id)
}

func (cache *TtlCache) Get(id string) interface{} {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	return cache.unsafeGet(id)
}

func (cache *TtlCache) Set(id string, value interface{}, ttl time.Duration) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	cache.unsafeSet(id, value, ttl)
}

func (cache *TtlCache) Delete(id string) {
	cache.lock.Lock()
	defer cache.lock.Unlock()
	cache.unsafeDelete(id)
}

func (cache *TtlCache) GetOrElseUpdate(id string, ttl time.Duration,
	create func() (interface{}, error)) (value interface{}, err error) {

	value = cache.Get(id)
	if value != nil {
		return
	}

	cache.lock.Lock()
	defer cache.lock.Unlock()

	value = cache.unsafeGet(id)
	if value != nil {
		return
	}

	value, err = create()
	cache.unsafeSet(id, value, ttl)
	return
}
