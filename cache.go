package ttlcache

import (
	"io"
	"sync"
	"time"
)

const (
	NeverExpires = time.Duration(-1)
)

type ttlCacheEntry struct {
	value  interface{}
	expiry *time.Time
	lock   *sync.RWMutex
}

func (e *ttlCacheEntry) Close() {
	c, ok := e.value.(io.Closer)
	if ok {
		c.Close()
	}
}

type TtlCache struct {
	gcInterval time.Duration
	cache      map[string]*ttlCacheEntry
	lock       *sync.RWMutex
	exit       chan struct{}
	exited     chan struct{}
	isClosed   bool
}

func NewTtlCache(gcInterval time.Duration) *TtlCache {
	var lock sync.RWMutex
	cache := &TtlCache{
		gcInterval: gcInterval,
		cache:      make(map[string]*ttlCacheEntry),
		lock:       &lock,
		exit:       make(chan struct{}, 1),
		exited:     make(chan struct{}, 1),
		isClosed:   false,
	}
	go cache.startCleaner()
	return cache
}

func (cache *TtlCache) Close() {
	cache.lock.RLock()
	isClosed := cache.isClosed
	cache.lock.RUnlock()

	if isClosed {
		return
	}

	cache.lock.Lock()
	cache.isClosed = true
	cache.lock.Unlock()

	cache.exit <- struct{}{}
	<-cache.exited
}

func (cache *TtlCache) startCleaner() {
	if cache == nil {
		return
	}
	if cache.gcInterval > 0 {
		ticker := time.NewTicker(cache.gcInterval)
	gcLoop:
		for {
			select {
			case _ = <-cache.exit:
				ticker.Stop()
				break gcLoop
			case now := <-ticker.C:
				if cache == nil {
					return
				}
				cache.lock.Lock()
				for id, entry := range cache.cache {
					entry.lock.RLock()
					expiry := entry.expiry
					entry.lock.RUnlock()

					if expiry != nil && expiry.Before(now) {
						entry.Close()
						delete(cache.cache, id)
					}
				}
				cache.lock.Unlock()
			}
		}
	} else {
		<-cache.exit
	}
	for id, entry := range cache.cache {
		entry.Close()
		delete(cache.cache, id)
	}
	cache.exited <- struct{}{}
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
	entry, ok := cache.cache[id]
	cache.lock.RUnlock()

	if !ok {
		return nil
	}

	entry.lock.RLock()
	defer entry.lock.RUnlock()

	if ok && (entry.expiry == nil || entry.expiry.After(time.Now())) {
		return entry.value
	} else {
		return nil
	}
}

func (cache *TtlCache) Set(id string, value interface{}, ttl time.Duration) {
	var expiry *time.Time
	if ttl != NeverExpires {
		expiryTime := time.Now().Add(ttl)
		expiry = &expiryTime
	}
	entry := cache.ensureEntry(id)

	entry.lock.Lock()
	defer entry.lock.Unlock()

	entry.Close() // close potential existing
	entry.value = value
	entry.expiry = expiry
}

func (cache *TtlCache) Delete(id string) {
	cache.lock.Lock()

	elem, ok := cache.cache[id]
	if !ok {
		cache.lock.Unlock()

		return
	}

	delete(cache.cache, id)

	cache.lock.Unlock()

	elem.Close()
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

	if entry.value != nil && (entry.expiry == nil || entry.expiry.After(time.Now())) {
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
		entry.Close()
		entry.value = value

		var expiry *time.Time
		if ttl != NeverExpires {
			expiryTime := time.Now().Add(ttl)
			expiry = &expiryTime
		}
		entry.expiry = expiry
	}
	return
}

func (cache *TtlCache) Foreach(f func(string, interface{})) {
	cache.lock.RLock()
	i := 0
	keys := make([]string, len(cache.cache))
	for key := range cache.cache {
		keys[i] = key
		i++
	}
	cache.lock.RUnlock()

	for _, key := range keys {
		cache.lock.RLock()
		entry, ok := cache.cache[key]
		cache.lock.RUnlock()
		if ok {
			f(key, entry.value)
		}
	}
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
