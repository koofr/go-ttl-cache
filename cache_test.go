package ttlcache

import (
	"sync"
	"testing"
	"time"
)

const min = 1 * time.Minute

type Closer struct {
	closed     bool
	sleep      time.Duration
	closedLock sync.RWMutex
}

func NewCloser(closed bool, sleep time.Duration) *Closer {
	return &Closer{
		closed: closed,
		sleep:  sleep,
	}
}

func (c *Closer) IsClosed() bool {
	c.closedLock.RLock()
	defer c.closedLock.RUnlock()
	return c.closed
}

func (c *Closer) Close() error {
	if c.sleep != 0 {
		time.Sleep(c.sleep)
	}
	c.closedLock.Lock()
	c.closed = true
	c.closedLock.Unlock()
	return nil
}

func TestCaching(t *testing.T) {
	cache := NewTtlCache(min)
	defer cache.Close()

	val := cache.Get("foo")
	if val != nil {
		t.Error("val should not be defined at start")
	}

	cache.Set("foo", 123, min)

	val = cache.Get("foo")
	if val != 123 {
		t.Error("val should be 123 but is ", val)
	}

	cache.Delete("foo")
	val = cache.Get("foo")
	if val != nil {
		t.Error("val should not be defined after deletion")
	}

	val, err := cache.GetOrElseUpdate("foo", min, func() (interface{}, error) {
		return "new value", nil
	})
	if err != nil {
		t.Error(err)
	}
	if val != "new value" {
		t.Error("val should be 'new value' but is ", val)
	}

	val, err = cache.GetOrElseUpdate("foo", min, func() (interface{}, error) {
		t.Error("producer ran again")
		return "wrong value", nil
	})
	if err != nil {
		t.Error(err)
	}
	if val != "new value" {
		t.Error("val should be 'new value' but is ", val)
	}
}

func TestDisabledCaching(t *testing.T) {
	cache := NewTtlCache(min)
	defer cache.Close()

	val := cache.Get("foo")
	if val != nil {
		t.Error("val should not be defined at start")
	}

	cache.Set("foo", 123, 0)

	val = cache.Get("foo")
	if val != nil {
		t.Error("val should not be defined")
	}

	cache.Delete("foo")
	val = cache.Get("foo")
	if val != nil {
		t.Error("val should not be defined after deletion")
	}

	val, err := cache.GetOrElseUpdate("foo", 0, func() (interface{}, error) {
		return "new value", nil
	})
	if err != nil {
		t.Error(err)
	}
	if val != "new value" {
		t.Error("val should be 'new value' but is ", val)
	}

	called := false

	val, err = cache.GetOrElseUpdate("foo", 0, func() (interface{}, error) {
		called = true
		return "new value", nil
	})
	if err != nil {
		t.Error(err)
	}
	if val != "new value" {
		t.Error("val should be 'new value' but is ", val)
	}
	if !called {
		t.Error("producer was not run again")
	}
}

func TestUpdateContention(t *testing.T) {
	cache := NewTtlCache(min)
	defer cache.Close()

	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			time.Sleep(time.Duration(i) * 10 * time.Millisecond)
			val, err := cache.GetOrElseUpdate("foo", min, func() (interface{}, error) {
				time.Sleep(200 * time.Millisecond)
				return i, nil
			})
			if err != nil {
				t.Error(err)
			}
			if val != 0 {
				t.Error("val should be 0 but is ", val)
			}

			wg.Done()
		}(i)
	}

	cache.GetOrElseUpdate("unrelated", min, func() (interface{}, error) {
		//this should run concurrently with the go-routines from the loop above if
		// entry level locking works properly
		time.Sleep(100 * time.Millisecond)
		return 0, nil
	})

	wg.Wait()

	val := cache.Get("foo")
	if val != 0 {
		t.Error("val should be 0 but is ", val)
	}
}

