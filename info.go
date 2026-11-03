package ipgeo

import "strings"

// Version 表示命中的 IP 版本。
type Version uint8

const (
	IPv4 Version = 4
	IPv6 Version = 6
)

// Info 是一次查询的结构化结果。
// 字段与 fa1seut0pia/ip2region-xdb 的输出（洲|国家|省|市|区县|ISP|网络类型）对齐。
type Info struct {
	IP          string  `json:"ip"`
	Version     Version `json:"version"`
	Continent   string  `json:"continent,omitempty"`    // 洲：亚洲
	Country     string  `json:"country,omitempty"`      // 国家：中国
	Province    string  `json:"province,omitempty"`     // 省：浙江省
	City        string  `json:"city,omitempty"`         // 市：杭州市
	District    string  `json:"district,omitempty"`     // 区县：西湖区
	ISP         string  `json:"isp,omitempty"`          // 运营商：电信
	NetworkType string  `json:"network_type,omitempty"` // 宽带/基站/专线/IDC
	ISOCode     string  `json:"iso_code,omitempty"`     // ISO 3166-1 alpha-2：CN
	IsPrivate   bool    `json:"is_private,omitempty"`   // 私有/保留地址
	Raw         string  `json:"raw,omitempty"`          // 原始 region 串
}

// String 返回非空字段的紧凑表示，便于打日志。
func (i *Info) String() string {
	parts := make([]string, 0, 5)
	for _, s := range []string{i.Country, i.Province, i.City, i.District, i.ISP} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "|")
}

// Fields 按“国家|省|城市|ISP|ISO”返回 5 个字段。
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

// Layout 描述 region 按 "|" 拆分后，每个位置对应的逻辑字段。
type Layout []Field

var (
	// LayoutAggregated：fa1seut0pia/ip2region-xdb 默认产物（7 段）
	// 洲|国家|省份|城市|区县|ISP|网络类型
	LayoutAggregated = Layout{FieldContinent, FieldCountry, FieldProvince, FieldCity, FieldDistrict, FieldISP, FieldNetworkType}

	// LayoutAggregatedISO：在聚合产物末尾追加 ISO 国家码（8 段）
	LayoutAggregatedISO = Layout{FieldContinent, FieldCountry, FieldProvince, FieldCity, FieldDistrict, FieldISP, FieldNetworkType, FieldISOCode}

	// LayoutOfficial：ip2region 官方免费数据（5 段）国家|省|市|ISP|ISO
	LayoutOfficial = Layout{FieldCountry, FieldProvince, FieldCity, FieldISP, FieldISOCode}
)

// autoLayout 按段数猜布局；不确定时按聚合格式对齐。
func autoLayout(region string) Layout {
	switch n := strings.Count(region, "|") + 1; {
	case n == 5:
		return LayoutOfficial
	case n == 7:
		return LayoutAggregated
	case n == 8:
		return LayoutAggregatedISO
	case n > 8:
		return LayoutAggregatedISO // 多出的字段忽略
	default: // 6 段等非常规情况：按聚合格式截断
		return LayoutAggregated[:n]
	}
}

func parseRegion(region string, layout Layout) *Info {
	parts := strings.Split(region, "|")
	info := &Info{Raw: region}
	for i, f := range layout {
		if i >= len(parts) {
			break
		}
		v := normalizeField(parts[i])
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
	return info
}

// normalizeField 清理占位值：官方库里空 ISP/国家码常写成 "0"。
func normalizeField(s string) string {
	s = strings.TrimSpace(s)
	if s == "0" || s == "-" {
		return ""
	}
	return s
}
