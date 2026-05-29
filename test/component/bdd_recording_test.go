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
	// ~60fps capture cap (16ms) keeps the 30fps output fully fed even during fast
	// motion (a 40ms/25fps cap sat below 30fps and juddered). The frame budget is
	// raised to match so a long journey isn't truncated mid-way.
	recordingMinFrameGap = 16 * time.Millisecond
	recordingMaxFrames   = 8000
	// Clamp per-frame display time so a long idle gap doesn't freeze the video.
	recordingMinHold = 0.04
	recordingMaxHold = 5.0
)

// screencastRecorder streams CDP screencast frames straight to a temp dir for a
// scenario, keeping only per-frame timestamps in memory. Buffering thousands of
// full-res JPEGs in a slice (~2-3GB for an 8000-frame journey) OOM-killed the
// run once a live agent container was also resident, so frames go to disk as
// they arrive. Frames land on chromedp's (serial) event goroutine; mu guards the
// counters against stopRecording reading them.
type screencastRecorder struct {
	mu       sync.Mutex
	dir      string      // temp dir holding fNNNNN.jpg frames
	count    int         // frames written so far (also the next frame's index)
	times    []time.Time // wall-clock capture time per kept frame (for real-time pacing)
	lastKept time.Time   // wall-clock time the last frame was retained (throttle)
}

// startRecording subscribes to CDP screencast frames and starts the stream.
// No-op unless LOOP_DOCS_CAPTURE is set, so it adds no overhead to normal runs.
func (tc *TestContext) startRecording() error {
	if os.Getenv("LOOP_DOCS_CAPTURE") == "" {
		return nil
	}
	dir, err := os.MkdirTemp("", "loop-journey-frames-")
	if err != nil {
		return err
	}
	rec := &screencastRecorder{dir: dir}
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
		idx := rec.count
		skip := idx >= recordingMaxFrames ||
			(!rec.lastKept.IsZero() && now.Sub(rec.lastKept) < recordingMinFrameGap)
		rec.mu.Unlock()
		if skip {
			return
		}
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			return
		}
		// Write before counting so a failed write leaves no dangling index that
		// the concat list would reference (frames stay contiguous 0..count-1).
		if err := os.WriteFile(filepath.Join(rec.dir, fmt.Sprintf("f%05d.jpg", idx)), data, 0o644); err != nil {
			return
		}
		rec.mu.Lock()
		rec.lastKept = now
		rec.times = append(rec.times, now)
		rec.count++
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
// MP4 under docs/videos (override via LOOP_DOCS_VIDEO_OUT). The filename carries
// a capture timestamp (e.g. journey-20060102-150405.mp4) so successive runs keep
// their history instead of overwriting. The MP4 is gitignored (intended for
// manual upload), so a missing ffmpeg is a soft failure — the screenshots
// captured during the journey are the committed assets.
func (tc *TestContext) stopRecording(name string) error {
	rec := tc.chromeTab.rec
	if rec == nil {
		return nil
	}
	tc.chromeTab.rec = nil
	_ = chromedp.Run(tc.chromeTab.ctx, page.StopScreencast())
	if rec.dir != "" {
		defer os.RemoveAll(rec.dir)
	}

	rec.mu.Lock()
	count, times := rec.count, rec.times
	rec.mu.Unlock()
	if count == 0 {
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
	outPath := filepath.Join(outDir, fmt.Sprintf("%s-%s.mp4", safe, time.Now().Format("20060102-150405")))
	if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
		return err
	}
	return encodeMP4(rec.dir, count, times, outPath)
}

// encodeMP4 muxes the JPEG frames already streamed to framesDir (f00000.jpg …)
// into an H.264 MP4 via ffmpeg's concat demuxer. Each frame's display duration
// is its real wall-clock gap to the next frame (clamped), so on-screen pauses
// and caption title-cards hold for their actual time; a final fps filter
// resamples to a universally-playable constant 30fps (H.264 makes the
// duplicated hold frames nearly free).
func encodeMP4(framesDir string, count int, times []time.Time, outPath string) error {
	var list strings.Builder
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("f%05d.jpg", i)
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
	if count > 0 {
		fmt.Fprintf(&list, "file '%s'\n", fmt.Sprintf("f%05d.jpg", count-1))
	}
	listPath := filepath.Join(framesDir, "frames.txt")
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
