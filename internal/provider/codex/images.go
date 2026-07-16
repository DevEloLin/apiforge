package codex

import (
	"encoding/json"
	"io"
	"strconv"
	"strings"

	"apiforge/internal/types"
	"apiforge/internal/util/idgen"
	"apiforge/internal/util/sse"
)

// Codex image support goes through the ChatGPT backend Responses API
// `image_generation` tool. The final base64 arrives in the
// `image_generation_call` output item's `result`; partials stream as
// `response.image_generation_call.partial_image` (`partial_image_b64`).

// imageModelFor puts the requested image model on the tool's model field, or
// omits it (letting the backend pick its default) for the gpt-image-1 alias.
func imageModelFor(model string) string {
	if model == "" || model == "gpt-image-1" {
		return ""
	}
	if strings.HasPrefix(model, "gpt-image") {
		return model
	}
	return ""
}

// hostModel is the text model that carries the image_generation tool call.
func hostModel(model string) string {
	if model != "" && model != "gpt-image-1" && strings.HasPrefix(model, "gpt-5") {
		return model
	}
	return "gpt-5.5"
}

func imageTool(req types.ImageRequest) map[string]any {
	tool := map[string]any{"type": "image_generation"}
	if m := imageModelFor(req.Model); m != "" {
		tool["model"] = m
	}
	if req.Size != "" && req.Size != "auto" {
		tool["size"] = req.Size
	}
	if req.Quality != "" && req.Quality != "auto" {
		tool["quality"] = req.Quality
	}
	if req.OutputFormat != "" {
		tool["output_format"] = req.OutputFormat
	}
	if req.Background != "" && req.Background != "auto" {
		tool["background"] = req.Background
	}
	if req.Mask != nil {
		tool["input_image_mask"] = map[string]any{"image_url": dataURL(*req.Mask)}
	}
	return tool
}

func dataURL(img types.ImageInput) string {
	ct := img.ContentType
	if ct == "" {
		ct = "image/png"
	}
	return "data:" + ct + ";base64," + img.B64
}

func baseImageBody(model string, content []any, tool map[string]any, sessionID string) map[string]any {
	return map[string]any{
		"model":               model,
		"instructions":        "",
		"input":               []any{map[string]any{"type": "message", "role": "user", "content": content}},
		"tools":               []any{tool},
		"tool_choice":         "auto",
		"parallel_tool_calls": false,
		"store":               false,
		"stream":              true,
		"reasoning":           nil,
		"prompt_cache_key":    sessionID,
		"include":             []any{},
	}
}

func buildImageGenBody(req types.ImageRequest, sessionID string) map[string]any {
	content := []any{map[string]any{"type": "input_text", "text": req.Prompt}}
	return baseImageBody(hostModel(req.Model), content, imageTool(req), sessionID)
}

// buildImageEditBody wraps each input image in <image name=...> labels with
// detail:high (matching the reference client), then appends the prompt.
func buildImageEditBody(req types.ImageRequest, sessionID string) map[string]any {
	content := []any{}
	for i, img := range req.Images {
		content = append(content,
			map[string]any{"type": "input_text", "text": "<image name=[Image #" + strconv.Itoa(i+1) + "]>"},
			map[string]any{"type": "input_image", "image_url": dataURL(img), "detail": "high"},
			map[string]any{"type": "input_text", "text": "</image>"},
		)
	}
	content = append(content, map[string]any{"type": "input_text", "text": req.Prompt})
	return baseImageBody(hostModel(req.Model), content, imageTool(req), sessionID)
}

type collectedImage struct {
	b64           string
	outputFormat  string
	size          string
	revisedPrompt string
}

// collectImage consumes a Responses SSE stream and returns the generated image,
// preferring the final result and only falling back to the last partial if the
// stream actually completed (a truncated partial would be misleading). The
// returned completed flag lets the caller tell a deterministic "no image"
// (refusal/empty on a completed stream — not worth retrying) from a truncation.
func collectImage(upstream io.Reader) (img *collectedImage, completed bool) {
	var lastPartial, outputFormat, size string
	outputFormat = "png"
	completed = false

	for ev := range sse.Frames(upstream) {
		if ev.Data == "" || ev.Data == "[DONE]" {
			continue
		}
		var evt map[string]any
		if json.Unmarshal([]byte(ev.Data), &evt) != nil {
			continue
		}
		switch str(evt["type"]) {
		case "response.completed":
			completed = true
		case "response.image_generation_call.partial_image":
			if p := str(evt["partial_image_b64"]); p != "" {
				lastPartial = p
			}
			if f := str(evt["output_format"]); f != "" {
				outputFormat = f
			}
			if s := str(evt["size"]); s != "" {
				size = s
			}
		case "response.output_item.done":
			item, _ := evt["item"].(map[string]any)
			if item != nil && str(item["type"]) == "image_generation_call" {
				if result := str(item["result"]); result != "" {
					return &collectedImage{
						b64:           result,
						outputFormat:  firstNonEmpty(str(item["output_format"]), outputFormat),
						size:          firstNonEmpty(str(item["size"]), size),
						revisedPrompt: str(item["revised_prompt"]),
					}, completed
				}
			}
		}
	}
	if completed && lastPartial != "" {
		return &collectedImage{b64: lastPartial, outputFormat: outputFormat, size: size}, completed
	}
	return nil, completed
}

// toImagesResponse emits the official OpenAI Images API response shape.
func toImagesResponse(images []*collectedImage) map[string]any {
	data := make([]any, 0, len(images))
	for _, img := range images {
		entry := map[string]any{"b64_json": img.b64}
		if img.revisedPrompt != "" {
			entry["revised_prompt"] = img.revisedPrompt
		}
		data = append(data, entry)
	}
	resp := map[string]any{
		"created": idgen.NowSeconds(),
		"data":    data,
		"usage": map[string]any{
			"input_tokens": 0, "output_tokens": 0, "total_tokens": 0,
			"input_tokens_details": map[string]any{"text_tokens": 0, "image_tokens": 0},
		},
	}
	if len(images) > 0 {
		resp["output_format"] = images[0].outputFormat
		if images[0].size != "" {
			resp["size"] = images[0].size
		}
	} else {
		resp["output_format"] = "png"
	}
	return resp
}