func TestGetOrElseUpdateLocking(t *testing.T) {
	cache := NewTtlCache(min)
	defer cache.Close()

	foo1Updating := make(chan bool)
	foo1Continue := make(chan bool)
	foo1Done := make(chan bool)
	foo2Before := make(chan bool)
	foo2Done := make(chan bool)

	go func() {
		val, err := cache.GetOrElseUpdate("foo", min, func() (interface{}, error) {
			foo1Updating <- true

			<-foo1Continue

			return 0, nil
		})
		if err != nil {
			t.Error(err)
		}
		if val != 0 {
			t.Error("val should be 0 but is ", val)
		}

		foo1Done <- true
	}()

	go func() {
		<-foo1Updating

		foo2Before <- true

		val, err := cache.GetOrElseUpdate("foo", min, func() (interface{}, error) {
			return 1, nil
		})
		if err != nil {
			t.Error(err)
		}
		if val != 0 {
			t.Error("val should be 0 but is ", val)
		}

		foo2Done <- true
	}()

	<-foo2Before

	val, err := cache.GetOrElseUpdate("bar", min, func() (interface{}, error) {
		return 42, nil
	})
	if err != nil {
		t.Error(err)
	}
	if val != 42 {
		t.Error("val should be 42 but is ", val)
	}

	foo1Continue <- true

	<-foo1Done
	<-foo2Done
}

func TestNonCaching(t *testing.T) {
	cache := NewTtlCache(min)
	defer cache.Close()

	val, err := cache.GetOrElseUpdate("foo", min, func() (interface{}, error) {
		return nil, DoNotCache{123}
	})
	if err != nil {
		t.Error(err)
	}
	if val != 123 {
		t.Error("val should be 123 but is ", val)
	}

	val = cache.Get("foo")
	if val != nil {
		t.Error("val should not be defined")
	}
}

func TestSetNeverExpires(t *testing.T) {
	cache := NewTtlCache(10 * time.Millisecond)
	defer cache.Close()

	cache.Set("foo", 123, NeverExpires)

	val := cache.Get("foo")
	if val == nil {
		t.Error("val should be defined")
	}

	time.Sleep(20 * time.Millisecond)

	val = cache.Get("foo")
	if val == nil {
		t.Error("val should be defined after GC cycle")
	}
}

func TestGetOrElseUpdateNeverExpires(t *testing.T) {
	cache := NewTtlCache(10 * time.Millisecond)
	defer cache.Close()

	val, err := cache.GetOrElseUpdate("foo", NeverExpires, func() (interface{}, error) {
		return 123, nil
	})
	if err != nil {
		t.Error(err)
	}
	if val != 123 {
		t.Error("val should be 123 but is ", val)
	}

	val = cache.Get("foo")
	if val == nil {
		t.Error("val should be defined")
	}

	time.Sleep(20 * time.Millisecond)

	val = cache.Get("foo")
	if val == nil {
		t.Error("val should be defined after GC cycle")
	}
}

func TestCleaner(t *testing.T) {
	cache := NewTtlCache(500 * time.Millisecond)
	defer cache.Close()

	if _, ok := cache.cache["foo"]; ok {
		t.Error("val should not be defined")
	}

	go func() {
		cache.GetOrElseUpdate("foo", 250*time.Millisecond, func() (interface{}, error) {
			time.Sleep(1000 * time.Millisecond)
			return "bar", nil
		})
	}()

	time.Sleep(80 * time.Millisecond)

	cache.lock.Lock()
	if _, ok := cache.cache["foo"]; !ok {
		t.Error("val should be defined")
	}
	cache.lock.Unlock()

	time.Sleep(700 * time.Millisecond)

	cache.lock.Lock()
	if _, ok := cache.cache["foo"]; !ok {
		t.Error("val should be defined")
	}
	cache.lock.Unlock()

	time.Sleep(1 * time.Second)

	cache.lock.Lock()
	if _, ok := cache.cache["foo"]; ok {
		t.Error("val should not be defined")
	}
	cache.lock.Unlock()
}

func TestDisabledCleaner(t *testing.T) {
	cache := NewTtlCache(0)
	defer cache.Close()

	cache.Set("foo", 123, 0)

	val := cache.Get("foo")
	if val != nil {
		t.Error("val should not be defined")
	}
}

func TestCloseOnDelete(t *testing.T) {
	cache := NewTtlCache(min)
	defer cache.Close()

	c := NewCloser(false, 0)
	cache.Set("foo", c, min)
	if cache.Get("foo").(*Closer).IsClosed() {
		t.Error("foo should not be closed")
	}
	if c.IsClosed() {
		t.Error("c should not be closed")
	}

	cache.Delete("foo")
	if !c.IsClosed() {
		t.Error("c should be closed")
	}
	if cache.Get("foo") != nil {
		t.Error("foo should not be gettable")
	}
}

