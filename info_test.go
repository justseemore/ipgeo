package ipgeo

import "testing"

func TestParseRegionAggregated(t *testing.T) {
	cases := []struct {
		region      string
		continent   string
		country     string
		province    string
		city        string
		district    string
		isp         string
		networkType string
	}{
		{"亚洲|中国|浙江省|杭州市||阿里云|", "亚洲", "中国", "浙江省", "杭州市", "", "阿里云", ""},
		{"亚洲|中国|广东省|深圳市|南山区|电信|宽带", "亚洲", "中国", "广东省", "深圳市", "南山区", "电信", "宽带"},
		{"亚洲|澳大利亚|昆士兰|布里斯班||Cloudflare|AS13335", "亚洲", "澳大利亚", "昆士兰", "布里斯班", "", "Cloudflare", "AS13335"},
	}

	for _, c := range cases {
		got := parseRegion(c.region, autoLayout(c.region))
		if got.Continent != c.continent || got.Country != c.country ||
			got.Province != c.province || got.City != c.city ||
			got.District != c.district || got.ISP != c.isp ||
			got.NetworkType != c.networkType {
			t.Errorf("%s: got %+v", c.region, got)
		}
	}
}

func TestParseRegionOfficial(t *testing.T) {
	got := parseRegion("Australia|Queensland|Brisbane|0|AU", autoLayout("Australia|Queensland|Brisbane|0|AU"))
	if got.Country != "Australia" || got.Province != "Queensland" ||
		got.City != "Brisbane" || got.ISP != "" || got.ISOCode != "AU" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestAutoLayoutByFieldCount(t *testing.T) {
	for region, want := range map[string]int{
		"a|b|c|d|e":       5,
		"a|b|c|d|e|f|g":   7,
		"a|b|c|d|e|f|g|h": 8,
	} {
		if n := len(autoLayout(region)); n != want {
			t.Errorf("%s: layout len=%d, want %d", region, n, want)
		}
	}
}

func TestNormalizeField(t *testing.T) {
	for in, want := range map[string]string{
		"0": "", "-": "", "  电信 ": "电信", "中国": "中国",
	} {
		if got := normalizeField(in); got != want {
			t.Errorf("normalizeField(%q)=%q, want %q", in, got, want)
		}
	}
}
