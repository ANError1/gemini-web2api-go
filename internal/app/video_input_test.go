package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestHistoryTurnScopesToRid(t *testing.T) {
	turnA := []any{[]any{"c_1", "r_aaa"}, nil, []any{[]any{"first"}}, []any{[]any{"rc_1111111111", []any{"https://contribution.usercontent.google.com/download?c=A"}}}}
	turnB := []any{[]any{"c_1", "r_bbb"}, nil, []any{[]any{"continue"}}, []any{[]any{"rc_2222222222", []any{"I can create more videos as soon as your limit resets"}}}}
	inner, _ := json.Marshal([]any{[]any{turnB, turnA}})
	env, _ := json.Marshal([]any{[]any{"wrb.fr", "hNvQHb", string(inner)}})
	body := ")]}'\n\n123\n" + string(env) + "\n"
	b, ok := historyTurn(body, "r_aaa")
	if !ok || !strings.Contains(b, "rc_1111111111") || strings.Contains(b, "limit resets") {
		t.Fatalf("turn A not isolated: ok=%v %s", ok, b)
	}
	b, ok = historyTurn(body, "r_bbb")
	if !ok || !strings.Contains(b, "limit resets") || strings.Contains(b, "rc_1111111111") {
		t.Fatalf("turn B not isolated: ok=%v %s", ok, b)
	}
	if _, ok := historyTurn(body, "r_zzz"); ok {
		t.Fatal("unknown rid must not match")
	}
}

func TestTurnReplyTextDetectsRefusal(t *testing.T) {
	// Real hNvQHb body (tokens redacted) where Gemini refused with text, no video tool.
	raw, err := os.ReadFile("testdata/video_refusal_hist.txt")
	if err != nil {
		t.Fatal(err)
	}
	body, ok := historyTurn(string(raw), "r_91174b02aa556b62")
	if !ok {
		t.Fatal("turn not found")
	}
	got, ok := turnReplyText(body)
	if !ok || !strings.Contains(got, "hard time fulfilling") {
		t.Fatalf("refusal text not found: %q %v", got, ok)
	}
	if strings.Contains(body, "video_generation") {
		t.Fatal("fixture should not contain the video tool marker")
	}
	if _, ok := turnReplyText(`[["wrb.fr","hNvQHb","[[[[\"c_1\",\"r_a\"],null,[[\"p\"]]]]]"]]`); ok {
		t.Fatal("turn without reply must report ok=false")
	}
}
