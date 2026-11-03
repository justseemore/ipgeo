package ipgeo

import "encoding/binary"

// xdb v2 布局常量（与官方格式一致，不依赖上游实现细节）
const (
	headerInfoLength = 256
	vectorIndexCols  = 256
	vectorIndexSize  = 8

	segSize4 = 2*4 + 6  // 14: startIP|endIP|dataLen|dataPtr
	segSize6 = 2*16 + 6 // 38
)

// searcher 是并发安全的只读查询器。
// 返回值是后端缓冲区的一段切片，调用方必须立刻拷贝或解析，不得长期持有。
type searcher interface {
	search4(addr uint32) ([]byte, bool)
	search16(addr [16]byte) ([]byte, bool)
	close()
}

// memSearcher 在纯内存字节切片上做二分查找：无状态、可被无限并发共享。
type memSearcher struct {
	data  []byte
	unmap func()
}

func (s *memSearcher) close() {
	if s.unmap != nil {
		s.unmap()
		s.unmap = nil
	}
	s.data = nil
}

func (s *memSearcher) search4(v uint32) ([]byte, bool) {
	idx := int(v>>24)*vectorIndexCols*vectorIndexSize + int((v>>16)&0xff)*vectorIndexSize
	base := headerInfoLength + idx
	if base+vectorIndexSize > len(s.data) {
		return nil, false
	}
	sPtr := binary.LittleEndian.Uint32(s.data[base:])
	ePtr := binary.LittleEndian.Uint32(s.data[base+4:])
	if sPtr == 0 || ePtr <= sPtr {
		return nil, false
	}

	lo, hi := 0, int((ePtr-sPtr)/segSize4)-1
	for lo <= hi {
		m := int(uint(lo+hi) >> 1)
		off := int(sPtr) + m*segSize4
		if off+segSize4 > len(s.data) {
			return nil, false
		}
		rec := s.data[off : off+segSize4]
		switch start, end := be32(rec), be32(rec[4:]); {
		case v < start:
			hi = m - 1
		case v > end:
			lo = m + 1
		default:
			return s.region(rec, segSize4-6)
		}
	}
	return nil, false
}

func (s *memSearcher) search16(a [16]byte) ([]byte, bool) {
	idx := int(a[0])*vectorIndexCols*vectorIndexSize + int(a[1])*vectorIndexSize
	base := headerInfoLength + idx
	if base+vectorIndexSize > len(s.data) {
		return nil, false
	}
	sPtr := binary.LittleEndian.Uint32(s.data[base:])
	ePtr := binary.LittleEndian.Uint32(s.data[base+4:])
	if sPtr == 0 || ePtr <= sPtr {
		return nil, false
	}

	lo, hi := 0, int((ePtr-sPtr)/segSize6)-1
	for lo <= hi {
		m := int(uint(lo+hi) >> 1)
		off := int(sPtr) + m*segSize6
		if off+segSize6 > len(s.data) {
			return nil, false
		}
		rec := s.data[off : off+segSize6]
		c := cmpBytes(rec[:16], a[:])
		if c > 0 {
			hi = m - 1
		} else if c = cmpBytes(rec[16:32], a[:]); c < 0 {
			lo = m + 1
		} else {
			return s.region(rec, segSize6-6)
		}
	}
	return nil, false
}

// region 从段索引记录尾部取出 dataLen / dataPtr，返回数据区切片。
func (s *memSearcher) region(rec []byte, n int) ([]byte, bool) {
	size := int(binary.LittleEndian.Uint16(rec[n:]))
	off := int(binary.LittleEndian.Uint32(rec[n+2:]))
	if size == 0 || off < 0 || off+size > len(s.data) {
		return nil, false
	}
	return s.data[off : off+size], true
}

func be32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func cmpBytes(x, y []byte) int {
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			if x[i] < y[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}
