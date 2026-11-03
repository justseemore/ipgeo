//go:build !unix

package ipgeo

import (
	"fmt"
	"os"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

// 非 unix 平台退化为整文件读入内存，语义与 mmap 一致：
// 无常驻文件句柄、全内存查询、可被并发共享。
func newSearcher(path string, wantV4 bool) (searcher, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if err := xdb.Verify(f); err != nil {
		f.Close()
		return nil, fmt.Errorf("verify xdb: %w", err)
	}
	hdr, err := xdb.LoadHeader(f)
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("load header: %w", err)
	}
	ver, err := xdb.VersionFromHeader(hdr)
	f.Close()
	if err != nil {
		return nil, fmt.Errorf("detect version: %w", err)
	}
	if (ver.Id == xdb.IPv4.Id) != wantV4 {
		return nil, fmt.Errorf("xdb %q ip version mismatch: got %s", path, ver.Name)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return &memSearcher{data: data}, nil
}