func TestCloseOnTimeout(t *testing.T) {
	cache := NewTtlCache(1)
	defer cache.Close()

	c := NewCloser(false, 0)
	cache.Set("foo", c, 0)
	time.Sleep(50 * time.Millisecond)
	if !c.IsClosed() {
		t.Error("c should be closed")
	}
}

func TestCloseOnSetOverride(t *testing.T) {
	cache := NewTtlCache(0)
	defer cache.Close()

	c1 := NewCloser(false, 0)
	c2 := NewCloser(false, 0)

	cache.Set("foo", c1, min)

	if cache.Get("foo").(*Closer).IsClosed() {
		t.Error("foo should not be closed")
	}
	if c1.IsClosed() {
		t.Error("c1 should not be closed")
	}

	cache.Set("foo", c2, min)

	if cache.Get("foo").(*Closer).IsClosed() {
		t.Error("foo should not be closed")
	}
	if !c1.IsClosed() {
		t.Error("c1 should be closed")
	}
	if c2.IsClosed() {
		t.Error("c2 should not be closed")
	}
}

func TestCloseOnGetOrElseUpdateTimeout(t *testing.T) {
	cache := NewTtlCache(0)
	defer cache.Close()

	c1 := NewCloser(false, 0)
	c2 := NewCloser(false, 0)

	cache.Set("foo", c1, 0)
	time.Sleep(10 * time.Millisecond)

	if c1.IsClosed() {
		t.Error("c1 should not be closed")
	}
	cache.GetOrElseUpdate("foo", min,
		func() (interface{}, error) {
			return c2, nil
		})

	if cache.Get("foo").(*Closer).IsClosed() {
		t.Error("foo should not be closed")
	}
	if !c1.IsClosed() {
		t.Error("c1 should be closed")
	}
	if c2.IsClosed() {
		t.Error("c2 should not be closed")
	}
}

func TestCloseOnCacheClose(t *testing.T) {
	cache := NewTtlCache(0)

	c1 := NewCloser(false, 70*time.Millisecond)
	c2 := NewCloser(false, 70*time.Millisecond)

	cache.Set("1", c1, 0)
	cache.Set("2", c2, min)
	time.Sleep(50 * time.Millisecond)

	if c1.IsClosed() {
		t.Error("c1 should not be closed")
	}

	if c2.IsClosed() {
		t.Error("c2 should not be closed")
	}

	beforeClose := time.Now()

	cache.Close()

	if !c1.IsClosed() {
		t.Error("c1 should be closed")
	}

	if !c2.IsClosed() {
		t.Error("c2 should be closed")
	}

	if time.Now().Sub(beforeClose) < 130*time.Millisecond {
		t.Error("close should wait for entries to be closed")
	}
}

func TestCloseOnCacheCloseWithGC(t *testing.T) {
	cache := NewTtlCache(10 * time.Millisecond)

	c1 := NewCloser(false, 70*time.Millisecond)
	c2 := NewCloser(false, 70*time.Millisecond)

	cache.Set("1", c1, 0)
	cache.Set("2", c2, min)
	time.Sleep(50 * time.Millisecond)

	if c1.IsClosed() {
		t.Error("c1 should not be closed")
	}

	if c2.IsClosed() {
		t.Error("c2 should not be closed")
	}

	beforeClose := time.Now()

	cache.Close()

	if !c1.IsClosed() {
		t.Error("c1 should be closed")
	}

	if !c2.IsClosed() {
		t.Error("c2 should be closed")
	}

	if time.Now().Sub(beforeClose) < 60*time.Millisecond {
		t.Error("close should wait for entries to be closed")
	}
}

func TestDoubleClose(t *testing.T) {
	cache := NewTtlCache(10 * time.Millisecond)

	c1 := NewCloser(false, 70*time.Millisecond)
	c2 := NewCloser(false, 70*time.Millisecond)

	cache.Set("1", c1, 0)
	cache.Set("2", c2, min)
	time.Sleep(50 * time.Millisecond)

	if c1.IsClosed() {
		t.Error("c1 should not be closed")
	}

	if c2.IsClosed() {
		t.Error("c2 should not be closed")
	}

	beforeClose := time.Now()

	cache.Close()
	cache.Close()

	if !c1.IsClosed() {
		t.Error("c1 should be closed")
	}

	if !c2.IsClosed() {
		t.Error("c2 should be closed")
	}

	if time.Now().Sub(beforeClose) < 60*time.Millisecond {
		t.Error("close should wait for entries to be closed")
	}
}
