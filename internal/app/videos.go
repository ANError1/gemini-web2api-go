package app

// /v1/videos —— OpenAI（Sora）形状的异步视频生成端点。
//
// gemini 视频要几十秒到几分钟，同步阻塞一个 HTTP 请求不现实，所以照 OpenAI Sora 那套走异步：
//   POST /v1/videos               建任务，立即返回 {id, status:"queued"}
//   GET  /v1/videos/{id}          轮询状态 queued|in_progress|completed|failed
//   GET  /v1/videos/{id}/content  完成后下 MP4
// 底层复用 callGemini（跟 /v1/chat/completions 里 model=gemini-video 同一条链），
// 只是把「阻塞等结果」挪到后台 goroutine，前台立即返 id。

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

type videoJob struct {
	ID        string `json:"id"`
	Object    string `json:"object"`
	Model     string `json:"model"`
	Status    string `json:"status"` // queued | in_progress | completed | failed
	CreatedAt int64  `json:"created_at"`
	Prompt    string `json:"prompt,omitempty"`
	Size      string `json:"size,omitempty"` // "1280x720" (16:9) or "720x1280" (9:16)
	Error     string `json:"error,omitempty"`

	mp4  []byte // 完成后的视频字节，不进 JSON
	mime string
}

var (
	videoJobs   = map[string]*videoJob{}
	videoJobsMu sync.Mutex
)

func putVideoJob(j *videoJob) {
	videoJobsMu.Lock()
	// 顺手清掉 2 小时前的旧任务，别让内存里越堆越多。
	cutoff := time.Now().Unix() - 2*3600
	for id, old := range videoJobs {
		if old.CreatedAt < cutoff {
			delete(videoJobs, id)
		}
	}
	videoJobs[j.ID] = j
	videoJobsMu.Unlock()
}

func getVideoJob(id string) *videoJob {
	videoJobsMu.Lock()
	defer videoJobsMu.Unlock()
	return videoJobs[id]
}

// handleCreateVideo 处理 POST /v1/videos。
func handleCreateVideo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, 405, map[string]any{"error": map[string]string{"message": "method not allowed"}})
		return
	}
	var req map[string]any
	var fileRef *pendingUpload
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// OpenAI SDK shape: videos.create(prompt=..., input_reference=<file>) is multipart.
		if err := r.ParseMultipartForm(maxImageBytes + 1<<20); err != nil {
			writeJSON(w, 400, map[string]any{"error": map[string]string{"message": "bad multipart body: " + err.Error()}})
			return
		}
		req = map[string]any{}
		for k, v := range r.MultipartForm.Value {
			if len(v) > 0 {
				req[k] = v[0]
			}
		}
		if f, hdr, err := r.FormFile("input_reference"); err == nil {
			data, rerr := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
			f.Close()
			mime := hdr.Header.Get("Content-Type")
			if mime == "" || mime == "application/octet-stream" {
				mime = http.DetectContentType(data)
			}
			up, uerr := newMediaUpload(data, mime, 1)
			if rerr != nil || uerr != nil {
				writeJSON(w, 400, map[string]any{"error": map[string]string{"message": fmt.Sprintf("input_reference: %v%v", rerr, uerr), "type": "invalid_request_error"}})
				return
			}
			fileRef = &up
		}
	} else if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]string{"message": "bad json body"}})
		return
	}
	prompt, _ := req["prompt"].(string)
	if strings.TrimSpace(prompt) == "" {
		writeJSON(w, 400, map[string]any{"error": map[string]string{"message": "missing prompt", "type": "invalid_request_error"}})
		return
	}
	model, _ := req["model"].(string)
	if model == "" {
		model = "gemini-video"
	}
	// 校验模型确实是视频模型（查基础配置，不用 resolveModel —— 它无 cookie 时对登录态模型
	// 直接返「需要 cookie」错，那个错该留给 job 里报，别在这一步糊成「不是视频模型」）。
	if base, ok := Models[model]; !ok || base.Tool != toolVideo {
		writeJSON(w, 400, map[string]any{"error": map[string]string{
			"message": "model must be a video model (gemini-video)", "type": "invalid_request_error"}})
		return
	}
	// Image→video: OpenAI Sora's `input_reference` (start frame). Accepts a data: URL,
	// an http(s) URL, or {"image_url": "..."} / {"url": "..."}. Materialized now so a
	// bad image fails the POST (400) instead of a queued job.
	var images []pendingUpload
	if fileRef != nil {
		images = append(images, *fileRef)
	} else if ref := inputReferenceSrc(req["input_reference"]); ref != "" {
		img, err := materializeImage(ref, "", 1)
		if err != nil {
			writeJSON(w, 400, map[string]any{"error": map[string]string{"message": "input_reference: " + err.Error(), "type": "invalid_request_error"}})
			return
		}
		images = append(images, img)
	}
	size, err := videoSize(req)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": map[string]string{"message": err.Error(), "type": "invalid_request_error"}})
		return
	}
	j := &videoJob{
		Size:      size,
		ID:        "video_" + randHex(16),
		Object:    "video",
		Model:     model,
		Status:    "queued",
		CreatedAt: time.Now().Unix(),
		Prompt:    prompt,
	}
	putVideoJob(j)
	go runVideoJob(j, images)
	writeJSON(w, 200, j)
}

