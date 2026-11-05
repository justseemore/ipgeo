package ipgeo

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

var (
	// ErrNotFound 表示库中没有该 IP 的记录。
	ErrNotFound = errors.New("ipgeo: ip not found in database")
	// ErrInvalidIP 表示入参不是合法 IP。
	ErrInvalidIP = errors.New("ipgeo: invalid ip address")
)

// CachePolicy 保留以兼容旧调用签名。
// 本分支统一走 mmap 全内存查询，三种策略行为等价：
// 都不再打开常驻文件句柄，也不再需要 searcher 池。
type CachePolicy int

const (
	CacheNone CachePolicy = iota
	CacheVectorIndex
	CacheFull
)

type config struct {
	layout       Layout
	cacheEntries int
	preload      bool
}

// Option 是 Open 的可选项。
type Option func(*config)

func WithCachePolicy(CachePolicy) Option   { return func(*config) {} }
func WithV4CachePolicy(CachePolicy) Option { return func(*config) {} }
func WithV6CachePolicy(CachePolicy) Option { return func(*config) {} }
func WithSearchers(int) Option             { return func(*config) {} }

// WithPreload 把 xdb 整个读进堆内存，而不是 mmap。
//
// 适合库文件固定、实例内存可控的部署：省掉运行期缺页抖动，代价是 RSS ≈ 文件大小
// （v4 / v6 分别计算，多个实例则各自一份）。默认 false，即 mmap + MADV_RANDOM。
func WithPreload(preload bool) Option { return func(c *config) { c.preload = preload } }

// WithLayout 显式指定 region 字段布局，跳过自动推断。
func WithLayout(l Layout) Option { return func(c *config) { c.layout = l } }

// WithLRU 开启结果缓存（总条数，内部按 256 分片均摊）。
func WithLRU(entries int) Option {
	return func(c *config) {
		if entries > 0 {
			c.cacheEntries = entries
		}
	}
}

// DB 是并发安全的只读查询句柄。
type DB struct {
	v4, v6 searcher
	layout Layout
	cache  *Cache
}

// Open 打开 v4 / v6 的 xdb；任一参数传空串表示禁用该版本。
func Open(v4Path, v6Path string, opts ...Option) (*DB, error) {
	if v4Path == "" && v6Path == "" {
		return nil, errors.New("ipgeo: at least one of v4Path / v6Path is required")
	}

	cfg := &config{}
	for _, opt := range opts {
		opt(cfg)
	}

	db := &DB{layout: cfg.layout}

	if v4Path != "" {
		s, err := newSearcher(v4Path, true, cfg.preload)
		if err != nil {
			return nil, fmt.Errorf("ipgeo: open ipv4 xdb %q: %w", v4Path, err)
		}
		db.v4 = s
	}
	if v6Path != "" {
		s, err := newSearcher(v6Path, false, cfg.preload)
		if err != nil {
			db.Close()
			return nil, fmt.Errorf("ipgeo: open ipv6 xdb %q: %w", v6Path, err)
		}
		db.v6 = s
	}
	if cfg.cacheEntries > 0 {
		db.cache = NewCache(cfg.cacheEntries)
	}
	return db, nil
}

// MustOpen 是 Open 的 panic 版本。
func MustOpen(v4Path, v6Path string, opts ...Option) *DB {
	db, err := Open(v4Path, v6Path, opts...)
	if err != nil {
		panic(err)
	}
	return db
}

// Close 释放 mmap 映射；调用后不得再查询。
func (db *DB) Close() {
	if db.v4 != nil {
		db.v4.close()
		db.v4 = nil
	}
	if db.v6 != nil {
		db.v6.close()
		db.v6 = nil
	}
}

// Lookup 传入字符串 IP，返回结构化结果。
func (db *DB) Lookup(ip string) (*Info, error) {
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidIP, ip)
	}
	return db.LookupAddr(a)
}

// LookupIP 传入 net.IP，返回结构化结果。
func (db *DB) LookupIP(ip net.IP) (*Info, error) {
	a, ok := netip.AddrFromSlice(ip)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrInvalidIP, ip.String())
	}
	return db.LookupAddr(a)
}

// LookupAddr 是零分配入口，推荐在热路径使用。
func (db *DB) LookupAddr(a netip.Addr) (*Info, error) {
	a = a.Unmap() // ::ffff:1.2.3.4 -> 1.2.3.4
	if !a.IsValid() {
		return nil, ErrInvalidIP
	}

	if db.cache != nil {
		if info, ok := db.cache.Get(a); ok {
			return info, nil
		}
	}

	// 私有 / 保留地址直接短路
	if isReservedAddr(a) {
		info := &Info{Addr: a, IsPrivate: true, Version: versionOf(a)}
		info.IP = info.Addr.String()
		return info, nil
	}

	var (
		raw []byte
		ok  bool
	)
	if a.Is4() {
		if db.v4 == nil {
			return nil, ErrNotFound
		}
		b := a.As4()
		raw, ok = db.v4.search4(be32(b[:]))
	} else {
		if db.v6 == nil {
			return nil, ErrNotFound
		}
		raw, ok = db.v6.search16(a.As16())
	}
	if !ok || len(raw) == 0 {
		return nil, ErrNotFound
	}

	info := newInfo(a, raw, db.layout)
	if db.cache != nil {
		db.cache.Put(a, info)
	}
	return info, nil
}

func versionOf(a netip.Addr) Version {
	if a.Is4() {
		return IPv4
	}
	return IPv6
}

func isReservedAddr(a netip.Addr) bool {
	return a.IsPrivate() ||
		a.IsLoopback() ||
		a.IsUnspecified() ||
		a.IsMulticast() ||
		a.IsLinkLocalUnicast() ||
		a.IsLinkLocalMulticast() ||
		a.IsInterfaceLocalMulticast()
}

var _ = context.Background
