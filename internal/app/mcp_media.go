package app

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// MCP 的媒体工具：生图 / 音乐 / 视频，底层跟 /v1/chat/completions 里的 gemini-image /
// gemini-music、/v1/videos 是同一条链。
//
// 产物用 MCP 原生的 image / audio 内容类型返回（base64 放在 data 字段，带 mimeType），
// 不塞进 text：支持的客户端把它当附件渲染，不会把几十万字的 base64 当文本喂进上下文。
// 不做「短链」那种不带鉴权的公开 URL —— 那要额外的存储、过期、对外地址配置，
// 而原生内容类型已经解决了「文本里塞 base64 卡死客户端」这个问题。

func generateImageToolDef() map[string]interface{} {
	return map[string]interface{}{
		"name": "generate_image",
		"description": "Generate an image with Google Gemini (Nano Banana). Optionally pass " +
			"reference images to edit or restyle. The image is attached to the result; " +
			"don't repeat or re-describe it.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prompt": map[string]interface{}{
					"type":        "string",
					"description": "What to draw, or how to change the reference images.",
				},
				"image_urls": map[string]interface{}{
					"type":        "array",
					"items":       map[string]interface{}{"type": "string"},
					"description": "Optional reference images: data: URLs or http(s) links.",
				},
			},
			"required": []interface{}{"prompt"},
		},
	}
}

func generateMusicToolDef() map[string]interface{} {
	return map[string]interface{}{
		"name": "generate_music",
		"description": "Generate a ~30 second music clip with Google Gemini (Lyria). " +
			"The audio is attached to the result.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"prompt": map[string]interface{}{
					"type":        "string",
					"description": "Style, mood, instruments, tempo, and so on.",
				},
			},
			"required": []interface{}{"prompt"},
		},
	}
}

// mcpGenerateMedia 用媒体模型生成一次，返回 MCP 工具结果。
func mcpGenerateMedia(model, prompt string, refs []string) (map[string]interface{}, error) {
	_, mc, err := resolveModel(model)
	if err != nil {
		return nil, err
	}
	var images []pendingUpload
	for i, src := range refs {
		img, err := materializeImage(src, "", i+1)
		if err != nil {
			return nil, err
		}
		images = append(images, img)
	}
	_, _, res, err := callGemini(prompt, prompt, mc, nil, images, nil, nil)
	if err != nil {
		return nil, err
	}
	var content []interface{}
	for _, a := range res.Artifacts {
		content = append(content, mcpMediaContent(a))
	}
	if len(content) == 0 {
		return nil, fmt.Errorf("no media in the response")
	}
	// 给模型一句话交代：产物已经在结果里了，免得它在正文里再复述 / 再贴一遍。
	note := fmt.Sprintf("Generated %d file(s); they are attached to this result.", len(content))
	return map[string]interface{}{"content": append([]interface{}{
		map[string]interface{}{"type": "text", "text": note}}, content...)}, nil
}

// mcpMediaContent 按 mime 选 MCP 内容类型：image/* → image，audio/* → audio，
// 其余（如 video/mp4）没有对应类型，用 embedded resource 的 blob 带上。
// mime 来自下载时上游的 Content-Type，不一定跟工具对得上（音乐不保证是 audio/*），
// 所以按 mime 选，而不是按工具。
func mcpMediaContent(a MediaArtifact) map[string]interface{} {
	data := base64.StdEncoding.EncodeToString(a.Data)
	switch {
	case strings.HasPrefix(a.Mime, "image/"):
		return map[string]interface{}{"type": "image", "data": data, "mimeType": a.Mime}
	case strings.HasPrefix(a.Mime, "audio/"):
		return map[string]interface{}{"type": "audio", "data": data, "mimeType": a.Mime}
	}
	return map[string]interface{}{"type": "resource", "resource": map[string]interface{}{
		"uri": "gemini://artifact/" + randHex(8), "mimeType": a.Mime, "blob": data}}
}

// mcpStrings 取 JSON 数组参数里的字符串项，空串跳过。
func mcpStrings(v interface{}) []string {
	arr, _ := v.([]interface{})
	var out []string
	for _, it := range arr {
		if s, ok := it.(string); ok && strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
		}
	}
	return out
}
