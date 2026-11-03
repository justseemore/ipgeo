//go:build unix

package ipgeo

import (
	"errors"
	"fmt"
	"os"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
	"golang.org/x/sys/unix"
)

// newSearcher 以只读方式 mmap 整个 xdb，之后 fd 立即关闭。
func newSearcher(path string, wantV4 bool) (searcher, error) {
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
