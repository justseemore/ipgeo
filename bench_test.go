package ipgeo

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"testing"
)

func openTestDB(tb testing.TB, opts ...Option) *DB {
	tb.Helper()
	v4 := os.Getenv("IPGEO_V4_XDB")
	v6 := os.Getenv("IPGEO_V6_XDB")
	if v4 == "" && v6 == "" {
		tb.Skip("set IPGEO_V4_XDB / IPGEO_V6_XDB to run")
	}
	db, err := Open(v4, v6, opts...)
	if err != nil {
		tb.Fatalf("open: %v", err)
	}
	tb.Cleanup(db.Close)
	return db
}

func TestLookupBasics(t *testing.T) {
	db := openTestDB(t, WithLRU(1<<16))

	info, err := db.Lookup("223.5.5.5")
	if err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup: %v", err)
	}
	if info != nil {
		t.Logf("%s v%d -> %s (iso=%s)", info.IP, info.Version, info.String(), info.ISOCode)
	}

	if _, err := db.Lookup("192.168.1.1"); err != nil {
		t.Fatalf("private ip: %v", err)
	}
	if _, err := db.Lookup("nope"); !errors.Is(err, ErrInvalidIP) {
		t.Fatalf("want ErrInvalidIP, got %v", err)
	}
	if _, err := db.Lookup("240e:57f:3000::1"); err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatalf("ipv6: %v", err)
	}
}

func TestProviderReload(t *testing.T) {
	v4 := os.Getenv("IPGEO_V4_XDB")
	if v4 == "" {
		t.Skip("set IPGEO_V4_XDB to run")
	}
	p, err := New(v4, os.Getenv("IPGEO_V6_XDB"), nil, WithLRU(1<<16))
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	defer p.Close()

	if err := p.Reload(t.Context()); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if _, err := p.Lookup("223.5.5.5"); err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatalf("lookup after reload: %v", err)
	}
}

var benchAddrs = func() []netip.Addr {
	out := make([]netip.Addr, 4096)
	for i := range out {
		out[i] = netip.AddrFrom4([4]byte{byte(1 + i>>16), byte(i >> 8), byte(i), 1})
	}
	return out
}()

func BenchmarkLookup_Hit(b *testing.B) {
	db := openTestDB(b, WithLRU(1<<16))
	for _, a := range benchAddrs {
		db.LookupAddr(a) // 预热
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			db.LookupAddr(benchAddrs[i&4095])
			i++
		}
	})
}

func BenchmarkLookup_Cold(b *testing.B) {
	db := openTestDB(b) // 不带缓存，测二分 + mmap 路径
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			db.LookupAddr(benchAddrs[i&4095])
			i++
		}
	})
}

func BenchmarkLookup_String(b *testing.B) {
	db := openTestDB(b, WithLRU(1<<16))
	ss := make([]string, len(benchAddrs))
	for i, a := range benchAddrs {
		ss[i] = a.String()
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			db.Lookup(ss[i&4095])
			i++
		}
	})
}

var _ = fmt.Sprint
