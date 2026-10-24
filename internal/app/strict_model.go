package app

import (
	"regexp"
	"strings"
)

// modelNameFoldRe 把字母数字和点以外的东西都折成空格：连字符、全角后缀（"扩展"）
// 一并抹掉，"3.5 Flash-Lite" 和 "3.5 flash lite" 才比得上。
var modelNameFoldRe = regexp.MustCompile(`[^a-z0-9.]+`)

func foldModelName(s string) string {
	return strings.TrimSpace(modelNameFoldRe.ReplaceAllString(strings.ToLower(s), " "))
}

// modelDowngraded 判断服务端自报的显示名是不是请求的那个模型。
//
// 按「整词前缀」比：思考档会在名字后面挂后缀（"3.1 Pro High"、"3.6 Flash 扩展"），
// 后缀改过名（扩展 → High），所以只认前缀；但必须断在词边界，否则 "3.5 Flash" 会
// 把 "3.5 Flash-Lite" 当成自己。
//
// 看不见就不判：没自报（reported 为空）或模型没登记 Upstream 一律当没降级——
// 误报会让开了开关的人白白丢请求，比漏判更伤。
func modelDowngraded(mc ModelConfig, reported string) bool {
	want := foldModelName(mc.Upstream)
	got := foldModelName(reported)
	if want == "" || got == "" {
		return false
	}
	return got != want && !strings.HasPrefix(got, want+" ")
}
