package ttlcache

import (
	"sync"
	"testing"
	"time"
)

const min = 1 * time.Minute

type Closer struct {
	closed bool
}

func (c *Closer) Close() {
	c.closed = true
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

	if _, ok := cache.cache["foo"]; !ok {
		t.Error("val should be defined")
	}

	time.Sleep(700 * time.Millisecond)

	if _, ok := cache.cache["foo"]; !ok {
		t.Error("val should be defined")
	}

	time.Sleep(1 * time.Second)

	if _, ok := cache.cache["foo"]; ok {
		t.Error("val should not be defined")
	}
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

	c := &Closer{false}
	cache.Set("foo", c, min)
	if cache.Get("foo").(*Closer).closed {
		t.Error("foo should not be closed")
	}
	if c.closed {
		t.Error("c should not be closed")
	}

	cache.Delete("foo")
	if !c.closed {
		t.Error("c should be closed")
	}
	if cache.Get("foo") != nil {
		t.Error("foo should not be gettable")
	}
}

func TestCloseOnTimeout(t *testing.T) {
	cache := NewTtlCache(1)
	defer cache.Close()

	c := &Closer{false}
	cache.Set("foo", c, 0)
	time.Sleep(50 * time.Millisecond)
	if !c.closed {
		t.Error("c should be closed")
	}
}

func TestCloseOnSetOverride(t *testing.T) {
	cache := NewTtlCache(0)
	defer cache.Close()

	c1 := &Closer{false}
	c2 := &Closer{false}

	cache.Set("foo", c1, min)

	if cache.Get("foo").(*Closer).closed {
		t.Error("foo should not be closed")
	}
	if c1.closed {
		t.Error("c1 should not be closed")
	}

	cache.Set("foo", c2, min)

	if cache.Get("foo").(*Closer).closed {
		t.Error("foo should not be closed")
	}
	if !c1.closed {
		t.Error("c1 should be closed")
	}
	if c2.closed {
		t.Error("c2 should not be closed")
	}
}

func TestCloseOnGetOrElseUpdateTimeout(t *testing.T) {
	cache := NewTtlCache(0)
	defer cache.Close()

	c1 := &Closer{false}
	c2 := &Closer{false}

	cache.Set("foo", c1, 0)
	time.Sleep(10 * time.Millisecond)

	if c1.closed {
		t.Error("c1 should not be closed")
	}
	cache.GetOrElseUpdate("foo", min,
		func() (interface{}, error) {
			return c2, nil
		})

	if cache.Get("foo").(*Closer).closed {
		t.Error("foo should not be closed")
	}
	if !c1.closed {
		t.Error("c1 should be closed")
	}
	if c2.closed {
		t.Error("c2 should not be closed")
	}
}

func TestCloseOnCacheClose(t *testing.T) {
	cache := NewTtlCache(0)

	c1 := &Closer{false}
	c2 := &Closer{false}

	cache.Set("1", c1, 0)
	cache.Set("2", c2, min)
	time.Sleep(50 * time.Millisecond)

	if c1.closed {
		t.Error("c1 should not be closed")
	}

	if c2.closed {
		t.Error("c2 should not be closed")
	}

	cache.Close()
	time.Sleep(50 * time.Millisecond)

	if !c1.closed {
		t.Error("c1 should be closed")
	}

	if !c2.closed {
		t.Error("c2 should be closed")
	}
}

// cache close
