// 复制到主项目: ai-engine/workflows/videos/video_postprocess.go
//
// 改造摘要（外部行为不变，DSL / 工作流无需修改）：
//   1. VideoPostprocessTool 增加 provider 字段，由 NewVideoPostprocessTool 注入。
//   2. probeVideoMeta()  → provider.Probe()，删除本地 ffprobe exec 调用。
//   3. hasFFmpegFilter() → 删除。ffmpeg-service 容器内 ffmpeg 能力固定，始终支持 drawtext。
//   4. 两处 exec.Command("ffmpeg") → provider.SubmitJob() + provider.WaitJob()。
//   5. 需同步扩展 ffmpeg-service postprocess executor，增加对
//      target_width / target_height / keep_audio / scale_and_pad 参数的支持。
//      （executor 改动只是在 Run() 里多解析几个 param key，不影响接口契约）
package videos

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/tuxi/dream-ai/ai-engine/tool"
	"github.com/tuxi/dream-ai/ai-engine/utils"
	ffmpegproviders "github.com/tuxi/dream-ai/ai-engine/pkg/ffmpeg/providers"
)

type VideoPostprocessTool struct {
	provider *ffmpegproviders.FFmpegServiceProvider // 注入，为空时 panic-fail-fast
}

// NewVideoPostprocessTool 注入 FFmpegServiceProvider。
// server.go 根据 cfg.FFmpeg.ServiceURL 是否为空决定传入真实 provider 还是 nil-guard。
func NewVideoPostprocessTool(provider *ffmpegproviders.FFmpegServiceProvider) *VideoPostprocessTool {
	return &VideoPostprocessTool{provider: provider}
}

// ── 工具元信息：不变 ──────────────────────────────────────────────────────

func (t *VideoPostprocessTool) Name() string { return "video_postprocess" }

func (t *VideoPostprocessTool) Description() string {
	return "视频后处理：按需统一比例/尺寸，并可选加水印，默认保留音频"
}

func (t *VideoPostprocessTool) InputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"local_video_path": {Type: "string", Required: true, Desc: "本地视频路径"},
			"aspect_ratio":     {Type: "string", Required: false, Desc: "目标画面比例"},
			"resolution":       {Type: "string", Required: false, Desc: "目标分辨率"},
			"watermark":        {Type: "bool", Required: false, Desc: "是否添加水印"},
			"keep_audio":       {Type: "bool", Required: false, Desc: "是否保留输入视频音轨，默认 true"},
		},
	}
}

func (t *VideoPostprocessTool) OutputSchema() tool.DataSchema {
	return tool.DataSchema{
		Fields: map[string]tool.FieldSchema{
			"final_video_path": {Type: "string", Desc: "后处理后的本地视频路径"},
			"width":            {Type: "number", Desc: "最终视频宽度"},
			"height":           {Type: "number", Desc: "最终视频高度"},
			"duration":         {Type: "number", Desc: "视频总时长（秒）"},
		},
	}
}

func (t *VideoPostprocessTool) Mode() tool.ExecutionMode { return tool.SyncExecution }

// ── Execute：内部实现替换为 HTTP 调用 ──────────────────────────────────────