// videoSize maps OpenAI's `size` ("1280x720"/"720x1280") or an `aspect_ratio`
// ("16:9"/"9:16") onto the two shapes Gemini web can produce. Default 16:9.
func videoSize(req map[string]any) (string, error) {
	s, _ := req["size"].(string)
	if s == "" {
		s, _ = req["aspect_ratio"].(string)
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "16:9", "landscape", "1280x720", "1920x1080":
		return "1280x720", nil
	case "9:16", "portrait", "720x1280", "1080x1920":
		return "720x1280", nil
	}
	return "", fmt.Errorf("unsupported size %q: use 1280x720 (16:9) or 720x1280 (9:16)", s)
}

// inputReferenceSrc pulls an image source out of the input_reference field.
func inputReferenceSrc(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case map[string]any:
		for _, k := range []string{"image_url", "url", "data"} {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
			if m, ok := x[k].(map[string]any); ok {
				if s, ok := m["url"].(string); ok {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}

// runVideoJob 后台跑视频生成，结果写回 job。
func runVideoJob(j *videoJob, images []pendingUpload) {
	videoJobsMu.Lock()
	j.Status = "in_progress"
	videoJobsMu.Unlock()

	_, mc, err := resolveModel(j.Model)
	if err != nil {
		finishVideoJob(j, nil, "", err.Error())
		return
	}
	if j.Size == "720x1280" {
		mc.VideoAspect = 17
	}
	_, _, res, err := callGemini(j.Prompt, j.Prompt, mc, nil, images, nil, nil)
	if err != nil {
		recordRequest("videos", j.Model, j.Prompt, "", res, 502, err.Error(), false)
		finishVideoJob(j, nil, "", err.Error())
		return
	}
	if res == nil || len(res.Artifacts) == 0 {
		recordRequest("videos", j.Model, j.Prompt, "", res, 502, "no video produced", false)
		finishVideoJob(j, nil, "", "no video produced")
		return
	}
	a := res.Artifacts[0]
	recordRequest("videos", j.Model, j.Prompt, "", res, 200, "", false)
	finishVideoJob(j, a.Data, a.Mime, "")
}

func finishVideoJob(j *videoJob, mp4 []byte, mime, errStr string) {
	videoJobsMu.Lock()
	defer videoJobsMu.Unlock()
	if errStr != "" {
		j.Status = "failed"
		j.Error = errStr
		return
	}
	j.mp4 = mp4
	j.mime = mime
	if j.mime == "" {
		j.mime = "video/mp4"
	}
	j.Status = "completed"
}

// handleVideoItem 处理 GET /v1/videos/{id} 和 /v1/videos/{id}/content。
func handleVideoItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"error": map[string]string{"message": "method not allowed"}})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/v1/videos/")
	parts := strings.SplitN(rest, "/", 2)
	id := parts[0]
	j := getVideoJob(id)
	if j == nil {
		writeJSON(w, 404, map[string]any{"error": map[string]string{"message": "video job not found", "type": "not_found"}})
		return
	}
	if len(parts) == 2 && parts[1] == "content" {
		videoJobsMu.Lock()
		status, mp4, mime := j.Status, j.mp4, j.mime
		videoJobsMu.Unlock()
		if status != "completed" || len(mp4) == 0 {
			writeJSON(w, 404, map[string]any{"error": map[string]string{
				"message": "video not ready (status=" + status + ")", "type": "not_found"}})
			return
		}
		w.Header().Set("Content-Type", mime)
		w.WriteHeader(200)
		_, _ = w.Write(mp4)
		return
	}
	writeJSON(w, 200, j)
}
