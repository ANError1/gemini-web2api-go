package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func mcpCall(t *testing.T, method string, params any) map[string]interface{} {
	t.Helper()
	raw, _ := json.Marshal(params)
	resp := dispatchMCP(&rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: method, Params: raw})
	if resp == nil || resp.Error != nil {
		t.Fatalf("%s: %+v", method, resp)
	}
	out, _ := json.Marshal(resp.Result)
	var m map[string]interface{}
	_ = json.Unmarshal(out, &m)
	return m
}

func TestMCPListsMediaTools(t *testing.T) {
	m := mcpCall(t, "tools/list", map[string]any{})
	var names []string
	for _, it := range m["tools"].([]interface{}) {
		names = append(names, it.(map[string]interface{})["name"].(string))
	}
	got := strings.Join(names, ",")
	if got != "web_search,generate_image,generate_music" {
		t.Fatalf("tools = %s", got)
	}
}

func TestMCPMediaToolNeedsPrompt(t *testing.T) {
	m := mcpCall(t, "tools/call", map[string]any{"name": "generate_image", "arguments": map[string]any{"prompt": "  "}})
	if m["isError"] != true {
		t.Fatalf("empty prompt should be a tool error: %v", m)
	}
}

// 产物按 mime 映射成 MCP 原生内容类型；没有对应类型的走 embedded resource。
func TestMCPMediaContent(t *testing.T) {
	cases := map[string]string{"image/png": "image", "audio/mpeg": "audio", "video/mp4": "resource"}
	for mime, want := range cases {
		c := mcpMediaContent(MediaArtifact{Mime: mime, Data: []byte("xyz")})
		if c["type"] != want {
			t.Errorf("%s -> %v, want %s", mime, c["type"], want)
		}
		switch want {
		case "resource":
			r := c["resource"].(map[string]interface{})
			if r["blob"] != "eHl6" || r["mimeType"] != mime {
				t.Errorf("resource = %v", r)
			}
		default:
			if c["data"] != "eHl6" || c["mimeType"] != mime {
				t.Errorf("%s content = %v", mime, c)
			}
		}
	}
}
