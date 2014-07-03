package ttlcache

import (
	"sync"
	"testing"
	"time"
)

const min = 1 * time.Minute

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
