package ipgeo

import (
	"net/netip"
	"strings"
)

// Version 表示命中的 IP 版本。
type Version uint8

const (
	IPv4 Version = 4
	IPv6 Version = 6
)

// Info 是一次查询的结构化结果。
type Info struct {
	IP          string     `json:"ip"`
	Addr        netip.Addr `json:"-"` // 零分配的强类型入口
	Version     Version    `json:"version"`
	Continent   string     `json:"continent,omitempty"`
	Country     string     `json:"country,omitempty"`
	Province    string     `json:"province,omitempty"`
	City        string     `json:"city,omitempty"`
	District    string     `json:"district,omitempty"`
	ISP         string     `json:"isp,omitempty"`
	NetworkType string     `json:"network_type,omitempty"`
	ISOCode     string     `json:"iso_code,omitempty"`
	IsPrivate   bool       `json:"is_private,omitempty"`
	Raw         string     `json:"raw,omitempty"`
}

func (i *Info) String() string {
	parts := make([]string, 0, 5)
	for _, s := range []string{i.Country, i.Province, i.City, i.District, i.ISP} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "|")
}

func (i *Info) Fields() [5]string {
	return [5]string{i.Country, i.Province, i.City, i.ISP, i.ISOCode}
}

// Field 是 region 串中一个逻辑字段。
type Field uint8

const (
	FieldContinent Field = iota
	FieldCountry
	FieldProvince
	FieldCity
	FieldDistrict
	FieldISP
	FieldNetworkType
	FieldISOCode
)

// Layout 描述 region 按 "|" 拆分后每个位置对应的逻辑字段。
type Layout []Field

var (
	LayoutAggregated    = Layout{FieldContinent, FieldCountry, FieldProvince, FieldCity, FieldDistrict, FieldISP, FieldNetworkType}
	LayoutAggregatedISO = Layout{FieldContinent, FieldCountry, FieldProvince, FieldCity, FieldDistrict, FieldISP, FieldNetworkType, FieldISOCode}
	LayoutOfficial      = Layout{FieldCountry, FieldProvince, FieldCity, FieldISP, FieldISOCode}
)

func autoLayout(region string) Layout {
	switch n := strings.Count(region, "|") + 1; {
	case n == 5:
		return LayoutOfficial
	case n == 7:
		return LayoutAggregated
	case n == 8:
		return LayoutAggregatedISO
	case n > 8:
		return LayoutAggregatedISO
	default:
		return LayoutAggregated[:n]
	}
}

// newInfo 单次扫描解析 region：只分配 1 个字符串，字段全部复用其子串。
func newInfo(a netip.Addr, raw []byte, layout Layout) *Info {
	s := string(raw)

	info := &Info{
		Addr:    a,
		Version: versionOf(a),
		Raw:     s,
	}
	var buf [48]byte
	info.IP = string(a.AppendTo(buf[:0]))

	if layout == nil {
		layout = autoLayout(s)
	}

	var start, n int
	for i := 0; i <= len(s) && n < len(layout); i++ {
		if i == len(s) || s[i] == '|' {
			setField(info, layout[n], normalizeField(s[start:i]))
			n++
			start = i + 1
		}
	}
	return info
}

func setField(info *Info, f Field, v string) {
	switch f {
	case FieldContinent:
		info.Continent = v
	case FieldCountry:
		info.Country = v
	case FieldProvince:
		info.Province = v
	case FieldCity:
		info.City = v
	case FieldDistrict:
		info.District = v
	case FieldISP:
		info.ISP = v
	case FieldNetworkType:
		info.NetworkType = v
	case FieldISOCode:
		info.ISOCode = strings.ToUpper(v)
	}
}

// normalizeField 清理占位值：官方库里空 ISP / 国家码常写成 "0"。
func normalizeField(s string) string {
	s = strings.TrimSpace(s)
	if s == "0" || s == "-" {
		return ""
	}
	return s
}
