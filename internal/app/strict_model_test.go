package app

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func setStrictModel(t *testing.T, on bool) {
	old := rtCfg()
	t.Cleanup(func() { rtMu.Lock(); rtVal = old; rtMu.Unlock() })
	rtMu.Lock()
	rtVal.StrictModel = on
	rtMu.Unlock()
}

// 结构取自 2026-10-24 实测：显示名和第一段正文在同一行（无 Argon 权限的号请求 Argon）。
const downgradedLine = `[["wrb.fr",null,"[null,[\"c_1\",\"r_1\"],null,null,[[\"rc_1\",[\"1+1等于2。\"]]],null,\"cf41b0e0dd7d53e5\",null,null,\"3.5 Flash-Lite\",true]"]]`

func TestStrictLineCheck(t *testing.T) {
	argon := Models["gemini-4-argon"]

	setStrictModel(t, false)
	if strictLineCheck(argon, func() int64 { return 7 }) != nil {
		t.Fatal("开关关着应返回 nil，不能给读取路径加任何开销")
	}

	setStrictModel(t, true)
	if strictLineCheck(Models["gemini-image"], func() int64 { return 7 }) != nil {
		t.Fatal("媒体模型没登记 Upstream，不该检查")
	}
	check := strictLineCheck(argon, func() int64 { return 7 })
	err := check(downgradedLine)
	var mde *ModelDowngradedError
	if !errors.As(err, &mde) || mde.Reported != "3.5 Flash-Lite" || mde.AccountID != 7 {
		t.Fatalf("降级行应报 ModelDowngradedError(3.5 Flash-Lite, #7)，得到 %v", err)
	}
	if err := strictLineCheck(Models["gemini-3.5-flash-lite"], func() int64 { return 7 })(downgradedLine); err != nil {
		t.Fatalf("请求的就是 Flash-Lite，不该报降级：%v", err)
	}
}

// 回调报错必须立刻停读：后面的行不能再回调，否则正文会在报错之前漏给客户端。
func TestReadBodyStopsOnCallbackError(t *testing.T) {
	stop := errors.New("stop")
	var seen []string
	_, _, err := readBody(strings.NewReader("a\nb\nc\n"), func(line string) error {
		seen = append(seen, line)
		if line == "b" {
			return stop
		}
		return nil
	}, time.Now())
	if err != stop {
		t.Fatalf("应原样返回回调的错误，得到 %v", err)
	}
	if strings.Join(seen, ",") != "a,b" {
		t.Fatalf("报错后还在回调：%v", seen)
	}
}

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
