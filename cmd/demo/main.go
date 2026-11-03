package main

import (
	"errors"
	"fmt"
	"log"
	"os"

	"github.com/justseemore/ipgeo"
)

func main() {
	v4 := envOr("IPGEO_V4_XDB", "data/ip2region_v4.xdb")
	v6 := envOr("IPGEO_V6_XDB", "data/ip2region_v6.xdb")

	db, err := ipgeo.Open(v4, v6,
		ipgeo.WithCachePolicy(ipgeo.CacheVectorIndex),
		ipgeo.WithSearchers(16),
		ipgeo.WithLRU(100000),
	)
	if err != nil {
		log.Fatalf("open: %v", err)
	}
	defer db.Close()

	ips := os.Args[1:]
	if len(ips) == 0 {
		ips = []string{"223.5.5.5", "1.2.3.4", "240e:57f:3000::", "2604:a840:3::a04d", "192.168.1.1"}
	}

	for _, ip := range ips {
		info, err := db.Lookup(ip)
		switch {
		case errors.Is(err, ipgeo.ErrNotFound):
			fmt.Printf("%-20s 未收录\n", ip)
			continue
		case err != nil:
			fmt.Printf("%-20s 错误: %v\n", ip, err)
			continue
		}
		fmt.Printf("%-20s v%d private=%-5v %-28s ISO=%-3s 类型=%s\n",
			ip, info.Version, info.IsPrivate, info.String(), info.ISOCode, info.NetworkType)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
