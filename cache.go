package ttlcache

import (
	"sync"
	"time"
)

type TtlCache struct {
	gcInterval time.Duration
	cache      map[string]*ttlCacheEntry
	lock       *sync.RWMutex
	exit       chan struct{}
}

type ttlCacheEntry struct {
	value  interface{}
	expiry *time.Time
	lock   *sync.RWMutex
}

func NewTtlCache(gcInterval time.Duration) *TtlCache {
	var lock sync.RWMutex
	cache := &TtlCache{
		gcInterval: gcInterval,
		cache:      make(map[string]*ttlCacheEntry),
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
	if cache == nil {
		return
	}
	if cache.gcInterval < 1 {
		return
	}
	ticker := time.NewTicker(cache.gcInterval)
	for {
		select {
		case _ = <-cache.exit:
			ticker.Stop()
		case now := <-ticker.C:
			if cache == nil {
				return
			}
			cache.lock.Lock()
			for id, entry := range cache.cache {
				if entry.expiry != nil && entry.expiry.Before(now) {
					delete(cache.cache, id)
				}
			}
			cache.lock.Unlock()
		}

	}
}

func (cache *TtlCache) ensureEntry(id string) (entry *ttlCacheEntry) {
	cache.lock.RLock()
	entry, ok := cache.cache[id]
	cache.lock.RUnlock()
	if ok {
		return
	}
	cache.lock.Lock()
	defer cache.lock.Unlock()
	entry, ok = cache.cache[id]
	if ok {
		return
	}
	var lock sync.RWMutex
	entry = &ttlCacheEntry{lock: &lock}
	cache.cache[id] = entry
	return
}

func (cache *TtlCache) Get(id string) interface{} {
	cache.lock.RLock()
	defer cache.lock.RUnlock()
	entry, ok := cache.cache[id]
	if !ok {
		return nil
	}

	entry.lock.RLock()
	defer entry.lock.RUnlock()

	if ok && entry.expiry != nil && entry.expiry.After(time.Now()) {
		return entry.value
	} else {
		return nil
	}
}

func (cache *TtlCache) Set(id string, value interface{}, ttl time.Duration) {
	expiry := time.Now().Add(ttl)
	entry := cache.ensureEntry(id)

	entry.lock.Lock()
	defer entry.lock.Unlock()

	entry.value = value
	entry.expiry = &expiry
}

func (cache *TtlCache) Delete(id string) {
	cache.lock.Lock()
	defer cache.lock.Unlock()

	delete(cache.cache, id)
}

func (cache *TtlCache) GetOrElseUpdate(id string, ttl time.Duration,
	create func() (interface{}, error)) (value interface{}, err error) {

	value = cache.Get(id)
	if value != nil {
		return
	}

	entry := cache.ensureEntry(id)
	entry.lock.Lock()
	defer entry.lock.Unlock()

	if entry.value != nil && entry.expiry != nil && entry.expiry.After(time.Now()) {
		return entry.value, nil
	} else {
		value, err = create()
		if err != nil {
			nonCached, ok := IsDoNotCache(err)
			if ok {
				expiry := time.Unix(0, 0)
				entry.expiry = &expiry //will be GCed if nobody else is using it
				value = nonCached
				err = nil
			}
			return
		}
		entry.value = value
		expiry := time.Now().Add(ttl)
		entry.expiry = &expiry
	}
	return
}

type DoNotCache struct {
	Value interface{}
}

func (d DoNotCache) Error() string {
	return "This contains an uncachable value"
}

func IsDoNotCache(err error) (value interface{}, ok bool) {
	dnc, ok := err.(DoNotCache)
	if !ok {
		return
	}
	value = dnc.Value
	return
}
