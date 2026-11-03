package ipgeo

import (
	"encoding/binary"
	"net/netip"
	"sync"
)

const cacheShards = 256

// cacheShard 是独立加锁的分片：命中走 RLock，不阻塞其他 CPU。
type cacheShard struct {
	mu   sync.RWMutex
	m    map[netip.Addr]*Info
	ring []netip.Addr // FIFO 淘汰顺序
	pos  int
}

func (s *cacheShard) get(a netip.Addr) (*Info, bool) {
	s.mu.RLock()
	v, ok := s.m[a]
	s.mu.RUnlock()
	return v, ok
}

func (s *cacheShard) put(a netip.Addr, v *Info) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.m[a]; ok {
		return
	}
	if len(s.ring) < cap(s.ring) {
		s.ring = append(s.ring, a)
		s.m[a] = v
		return
	}
	if len(s.ring) == 0 {
		return
	}
	delete(s.m, s.ring[s.pos])
	s.ring[s.pos] = a
	s.m[a] = v
	s.pos++
	if s.pos == len(s.ring) {
		s.pos = 0
	}
}

// Cache 是按 IP 分片的结果缓存，容量均摊到 256 片。
type Cache struct {
	shards [cacheShards]cacheShard
}

func NewCache(capacity int) *Cache {
	if capacity < cacheShards {
		capacity = cacheShards
	}
	per := capacity / cacheShards
	c := new(Cache)
	for i := range c.shards {
		c.shards[i].m = make(map[netip.Addr]*Info, per)
		c.shards[i].ring = make([]netip.Addr, 0, per)
	}
	return c
}

func (c *Cache) Get(a netip.Addr) (*Info, bool) {
	return c.shards[shardOf(a)&(cacheShards-1)].get(a)
}

func (c *Cache) Put(a netip.Addr, v *Info) {
	c.shards[shardOf(a)&(cacheShards-1)].put(a, v)
}

func shardOf(a netip.Addr) uint32 {
	var h uint64
	if a.Is4() {
		b := a.As4()
		h = uint64(binary.BigEndian.Uint32(b[:]))
	} else {
		b := a.As16()
		h = binary.LittleEndian.Uint64(b[:]) ^ binary.LittleEndian.Uint64(b[8:])
	}
	h *= 0x9E3779B97F4A7C15
	h ^= h >> 29
	return uint32(h >> 32)
}
