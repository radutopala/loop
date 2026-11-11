package dictation

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
)

// SampleRate is the rate of the audio dictation takes: 16 kHz mono PCM.
const SampleRate = 16000

// minSamples is the shortest audio whisper is given: below about two seconds
// it tends to make text up, so shorter audio is padded with silence.
const minSamples = 2 * SampleRate

// ServerArgs returns whisper-server's arguments for model, listening on
// port: greedy decoding, no timestamps, non-speech tokens suppressed. threads
// 0 keeps whisper-server's default.
func ServerArgs(model string, threads, port int) []string {
	args := []string{"-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port), "-bs", "1", "-bo", "1", "-nt", "-sns"}
	if threads > 0 {
		args = append(args, "-t", strconv.Itoa(threads))
	}
	return args
}

// Engine sends audio to a running whisper-server.
type Engine struct {
	client HTTPClient
}

// NewEngine returns an engine using client.
func NewEngine(client HTTPClient) *Engine { return &Engine{client: client} }

// Transcribe returns what was spoken in samples (16 kHz mono), in language
// ("auto" detects it), continuing from prompt, and the language whisper
// detected.
func (e *Engine) Transcribe(ctx context.Context, baseURL string, samples []int16, language, prompt string) (Transcript, error) {
	if len(samples) < minSamples {
		samples = append(samples[:len(samples):len(samples)], make([]int16, minSamples-len(samples))...)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "audio.wav")
	_, _ = fw.Write(EncodeWAV(samples))
	fields := map[string]string{
		"language":        language,
		"temperature":     "0",
		"response_format": "verbose_json",
		"audio_ctx":       strconv.Itoa(audioCtx(len(samples))),
		"prompt":          prompt,
	}
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	_ = mw.Close()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/inference", &body)
	if err != nil {
		return Transcript{}, err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := e.client.Do(req)
	if err != nil {
		return Transcript{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var out struct {
		Text        string  `json:"text"`
		Language    string  `json:"detected_language"`
		Probability float64 `json:"detected_language_probability"`
		Error       string  `json:"error"`
	}
	if err := json.Unmarshal(data, &out); err != nil || resp.StatusCode != http.StatusOK || out.Error != "" {
		return Transcript{}, fmt.Errorf("whisper-server: HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(data))
	}
	return Transcript{Text: out.Text, Language: out.Language, Probability: out.Probability}, nil
}

// audioCtx sizes whisper's encoder window (1500 frames, 50 a second, is 30
// seconds) to the audio, since a shorter window is what makes short audio
// fast. Only a few sizes are used, each with room to spare: measured with
// large-v3-turbo, windows under 512 or fitted tightly to the audio make it
// drop or invent words.
func audioCtx(samples int) int {
	frames := samples * 50 / SampleRate
	for _, size := range []int{512, 768, 1024} {
		if frames*7 <= size*5 {
			return size
		}
	}
	return 1500
}

// EncodeWAV wraps 16 kHz mono samples in a WAV file.
func EncodeWAV(samples []int16) []byte {
	var b bytes.Buffer
	size := uint32(len(samples) * 2)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, 36+size)
	b.WriteString("WAVEfmt ")
	// fmt chunk: PCM, mono, 16 kHz, 2 bytes a frame, 16 bits.
	for _, v := range []any{uint32(16), uint16(1), uint16(1), uint32(SampleRate), uint32(SampleRate * 2), uint16(2), uint16(16)} {
		_ = binary.Write(&b, binary.LittleEndian, v)
	}
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, size)
	_ = binary.Write(&b, binary.LittleEndian, samples)
	return b.Bytes()
}
