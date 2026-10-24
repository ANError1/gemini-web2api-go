package app

import (
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ModelDowngradedError 表示 strict_model 开着时上游把请求静默降级了。
// 它不是上游故障：同一个号重试只会再降级一次，所以重试循环见到它直接停。
type ModelDowngradedError struct {
	Requested string // 请求模型的显示名（ModelConfig.Upstream）
	Reported  string // 服务端自报实际用的
	AccountID int64  // 被降级的号，0 = 匿名
	// RetryAfter 是全池都在冷却时最早那个号解冻还要多久，0 = 不知道。
	RetryAfter time.Duration
}

func (e *ModelDowngradedError) Error() string {
	return fmt.Sprintf("requested %s but upstream answered with %s "+
		"(account quota for this model is likely exhausted; strict_model is on)", e.Requested, e.Reported)
}

// writeModelDowngraded 回 429 insufficient_quota：跟 OpenAI 额度用完同一个形状，
// agent 客户端本来就会把它当「换号 / 等一会」处理，而不是当成功。
func writeModelDowngraded(w http.ResponseWriter, e *ModelDowngradedError) {
	if e.RetryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	writeJSON(w, 429, map[string]interface{}{"error": map[string]string{
		"message": e.Error(),
		"type":    "insufficient_quota",
		"code":    "model_downgraded",
	}})
}

// strictLineCheck 返回逐行检查降级的函数；开关关着或模型没登记 Upstream 时返回 nil。
// 每行先过它再转发正文：显示名跟第一段正文在同一行或更早出现（2026-10-24 实测
// 逐行看过），所以先查再发就能在第一个字出去之前掐断。
func strictLineCheck(mc ModelConfig, accountID func() int64) func(line string) error {
	if !rtCfg().StrictModel || mc.Upstream == "" {
		return nil
	}
	return func(line string) error {
		if name := extractUpstreamModel(line); name != "" && modelDowngraded(mc, name) {
			return &ModelDowngradedError{Requested: mc.Upstream, Reported: name, AccountID: accountID()}
		}
		return nil
	}
}

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
