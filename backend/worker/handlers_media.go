package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/courses"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/queue"
	"github.com/DiegoMunevar2007/Proyecto-1-Cloud.git/storage"
	"github.com/hibiken/asynq"
	"gorm.io/gorm"
)

// MediaHandler procesa multimedia a HLS de forma idempotente.
type MediaHandler struct {
	DB    *gorm.DB
	Store *storage.Client
}

// HandleTranscode descarga el original, genera HLS sin upscaling y conserva el original.
func (h *MediaHandler) HandleTranscode(ctx context.Context, t *asynq.Task) error {
	p, err := queue.DecodeTranscode(t.Payload())
	if err != nil {
		return fmt.Errorf("payload inválido: %w", err)
	}
	if p.ResourceID == 0 || p.ObjectKey == "" {
		return fmt.Errorf("payload incompleto")
	}

	var r courses.Resource
	if err := h.DB.First(&r, p.ResourceID).Error; err != nil {
		return fmt.Errorf("recurso %d no encontrado: %w", p.ResourceID, err)
	}

	// Idempotencia: doble entrega no genera salidas repetidas.
	if r.ProcessingStatus == courses.ProcessingReady && r.HLSKey != "" {
		log.Printf("worker: recurso %d ya procesado (idempotente), skip", r.ID)
		return nil
	}

	h.DB.Model(&r).Updates(map[string]interface{}{"processing_status": courses.ProcessingProcessing})

	tmpDir, err := os.MkdirTemp("", fmt.Sprintf("transcode-%d-", r.ID))
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	ext := filepath.Ext(p.ObjectKey)
	if ext == "" {
		ext = ".bin"
	}
	srcPath := filepath.Join(tmpDir, "original"+ext)
	if err := h.Store.DownloadToFile(storage.BucketOriginals, p.ObjectKey, srcPath); err != nil {
		h.markFailed(&r, fmt.Sprintf("descarga: %v", err))
		return fmt.Errorf("descarga original: %w", err)
	}

	outDir := filepath.Join(tmpDir, "hls")
	if err := os.MkdirAll(outDir, 0755); err != nil {
		return err
	}
	playlist := filepath.Join(outDir, "index.m3u8")

	var cmd *exec.Cmd
	isAudio := strings.Contains(strings.ToLower(r.MimeType), "audio") || r.Type == courses.ResourceTypeAudio
	if isAudio {
		// Audio a HLS (segmentos .ts AAC).
		cmd = exec.Command("ffmpeg", "-y", "-i", srcPath,
			"-c:a", "aac", "-b:a", "128k",
			"-hls_time", "6", "-hls_playlist_type", "vod",
			"-hls_segment_filename", filepath.Join(outDir, "seg%03d.ts"),
			playlist)
	} else {
		// Video a HLS 720p máximo, sin upscaling, conserva aspecto.
		cmd = exec.Command("ffmpeg", "-y", "-i", srcPath,
			"-c:v", "libx264", "-preset", "veryfast", "-crf", "23",
			"-vf", "scale=w=-2:h='min(ih,720)'",
			"-c:a", "aac", "-b:a", "128k",
			"-hls_time", "6", "-hls_playlist_type", "vod",
			"-hls_segment_filename", filepath.Join(outDir, "seg%03d.ts"),
			playlist)
	}
	cmdCtx, cancel := context.WithTimeout(ctx, 25*time.Minute)
	defer cancel()
	cmd = exec.CommandContext(cmdCtx, cmd.Path, cmd.Args[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		h.markFailed(&r, fmt.Sprintf("ffmpeg: %v %.500s", err, string(out)))
		return fmt.Errorf("ffmpeg: %w output=%.500s", err, string(out))
	}

	prefix := fmt.Sprintf("%s/", r.StableID)
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		local := filepath.Join(outDir, e.Name())
		ct := "video/MP2T"
		if strings.HasSuffix(e.Name(), ".m3u8") {
			ct = "application/vnd.apple.mpegurl"
		}
		if err := h.Store.UploadFile(storage.BucketHLS, prefix+e.Name(), local, ct); err != nil {
			h.markFailed(&r, fmt.Sprintf("subida hls: %v", err))
			return fmt.Errorf("subida %s: %w", e.Name(), err)
		}
	}

	// Original se conserva intacto en BucketOriginals; solo se registra el derivado.
	h.DB.Model(&r).Updates(map[string]interface{}{
		"processing_status": courses.ProcessingReady,
		"hls_key":           prefix + "index.m3u8",
	})
	log.Printf("worker: recurso %d HLS listo en %s", r.ID, prefix+"index.m3u8")
	return nil
}

func (h *MediaHandler) markFailed(r *courses.Resource, detail string) {
	log.Printf("worker: recurso %d fallo: %s", r.ID, detail)
	h.DB.Model(r).Update("processing_status", courses.ProcessingFailed)
}
