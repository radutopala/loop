//go:build component

package component

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"encoding/base64"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// The end-to-end journey is recorded as one screencast and muxed into an MP4 by
// ffmpeg (baked into the test-runner image). Throttle a little to bound memory
// and keep the input rate sane; per-frame timestamps are kept so playback
// preserves real-time pacing (holds/captions become real on-screen pauses).
const (
	recordingMinFrameGap = 40 * time.Millisecond
	recordingMaxFrames   = 3000
	// Clamp per-frame display time so a long idle gap doesn't freeze the video.
	recordingMinHold = 0.04
	recordingMaxHold = 5.0
)

// screencastRecorder buffers CDP screencast frames for a scenario. Frames
// arrive on chromedp's event goroutine, so access is guarded by mu.
type screencastRecorder struct {
	mu       sync.Mutex
	frames   [][]byte    // raw JPEG bytes, in capture order
	times    []time.Time // wall-clock capture time per kept frame (for real-time pacing)
	lastKept time.Time   // wall-clock time the last frame was retained (throttle)
}

// startRecording subscribes to CDP screencast frames and starts the stream.
// No-op unless LOOP_DOCS_CAPTURE is set, so it adds no overhead to normal runs.
func (tc *TestContext) startRecording() error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	rec := &screencastRecorder{}
	tc.chromeTab.rec = rec
	ctx := tc.chromeTab.ctx

	chromedp.ListenTarget(ctx, func(ev interface{}) {
		f, ok := ev.(*page.EventScreencastFrame)
		if !ok {
			return
		}
		// Chrome pauses the stream until each frame is acknowledged, so ack
		// every frame — but only retain a throttled, capped subset (see consts).
		go func(sid int64) { _ = chromedp.Run(ctx, page.ScreencastFrameAck(sid)) }(f.SessionID)
		now := time.Now()
		rec.mu.Lock()
		skip := len(rec.frames) >= recordingMaxFrames ||
			(!rec.lastKept.IsZero() && now.Sub(rec.lastKept) < recordingMinFrameGap)
		if !skip {
			rec.lastKept = now
		}
		rec.mu.Unlock()
		if skip {
			return
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return
		}
		rec.mu.Lock()
		rec.frames = append(rec.frames, data)
		rec.times = append(rec.times, now)
		rec.mu.Unlock()
	})

	// Match the docs-capture viewport (1600x1000 @ 2x DPI = 3200x2000) so the
	// MP4 is as crisp as the screenshots. H.264 compresses this fine.
	return chromedp.Run(ctx,
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(80).
			WithMaxWidth(3200).
			WithMaxHeight(2000).
			WithEveryNthFrame(1),
	)
}

// stopRecording stops the screencast and muxes the captured JPEG frames into an
// MP4 under docs/videos (override via LOOP_DOCS_VIDEO_OUT). The MP4 is gitignored
// (intended for manual upload), so a missing ffmpeg is a soft failure — the
// screenshots captured during the journey are the committed assets.
func (tc *TestContext) stopRecording(name string) error {
	rec := tc.chromeTab.rec
	if rec == nil {
		return nil
	}
	tc.chromeTab.rec = nil
	_ = chromedp.Run(tc.chromeTab.ctx, page.StopScreencast())

	rec.mu.Lock()
	frames, times := rec.frames, rec.times
	rec.mu.Unlock()
	if len(frames) == 0 {
		return fmt.Errorf("recording %q captured no frames", name)
	}

	if _, err := exec.LookPath("ffmpeg"); err != nil {
		fmt.Printf("[docs-capture] ffmpeg not found in PATH; skipping MP4 for %q\n", name)
		return nil
	}

	outDir := os.Getenv("LOOP_DOCS_VIDEO_OUT")
	if outDir == "" {
		outDir = filepath.Join("..", "..", "docs", "videos")
	}
	safe := strings.TrimPrefix(filepath.Clean("/"+name), "/")
	outPath := filepath.Join(outDir, safe+".mp4")
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return encodeMP4(frames, times, outPath)
}

// encodeMP4 writes the JPEG frames to a temp dir and muxes them into an H.264
// MP4 via ffmpeg's concat demuxer. Each frame's display duration is its real
// wall-clock gap to the next frame (clamped), so on-screen pauses and caption
// title-cards hold for their actual time; a final fps filter resamples to a
// universally-playable constant 30fps (H.264 makes the duplicated hold frames
// nearly free).
func encodeMP4(frames [][]byte, times []time.Time, outPath string) error {
	dir, err := os.MkdirTemp("", "loop-journey-frames-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	var list strings.Builder
	for i, raw := range frames {
		name := fmt.Sprintf("f%05d.jpg", i)
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
			return err
		}
		hold := recordingMaxHold
		if i+1 < len(times) {
			hold = times[i+1].Sub(times[i]).Seconds()
		} else {
			hold = 2.0 // final frame lingers briefly
		}
		if hold < recordingMinHold {
			hold = recordingMinHold
		}
		if hold > recordingMaxHold {
			hold = recordingMaxHold
		}
		fmt.Fprintf(&list, "file '%s'\nduration %.3f\n", name, hold)
	}
	// The concat demuxer ignores the last entry's duration unless the file is
	// repeated once more — append it so the final frame honors its hold.
	if len(frames) > 0 {
		fmt.Fprintf(&list, "file '%s'\n", fmt.Sprintf("f%05d.jpg", len(frames)-1))
	}
	listPath := filepath.Join(dir, "frames.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return err
	}

	cmd := exec.Command("ffmpeg", "-y",
		"-f", "concat", "-safe", "0", "-i", listPath,
		"-vf", "scale=trunc(iw/2)*2:trunc(ih/2)*2,fps=30", // even dims + CFR for compatibility
		"-c:v", "libx264",
		"-pix_fmt", "yuv420p",
		"-movflags", "+faststart",
		outPath,
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w\n%s", err, out)
	}
	return nil
}
