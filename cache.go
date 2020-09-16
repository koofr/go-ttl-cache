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
	lock   sync.RWMutex
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
	lock       sync.RWMutex
	exit       chan struct{}
	exited     chan struct{}
	isClosed   bool
}

func NewTtlCache(gcInterval time.Duration) *TtlCache {
	c := &TtlCache{
		gcInterval: gcInterval,
		cache:      make(map[string]*ttlCacheEntry),
		exit:       make(chan struct{}, 1),
		exited:     make(chan struct{}, 1),
		isClosed:   false,
	}
	go c.startCleaner()
	return c
}

func (c *TtlCache) Close() {
	c.lock.RLock()
	isClosed := c.isClosed
	c.lock.RUnlock()

	if isClosed {
		return
	}

	c.lock.Lock()
	c.isClosed = true
	c.lock.Unlock()

	c.exit <- struct{}{}
	<-c.exited
}

func (c *TtlCache) startCleaner() {
	if c == nil {
		return
	}
	if c.gcInterval > 0 {
		ticker := time.NewTicker(c.gcInterval)
	gcLoop:
		for {
			select {
			case <-c.exit:
				ticker.Stop()
				break gcLoop
			case now := <-ticker.C:
				if c == nil {
					return
				}
				c.lock.Lock()
				for id, entry := range c.cache {
					entry.lock.RLock()
					expiry := entry.expiry
					entry.lock.RUnlock()

					if expiry != nil && expiry.Before(now) {
						entry.Close()
						delete(c.cache, id)
					}
				}
				c.lock.Unlock()
			}
		}
	} else {
		<-c.exit
	}
	for id, entry := range c.cache {
		entry.Close()
		delete(c.cache, id)
	}
	c.exited <- struct{}{}
}

func (c *TtlCache) ensureEntry(id string) (entry *ttlCacheEntry) {
	c.lock.RLock()
	entry, ok := c.cache[id]
	c.lock.RUnlock()
	if ok {
		return
	}
	c.lock.Lock()
	defer c.lock.Unlock()
	entry, ok = c.cache[id]
	if ok {
		return
	}
	entry = &ttlCacheEntry{}
	c.cache[id] = entry
	return
}

func (c *TtlCache) Get(id string) interface{} {
	c.lock.RLock()
	entry, ok := c.cache[id]
	c.lock.RUnlock()

	if !ok {
		return nil
	}

	entry.lock.RLock()
	defer entry.lock.RUnlock()

	if entry.expiry != nil && !entry.expiry.After(time.Now()) {
		return nil
	}

	return entry.value
}

func (c *TtlCache) Set(id string, value interface{}, ttl time.Duration) {
	var expiry *time.Time
	if ttl != NeverExpires {
		expiryTime := time.Now().Add(ttl)
		expiry = &expiryTime
	}
	entry := c.ensureEntry(id)

	entry.lock.Lock()
	defer entry.lock.Unlock()

	entry.Close() // close potential existing
	entry.value = value
	entry.expiry = expiry
}

func (c *TtlCache) Delete(id string) {
	c.lock.Lock()

	elem, ok := c.cache[id]
	if !ok {
		c.lock.Unlock()

		return
	}

	delete(c.cache, id)

	c.lock.Unlock()

	elem.Close()
}

func (c *TtlCache) GetOrElseUpdate(id string, ttl time.Duration,
	create func() (interface{}, error)) (value interface{}, err error) {

	value = c.Get(id)
	if value != nil {
		return
	}

	entry := c.ensureEntry(id)
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

func (c *TtlCache) Foreach(f func(string, interface{})) {
	c.lock.RLock()
	i := 0
	keys := make([]string, len(c.cache))
	for key := range c.cache {
		keys[i] = key
		i++
	}
	c.lock.RUnlock()

	for _, key := range keys {
		c.lock.RLock()
		entry, ok := c.cache[key]
		c.lock.RUnlock()
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