func (t *VideoPostprocessTool) Execute(
	ctx context.Context,
	input map[string]any,
	emitter tool.ToolEmitter,
) (*tool.Result, error) {

	localInputPath := input["local_video_path"].(string)

	// 1. 读取源视频信息 —— 原 probeVideoMeta() exec 调用，现改为 HTTP probe
	probeInfo, err := t.provider.Probe(ctx, localInputPath)
	if err != nil {
		errMsg := fmt.Errorf("读取视频信息失败: %w", err)
		tool.EmitFail(emitter, errMsg, map[string]any{"video": localInputPath})
		return nil, errMsg
	}

	// 从 ProbeInfo 中提取字段，保持与原 VideoMeta 相同的逻辑分支
	srcWidth := probeInfo.Width
	srcHeight := probeInfo.Height
	srcDuration := probeInfo.DurationSec
	hasAudio := false
	for _, s := range probeInfo.Streams {
		if s.CodecType == "audio" {
			hasAudio = true
			break
		}
	}

	tool.EmitLog(emitter, "源视频信息", map[string]any{
		"width":     srcWidth,
		"height":    srcHeight,
		"has_audio": hasAudio,
	})

	keepAudio := true
	if raw, exists := input["keep_audio"]; exists {
		keepAudio = utils.ToBool(raw)
	}

	aspectRatio := strings.TrimSpace(utils.ToString(input["aspect_ratio"]))
	resolution := strings.TrimSpace(utils.ToString(input["resolution"]))
	watermarkText := ""
	if utils.ToInt(input["watermark"]) >= 1 {
		watermarkText = "Dream Log"
	}

	missingCanvasParams := aspectRatio == "" || resolution == ""

	// 快速返回：无需任何处理
	if missingCanvasParams && keepAudio && watermarkText == "" {
		return &tool.Result{
			Data: map[string]any{
				"final_video_path": localInputPath,
				"width":            srcWidth,
				"height":           srcHeight,
				"duration":         srcDuration,
			},
		}, nil
	}

	targetW, targetH := srcWidth, srcHeight
	if !missingCanvasParams {
		targetW, targetH = getVideoCanvasSize(aspectRatio, resolution)
	}
	finalPath := fmt.Sprintf("/tmp/final_%d.mp4", time.Now().UnixNano())

	tool.EmitStart(emitter, "开始视频后处理", map[string]any{
		"video":       localInputPath,
		"target_size": fmt.Sprintf("%dx%d", targetW, targetH),
		"keep_audio":  keepAudio,
	})

	needResize := !missingCanvasParams && (srcWidth != targetW || srcHeight != targetH)
	needWatermark := watermarkText != ""

	// 2. 提交 postprocess 任务到 ffmpeg-service ─────────────────────────────
	//
	// params 说明（ffmpeg-service postprocess executor 需支持以下扩展字段）：
	//   video_path    — 输入视频路径（executor 已支持）
	//   output_path   — 指定输出路径，避免 executor 自动命名后需二次映射
	//   watermark_text — 水印文字（executor 已支持）
	//   target_width  — 缩放目标宽度（executor 需扩展：scale+pad filter）
	//   target_height — 缩放目标高度（同上）
	//   keep_audio    — 是否保留音轨（executor 需扩展：控制 -an / -c:a aac）
	//   has_audio     — 源文件是否有音轨（传给 executor 避免其重复 probe）
	//
	// executor 扩展只需在 Run() 里多解析这几个 key 并调整 ffmpeg args，
	// 不影响 API 契约。

	params := map[string]any{
		"video_path":  localInputPath,
		"output_path": finalPath,
		"keep_audio":  keepAudio,
		"has_audio":   hasAudio,
	}
	if needResize {
		params["target_width"] = targetW
		params["target_height"] = targetH
	}
	if needWatermark {
		// hasFFmpegFilter() 检测已删除。
		// ffmpeg-service 容器内 ffmpeg 始终包含 drawtext（Dockerfile 中 apk add ffmpeg）。
		params["watermark_text"] = watermarkText
	}

	tool.EmitLog(emitter, "执行视频后处理", map[string]any{
		"need_resize":    needResize,
		"need_watermark": needWatermark,
	})

	jobID, err := t.provider.SubmitJob(ctx, "postprocess", params)
	if err != nil {
		errMsg := fmt.Errorf("提交视频后处理任务失败: %w", err)
		tool.EmitFail(emitter, errMsg, map[string]any{"video": localInputPath})
		return nil, errMsg
	}

	outputPath, _, err := t.provider.WaitJob(ctx, jobID)
	if err != nil {
		errMsg := fmt.Errorf("视频后处理失败 job_id=%s: %w", jobID, err)
		tool.EmitFail(emitter, errMsg, map[string]any{
			"video":       localInputPath,
			"target_size": fmt.Sprintf("%dx%d", targetW, targetH),
		})
		return nil, errMsg
	}

	tool.EmitComplete(emitter, "视频后处理完成", map[string]any{
		"final_video_path": outputPath,
		"job_id":           jobID,
	})

	finalW := targetW
	finalH := targetH
	if finalW == 0 || finalH == 0 {
		finalW = srcWidth
		finalH = srcHeight
	}

	return &tool.Result{
		Data: map[string]any{
			"final_video_path": outputPath,
			"width":            finalW,
			"height":           finalH,
			"duration":         srcDuration,
		},
	}, nil
}
