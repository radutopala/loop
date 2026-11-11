package dictation

import (
	"context"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Event types a session emits.
const (
	EventReady   = "ready"
	EventPartial = "partial"
	EventFinal   = "final"
	EventError   = "error"
)

// Error codes an error event carries.
const (
	CodeDisabled      = "disabled"
	CodeBinaryMissing = "binary_missing"
	CodeModelMissing  = "model_missing"
	CodeEngineFailed  = "engine_failed"
	CodeBadAudio      = "bad_audio"
)

// Event is a message to the client. A partial's Committed is the session's
// stable text so far, which only grows; Tentative is the unstable tail the
// next partial may change.
type Event struct {
	Type      string
	Committed string
	Tentative string
	Text      string
	Code      string
	Message   string
}

// MarshalJSON writes the fields the event's type has.
func (e Event) MarshalJSON() ([]byte, error) {
	m := map[string]string{"type": e.Type}
	switch e.Type {
	case EventPartial:
		m["committed"], m["tentative"] = e.Committed, e.Tentative
	case EventFinal:
		m["text"] = e.Text
	case EventError:
		m["code"], m["message"] = e.Code, e.Message
	}
	return json.Marshal(m)
}

// Transcript is what a pass heard: its text, and the language whisper
// detected in the audio, with how sure it was.
type Transcript struct {
	Text        string
	Language    string
	Probability float64
}

// TranscribeFunc returns what was spoken in samples.
type TranscribeFunc func(ctx context.Context, samples []int16, language, prompt string) (Transcript, error)

// autoLanguage has whisper detect the language.
const autoLanguage = "auto"

// lockProbability is how sure a pass must be of the language it detected
// for the rest of the session to use it. Short audio is often taken for
// another language; decoding in the wrong one gives a translation.
const lockProbability = 0.8

const (
	// maxSegment ends a segment of uninterrupted speech, so passes stay short.
	maxSegment = 25 * SampleRate
	// silenceEnd is the trailing silence that ends a segment.
	silenceEnd = 7 * SampleRate / 10
	// keepSilence is how much of audio that is all silence is kept, so the
	// start of speech isn't cut.
	keepSilence = 3 * SampleRate / 10
	// frameLen is the VAD frame: 30 ms.
	frameLen = 480
	// promptWords is how much finished text is given to whisper as context.
	promptWords = 50
	// maxFinalPass is the longest recording transcribed again as a whole
	// when the session stops; a longer one keeps its live text.
	maxFinalPass = 120 * SampleRate
)

// Session turns streamed audio into live text. Audio is split into segments
// at pauses; every interval the current segment is transcribed again, and
// the words two passes in a row agree on are committed. When it stops, the
// whole recording is transcribed once more, so the final text is read with
// all of its context rather than pieced together from segments.
type Session struct {
	transcribe TranscribeFunc
	language   string
	emit       func(Event)
	interval   time.Duration
	// threshold is the RMS above which a frame is speech.
	threshold float64

	mu    sync.Mutex
	audio []int16
	// all is the whole recording, for the final pass; nil once it is
	// longer than maxFinalPass.
	all    []int16
	long   bool
	fresh  bool
	done   []string
	agreed []string
	last   []string
	// detected is the language an auto session settled on.
	detected  string
	cancelled bool
	finish    chan struct{}
	once      sync.Once
}

// NewSession returns a session that transcribes with transcribe and sends its
// events to emit. emit is called from Run's goroutine only.
func NewSession(transcribe TranscribeFunc, language string, emit func(Event)) *Session {
	return &Session{
		transcribe: transcribe,
		language:   language,
		emit:       emit,
		interval:   500 * time.Millisecond,
		threshold:  400,
		finish:     make(chan struct{}),
	}
}

// Write adds samples (16 kHz mono) to the session.
func (s *Session) Write(samples []int16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audio = append(s.audio, samples...)
	s.fresh = true
	if s.long {
		return
	}
	if len(s.all)+len(samples) > maxFinalPass {
		s.all, s.long = nil, true
		return
	}
	s.all = append(s.all, samples...)
}

// Stop ends the session: Run transcribes what's left and emits the final text.
func (s *Session) Stop() { s.once.Do(func() { close(s.finish) }) }

// Cancel ends the session without a final text.
func (s *Session) Cancel() {
	s.mu.Lock()
	s.cancelled = true
	s.mu.Unlock()
	s.Stop()
}

// Run transcribes until the session is stopped or ctx ends. It returns after
// the final event, an error event, or a cancel.
func (s *Session) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.finish:
			s.mu.Lock()
			cancelled := s.cancelled
			s.mu.Unlock()
			if !cancelled {
				s.finalize(ctx)
			}
			return
		case <-ticker.C:
			if err := s.step(ctx); err != nil {
				s.fail(err)
				return
			}
		}
	}
}

func (s *Session) fail(err error) {
	s.emit(Event{Type: EventError, Code: CodeEngineFailed, Message: err.Error()})
}

