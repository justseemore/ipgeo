package ipgeo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

var ErrClosed = errors.New("ipgeo: provider closed")

// Provider 持有可热切换的 DB，是进程内唯一实例。
type Provider struct {
	v4Path, v6Path string
	opts           []Option
	log            *slog.Logger

	cur    atomic.Pointer[handle]
	reload sync.Mutex
	closed atomic.Bool
}

// handle 是一代 DB 的引用计数包装，保证旧实例不会在查询途中被 Close。
type handle struct {
	db     *DB
	mu     sync.Mutex
	refs   int
	dead   bool
	zeroed bool
	zero   chan struct{}
}

func newHandle(db *DB) *handle { return &handle{db: db, zero: make(chan struct{})} }

func (h *handle) acquire() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.dead {
		return false
	}
	h.refs++
	return true
}

func (h *handle) release() {
	h.mu.Lock()
	h.refs--
	if h.dead && h.refs == 0 && !h.zeroed {
		h.zeroed = true
		close(h.zero)
	}
	h.mu.Unlock()
}

func (h *handle) retire() {
	h.mu.Lock()
	h.dead = true
	if h.refs == 0 && !h.zeroed {
		h.zeroed = true
		close(h.zero)
	}
	h.mu.Unlock()

	<-h.zero // 等在途查询排空
	h.db.Close()
}

func New(v4, v6 string, log *slog.Logger, opts ...Option) (*Provider, error) {
	if log == nil {
		log = slog.Default()
	}
	db, err := Open(v4, v6, opts...)
	if err != nil {
		return nil, err
	}
	p := &Provider{v4Path: v4, v6Path: v6, opts: opts, log: log}
	p.cur.Store(newHandle(db))
	return p, nil
}

func MustNew(v4, v6 string, log *slog.Logger, opts ...Option) *Provider {
	p, err := New(v4, v6, log, opts...)
	if err != nil {
		panic(err)
	}
	return p
}

func (p *Provider) borrow() (*DB, func(), error) {
	for i := 0; i < 16; i++ {
		h := p.cur.Load()
		if h == nil {
			return nil, nil, ErrClosed
		}
		if h.acquire() {
			return h.db, h.release, nil
		}
	}
	return nil, nil, ErrClosed
}

func (p *Provider) Lookup(ip string) (*Info, error) {
	db, done, err := p.borrow()
	if err != nil {
		return nil, err
	}
	defer done()
	return db.Lookup(ip)
}

func (p *Provider) LookupAddr(a netip.Addr) (*Info, error) {
	db, done, err := p.borrow()
	if err != nil {
		return nil, err
	}
	defer done()
	return db.LookupAddr(a)
}

func (p *Provider) LookupIP(ip net.IP) (*Info, error) {
	db, done, err := p.borrow()
	if err != nil {
		return nil, err
	}
	defer done()
	return db.LookupIP(ip)
}

// Reload 重新打开 xdb 并原子替换；任何失败都保留旧库。
func (p *Provider) Reload(ctx context.Context) error {
	p.reload.Lock()
	defer p.reload.Unlock()

	if p.closed.Load() {
		return ErrClosed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	db, err := Open(p.v4Path, p.v6Path, p.opts...)
	if err != nil {
		return fmt.Errorf("ipgeo: reload open: %w", err)
	}
	if err := p.smoke(db); err != nil {
		db.Close()
		return fmt.Errorf("ipgeo: reload smoke test: %w", err)
	}

	old := p.cur.Swap(newHandle(db))
	p.log.Info("ipgeo reloaded", "v4", p.v4Path, "v6", p.v6Path)
	if old != nil {
		go old.retire() // 后台等排空后 Munmap
	}
	return nil
}

func (p *Provider) smoke(db *DB) error {
	if p.v4Path == "" {
		return nil
	}
	_, err := db.Lookup("223.5.5.5")
	if err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

func (p *Provider) Close() error {
	if p.closed.Swap(true) {
		return nil
	}
	p.reload.Lock()
	defer p.reload.Unlock()
	if h := p.cur.Swap(nil); h != nil {
		h.retire()
	}
	return nil
}

// Watch 定时比对 xdb 的 size/mtime，变化则 Reload；失败下个周期重试。
func (p *Provider) Watch(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	last, err := p.stamp()
	if err != nil {
		p.log.Warn("ipgeo watch: initial stat failed", "err", err)
	}

	t := time.NewTicker(interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cur, err := p.stamp()
			if err != nil {
				p.log.Warn("ipgeo watch: stat failed", "err", err)
				continue
			}
			if cur == last {
				continue
			}
			if err := p.Reload(ctx); err != nil {
				p.log.Error("ipgeo watch: reload failed, keeping old db", "err", err)
				continue
			}
			last = cur
		}
	}
}
