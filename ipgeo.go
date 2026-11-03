package ipgeo

import (
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/lionsoul2014/ip2region/binding/golang/service"
)

// ErrNotFound 表示库中没有该 IP 的记录。
var ErrNotFound = errors.New("ipgeo: ip not found in database")

// CachePolicy 是 xdb 的缓存策略。
type CachePolicy int

const (
	// CacheNone 完全基于文件查询，最省内存，每次查询有一次磁盘 IO。
	CacheNone CachePolicy = CachePolicy(service.NoCache)
	// CacheVectorIndex 只缓存 512KiB 向量索引，默认推荐。
	CacheVectorIndex CachePolicy = CachePolicy(service.VIndexCache)
	// CacheFull 整个 xdb 读入内存，最快，内存占用≈文件大小。
	CacheFull CachePolicy = CachePolicy(service.BufferCache)
)

type config struct {
	v4Policy     CachePolicy
	v6Policy     CachePolicy
	searchers    int
	layout       Layout
	cacheEntries int
}

// Option 是 Open 的可选项。
type Option func(*config)

// WithCachePolicy 同时设置 v4/v6 缓存策略。
func WithCachePolicy(p CachePolicy) Option {
	return func(c *config) { c.v4Policy, c.v6Policy = p, p }
}

// WithV4CachePolicy 只设置 v4 缓存策略。
func WithV4CachePolicy(p CachePolicy) Option {
	return func(c *config) { c.v4Policy = p }
}

// WithV6CachePolicy 只设置 v6 缓存策略。
func WithV6CachePolicy(p CachePolicy) Option {
	return func(c *config) { c.v6Policy = p }
}

// WithSearchers 设置查询器池大小（CacheFull 下无效）。
func WithSearchers(n int) Option {
	return func(c *config) {
		if n > 0 {
			c.searchers = n
		}
	}
}

// WithLayout 显式指定 region 字段布局，跳过自动推断。
func WithLayout(l Layout) Option {
	return func(c *config) { c.layout = l }
}

// WithLRU 开启结果缓存（条数）。
func WithLRU(entries int) Option {
	return func(c *config) { c.cacheEntries = entries }
}

// DB 是并发安全的查询句柄。
type DB struct {
	svc    *service.Ip2Region
	layout Layout
	cache  *lruCache
}

// Open 打开 v4 / v6 的 xdb；任一参数传空串表示禁用该版本。
func Open(v4Path, v6Path string, opts ...Option) (*DB, error) {
	if v4Path == "" && v6Path == "" {
		return nil, errors.New("ipgeo: at least one of v4Path / v6Path is required")
	}

	cfg := &config{v4Policy: CacheVectorIndex, v6Policy: CacheVectorIndex, searchers: 8}
	for _, opt := range opts {
		opt(cfg)
	}

	var (
		v4, v6 *service.Config
		err    error
	)

	if v4Path != "" {
		if v4, err = service.NewV4Config(int(cfg.v4Policy), v4Path, cfg.searchers); err != nil {
			return nil, fmt.Errorf("ipgeo: open ipv4 xdb %q: %w", v4Path, err)
		}
	}
	if v6Path != "" {
		if v6, err = service.NewV6Config(int(cfg.v6Policy), v6Path, cfg.searchers); err != nil {
			return nil, fmt.Errorf("ipgeo: open ipv6 xdb %q: %w", v6Path, err)
		}
	}

	svc, err := service.NewIp2Region(v4, v6)
	if err != nil {
		return nil, fmt.Errorf("ipgeo: create service: %w", err)
	}

	db := &DB{svc: svc, layout: cfg.layout}
	if cfg.cacheEntries > 0 {
		db.cache = newLRU(cfg.cacheEntries)
	}
	return db, nil
}

// MustOpen 是 Open 的 panic 版本，方便初始化。
func MustOpen(v4Path, v6Path string, opts ...Option) *DB {
	db, err := Open(v4Path, v6Path, opts...)
	if err != nil {
		panic(err)
	}
	return db
}

// Close 释放查询器池，等待归还最多 10 秒。
func (db *DB) Close() { db.svc.Close() }

// Lookup 传入字符串 IP，返回结构化结果。
func (db *DB) Lookup(ip string) (*Info, error) {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return nil, fmt.Errorf("ipgeo: invalid ip address %q", ip)
	}
	return db.LookupIP(parsed)
}

// LookupIP 传入 net.IP，返回结构化结果。
func (db *DB) LookupIP(ip net.IP) (*Info, error) {
	if len(ip) == 0 {
		return nil, errors.New("ipgeo: empty ip")
	}

	// 统一 ::ffff:1.2.3.4 这类 IPv4-mapped IPv6
	var (
		ver Version
		key string
	)
	if v4 := ip.To4(); v4 != nil {
		ver, key = IPv4, v4.String()
	} else if v6 := ip.To16(); v6 != nil {
		ver, key = IPv6, v6.String()
	} else {
		return nil, fmt.Errorf("ipgeo: invalid ip address %q", ip.String())
	}

	if db.cache != nil {
		if info, ok := db.cache.get(key); ok {
			return info, nil
		}
	}

	// 私有/保留地址直接短路，避免无意义的库查询
	if isReserved(ip) {
		info := &Info{IP: key, Version: ver, IsPrivate: true}
		db.store(key, info)
		return info, nil
	}

	region, err := db.svc.Search(key)
	if err != nil {
		return nil, fmt.Errorf("ipgeo: search %s: %w", key, err)
	}
	if region == "" {
		return nil, ErrNotFound
	}

	layout := db.layout
	if layout == nil {
		layout = autoLayout(region)
	}

	info := parseRegion(region, layout)
	info.IP = key
	info.Version = ver
	db.store(key, info)
	return info, nil
}

func (db *DB) store(key string, info *Info) {
	if db.cache != nil {
		db.cache.put(key, info)
	}
}

func isReserved(ip net.IP) bool {
	return ip.IsPrivate() ||
		ip.IsLoopback() ||
		ip.IsUnspecified() ||
		ip.IsMulticast() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast()
}
