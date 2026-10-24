package app

import "testing"

func TestModelDowngraded(t *testing.T) {
	cases := []struct {
		model, reported string
		want            bool
	}{
		{"gemini-3.1-pro", "3.1 Pro", false},
		{"gemini-3.1-pro-thinking", "3.1 Pro High", false},
		{"gemini-3.6-flash-thinking", "3.6 Flash 扩展", false},
		{"gemini-4-argon-thinking", "4 Argon High", false},
		{"gemini-3.7-flash", "3.8 Flash", false},
		// 2026-10-24 实测：无 Argon 权限的号请求 Argon，自报 3.5 Flash-Lite
		{"gemini-4-argon", "3.5 Flash-Lite", true},
		// 同日实测：免费号请求 3.8，自报 3.6 Flash
		{"gemini-3.8-flash", "3.6 Flash", true},
		{"gemini-3.1-pro", "3.5 Flash-Lite", true},
		// 前缀必须断在词边界：Flash 不能把 Flash-Lite 认成自己
		{"gemini-3.5-flash-lite", "3.5 Flash", true},
		// 看不见不判
		{"gemini-3.1-pro", "", false},
		{"gemini-image", "Nano Banana 2", false},
	}
	for _, c := range cases {
		mc, ok := Models[c.model]
		if !ok {
			t.Fatalf("Models 里没有 %s", c.model)
		}
		if got := modelDowngraded(mc, c.reported); got != c.want {
			t.Errorf("%s 自报 %q: downgraded=%v want %v", c.model, c.reported, got, c.want)
		}
	}
}

// 除了媒体工具，每个模型都得登记 Upstream，不然 strict_model 对它静默失效。
func TestEveryChatModelHasUpstream(t *testing.T) {
	for name, mc := range Models {
		if mc.Tool == 0 && mc.Upstream == "" {
			t.Errorf("%s 没有 Upstream", name)
		}
	}
}
