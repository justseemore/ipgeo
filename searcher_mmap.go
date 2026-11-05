//go:build unix

package ipgeo

import (
	"errors"
	"fmt"
	"os"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
	"golang.org/x/sys/unix"
)

// newSearcher 打开 xdb：默认 mmap（关掉顺序预读），preload 时整文件读进堆。
func newSearcher(path string, wantV4, preload bool) (searcher, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	if err := xdb.Verify(f); err != nil {
		return nil, fmt.Errorf("verify xdb: %w", err)
	}
	hdr, err := xdb.LoadHeader(f)
	if err != nil {
		return nil, fmt.Errorf("load header: %w", err)
	}
	ver, err := xdb.VersionFromHeader(hdr)
	if err != nil {
		return nil, fmt.Errorf("detect version: %w", err)
	}
	if (ver.Id == xdb.IPv4.Id) != wantV4 {
		return nil, fmt.Errorf("xdb %q ip version mismatch: got %s", path, ver.Name)
	}

	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() < headerInfoLength {
		return nil, errors.New("xdb file too small")
	}

	// 预加载：读进堆，不走 mmap。memSearcher.close() 的 unmap 为 nil，
	// 只清掉切片引用，内存交给 GC。
	if preload {
		data, err := os.ReadFile(path) // 独立 fd，不影响上面 f 的偏移
		if err != nil {
			return nil, fmt.Errorf("preload xdb: %w", err)
		}
		return &memSearcher{data: data}, nil
	}

	data, err := unix.Mmap(int(f.Fd()), 0, int(fi.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, fmt.Errorf("mmap: %w", err)
	}
	// 点查为主：关闭顺序预读，避免把冷页挤进 page cache
	_ = unix.Madvise(data, unix.MADV_RANDOM)

	s := &memSearcher{data: data}
	s.unmap = func() { _ = unix.Munmap(data) }
	return s, nil
}