// step transcribes the current segment if new audio arrived, emitting a
// partial. A segment followed by a pause, or grown too long, is finished.
func (s *Session) step(ctx context.Context) error {
	s.mu.Lock()
	if !s.fresh || len(s.audio) < minSamples {
		s.mu.Unlock()
		return nil
	}
	s.fresh = false
	if !s.voiced(s.audio) {
		s.audio = slices.Clone(s.audio[len(s.audio)-keepSilence:])
		s.mu.Unlock()
		return nil
	}
	snap := slices.Clone(s.audio)
	prompt := s.prompt()
	s.mu.Unlock()

	t, err := s.pass(ctx, snap, prompt)
	if err != nil {
		return err
	}
	words := Words(t.Text)

	s.mu.Lock()
	rest := continuation(s.agreed, words)
	if s.trailingSilence(snap) >= silenceEnd || len(snap) >= maxSegment {
		s.done = slices.Concat(s.done, s.agreed, rest)
		s.audio = slices.Clone(s.audio[len(snap):])
		s.agreed, s.last, rest = nil, nil, nil
	} else {
		k := commonPrefix(rest, continuation(s.agreed, s.last))
		s.agreed = slices.Concat(s.agreed, rest[:k])
		s.last, rest = words, rest[k:]
	}
	committed := strings.Join(slices.Concat(s.done, s.agreed), " ")
	s.mu.Unlock()
	s.emit(Event{Type: EventPartial, Committed: committed, Tentative: strings.Join(rest, " ")})
	return nil
}

// finalize emits the final text: the whole recording transcribed again,
// or, when that is too long or comes back empty, the live text with what's
// left of the audio transcribed after it.
func (s *Session) finalize(ctx context.Context) {
	s.mu.Lock()
	all := slices.Clone(s.all)
	s.mu.Unlock()
	if s.voiced(all) {
		t, err := s.pass(ctx, all, "")
		if err != nil {
			s.fail(err)
			return
		}
		if w := Words(t.Text); len(w) > 0 {
			s.emit(Event{Type: EventFinal, Text: strings.Join(w, " ")})
			return
		}
	}
	s.mu.Lock()
	snap := slices.Clone(s.audio)
	prompt := s.prompt()
	words := slices.Clone(s.agreed)
	s.mu.Unlock()
	if s.voiced(snap) {
		t, err := s.pass(ctx, snap, prompt)
		if err != nil {
			s.fail(err)
			return
		}
		words = slices.Concat(words, continuation(words, Words(t.Text)))
	}
	s.mu.Lock()
	text := strings.Join(slices.Concat(s.done, words), " ")
	s.mu.Unlock()
	s.emit(Event{Type: EventFinal, Text: text})
}

// pass transcribes samples in the session's language. An auto session
// keeps to the first language a pass is sure of.
func (s *Session) pass(ctx context.Context, samples []int16, prompt string) (Transcript, error) {
	s.mu.Lock()
	language := s.language
	if language == autoLanguage && s.detected != "" {
		language = s.detected
	}
	s.mu.Unlock()
	t, err := s.transcribe(ctx, samples, language, prompt)
	if err != nil {
		return t, err
	}
	s.mu.Lock()
	if s.language == autoLanguage && s.detected == "" && t.Language != "" && t.Probability >= lockProbability {
		s.detected = t.Language
	}
	s.mu.Unlock()
	return t, nil
}

// prompt is the end of the finished text. Callers hold mu.
func (s *Session) prompt() string {
	return strings.Join(s.done[max(len(s.done)-promptWords, 0):], " ")
}

// voiced reports whether any frame of samples is speech.
func (s *Session) voiced(samples []int16) bool {
	for i := 0; i+frameLen <= len(samples); i += frameLen {
		if rms(samples[i:i+frameLen]) > s.threshold {
			return true
		}
	}
	return false
}

// trailingSilence is how many samples at the end of samples are silence, in
// whole frames.
func (s *Session) trailingSilence(samples []int16) int {
	n := 0
	for end := len(samples); end-frameLen >= 0 && rms(samples[end-frameLen:end]) <= s.threshold; end -= frameLen {
		n += frameLen
	}
	return n
}

func rms(frame []int16) float64 {
	var sum float64
	for _, v := range frame {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(frame)))
}

// nonSpeech matches whisper's annotations, such as [BLANK_AUDIO].
var nonSpeech = regexp.MustCompile(`\[[^\]]*\]`)

// Words splits whisper's text into words, without its annotations.
func Words(text string) []string {
	return strings.Fields(nonSpeech.ReplaceAllString(text, " "))
}

// alignWindow is how far from its position in an earlier pass the end of
// the committed words is looked for in a later one.
const alignWindow = 4

// continuation returns the words of a pass that come after agreed, the
// words already committed from earlier passes. A pass may word the start
// differently, shifting what follows, so the end of agreed is found in it
// by its last words, near where it was.
func continuation(agreed, words []string) []string {
	n := len(agreed)
	if n == 0 {
		return words
	}
	best, bestScore := -1, 0
	for i := max(n-1-alignWindow, 0); i < min(n+alignWindow, len(words)); i++ {
		score := 0
		for score <= i && score < n && norm(words[i-score]) == norm(agreed[n-1-score]) {
			score++
		}
		if score > bestScore || (score == bestScore && score > 0 && abs(i-(n-1)) < abs(best-(n-1))) {
			best, bestScore = i, score
		}
	}
	if best >= 0 {
		return words[best+1:]
	}
	return words[min(n, len(words)):]
}

// norm is a word without case or surrounding punctuation, for aligning.
func norm(w string) string {
	return strings.ToLower(strings.TrimFunc(w, unicode.IsPunct))
}

func abs(n int) int { return max(n, -n) }

func commonPrefix(a, b []string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
