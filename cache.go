package ipgeo

import (
	"container/list"
	"sync"
)

type lruEntry struct {
	key  string
	info *Info
}

// lruCache 是带互斥锁的简单 LRU，用于挡住热点 IP 的重复查询。
type lruCache struct {
	mu    sync.Mutex
	size  int
	ll    *list.List
	items map[string]*list.Element
}

func newLRU(size int) *lruCache {
	return &lruCache{
		size:  size,
		ll:    list.New(),
		items: make(map[string]*list.Element, size),
	}
}

func (c *lruCache) get(key string) (*Info, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).info, true
	}
	return nil, false
}

func (c *lruCache) put(key string, info *Info) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*lruEntry).info = info
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry{key: key, info: info})
	c.items[key] = el
	for c.ll.Len() > c.size {
		last := c.ll.Back()
		if last == nil {
			return
		}
		c.ll.Remove(last)
		delete(c.items, last.Value.(*lruEntry).key)
	}
}
