package dictation

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// speech is n samples loud enough to be speech; silence is n quiet ones.
func speech(n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = 3000
		if i%2 == 1 {
			out[i] = -3000
		}
	}
	return out
}

func silence(n int) []int16 { return make([]int16, n) }

// call is one transcription a fake transcriber was asked for.
type call struct {
	samples          int
	language, prompt string
}

// fakeTranscriber answers with texts in order, recording the calls.
type fakeTranscriber struct {
	mu     sync.Mutex
	texts  []string
	err    error
	calls  []call
	during func()
}

func (f *fakeTranscriber) transcribe(_ context.Context, samples []int16, language, prompt string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call{samples: len(samples), language: language, prompt: prompt})
	if f.during != nil {
		f.during()
	}
	if f.err != nil {
		return "", f.err
	}
	text := f.texts[0]
	f.texts = f.texts[1:]
	return text, nil
}

type SessionSuite struct {
	suite.Suite
	tr     *fakeTranscriber
	sess   *Session
	mu     sync.Mutex
	events []Event
}

func TestSessionSuite(t *testing.T) { suite.Run(t, new(SessionSuite)) }

func (s *SessionSuite) SetupTest() {
	s.tr = &fakeTranscriber{}
	s.events = nil
	s.sess = NewSession(s.tr.transcribe, "en", func(e Event) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.events = append(s.events, e)
	})
}

func (s *SessionSuite) emitted() []Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.events)
}

func partial(committed, tentative string) Event {
	return Event{Type: EventPartial, Committed: committed, Tentative: tentative}
}

func (s *SessionSuite) TestEventJSON() {
	tests := []struct {
		event Event
		want  string
	}{
		{Event{Type: EventReady, Text: "x"}, `{"type":"ready"}`},
		{partial("a", ""), `{"committed":"a","tentative":"","type":"partial"}`},
		{Event{Type: EventFinal, Text: "a b"}, `{"text":"a b","type":"final"}`},
		{Event{Type: EventError, Code: CodeEngineFailed, Message: "m"}, `{"code":"engine_failed","message":"m","type":"error"}`},
	}
	for _, tc := range tests {
		data, err := json.Marshal(tc.event)
		require.NoError(s.T(), err)
		require.JSONEq(s.T(), tc.want, string(data))
	}
}

func (s *SessionSuite) TestWords() {
	require.Equal(s.T(), []string{"hi", "there."}, Words(" [BLANK_AUDIO] hi  there.[Music]\n"))
	require.Empty(s.T(), Words("[BLANK_AUDIO]"))
}

func (s *SessionSuite) TestContinuation() {
	tests := []struct {
		name          string
		agreed, words string
		want          string
	}{
		{name: "nothing agreed", words: "a b", want: "a b"},
		{name: "same start", agreed: "a b", words: "a b c", want: "c"},
		{name: "shifted by a reworded start", agreed: "a b fails,", words: "a x b fails, fix", want: "fix"},
		{name: "case and punctuation", agreed: "Hello world.", words: "hello world, again", want: "again"},
		{name: "the nearest of equal matches", agreed: "go", words: "go go go", want: "go go"},
		{name: "the longest match", agreed: "a b c", words: "c x a b c d", want: "d"},
		{name: "no match falls back to position", agreed: "a b", words: "x y z", want: "z"},
		{name: "shorter than agreed", agreed: "a b c", words: "x", want: ""},
	}
	for _, tc := range tests {
		got := continuation(strings.Fields(tc.agreed), strings.Fields(tc.words))
		require.Equal(s.T(), tc.want, strings.Join(got, " "), tc.name)
	}
}

func (s *SessionSuite) TestStepWaitsForAudio() {
	ctx := context.Background()
	require.NoError(s.T(), s.sess.step(ctx), "nothing written")
	s.sess.Write(speech(minSamples - 1))
	require.NoError(s.T(), s.sess.step(ctx), "too short")
	require.Empty(s.T(), s.tr.calls)
	require.Empty(s.T(), s.emitted())
}

// Silence alone isn't transcribed; only its end is kept.
func (s *SessionSuite) TestStepDropsSilence() {
	s.sess.Write(silence(3 * SampleRate))
	require.NoError(s.T(), s.sess.step(context.Background()))
	require.Empty(s.T(), s.tr.calls)
	require.Len(s.T(), s.sess.audio, keepSilence)
	require.NoError(s.T(), s.sess.step(context.Background()), "no new audio")
	require.Empty(s.T(), s.tr.calls)
}

// Words two passes in a row agree on are committed; the rest is tentative.
func (s *SessionSuite) TestStepCommitsAgreedWords() {
	s.tr.texts = []string{" Hello world", " Hello world again", " Hello, world again friend"}
	for range 3 {
		s.sess.Write(speech(minSamples))
		require.NoError(s.T(), s.sess.step(context.Background()))
	}
	require.Equal(s.T(), []Event{
		partial("", "Hello world"),
		partial("Hello world", "again"),
		partial("Hello world again", "friend"),
	}, s.emitted())
	require.Equal(s.T(), []call{
		{samples: minSamples, language: "en"},
		{samples: 2 * minSamples, language: "en"},
		{samples: 3 * minSamples, language: "en"},
	}, s.tr.calls)
}

// A pause ends the segment: its text is committed, and audio that came in
// during the pass starts the next one, which has the text as its prompt.
func (s *SessionSuite) TestStepEndsSegmentAtPause() {
	s.tr.texts = []string{"one two", "three"}
	s.tr.during = func() { s.sess.Write(speech(100)) }
	s.sess.Write(append(speech(minSamples), silence(silenceEnd+frameLen)...))

	require.NoError(s.T(), s.sess.step(context.Background()))

	require.Equal(s.T(), []Event{partial("one two", "")}, s.emitted())
	require.Len(s.T(), s.sess.audio, 100)
	s.tr.during = nil
	s.sess.Write(speech(minSamples))
	require.NoError(s.T(), s.sess.step(context.Background()))
	require.Equal(s.T(), "one two", s.tr.calls[1].prompt)
	require.Equal(s.T(), partial("one two", "three"), s.emitted()[1])
}

func (s *SessionSuite) TestStepEndsLongSegment() {
	s.tr.texts = []string{"long"}
	s.sess.Write(speech(maxSegment))
	require.NoError(s.T(), s.sess.step(context.Background()))
	require.Equal(s.T(), []Event{partial("long", "")}, s.emitted())
	require.Empty(s.T(), s.sess.audio)
}

func (s *SessionSuite) TestPromptIsTheEndOfTheText() {
	words := make([]string, promptWords+10)
	for i := range words {
		words[i] = "w"
	}
	words[10] = "first"
	s.sess.done = words
	require.True(s.T(), strings.HasPrefix(s.sess.prompt(), "first "))
	require.Len(s.T(), strings.Fields(s.sess.prompt()), promptWords)
}

func (s *SessionSuite) TestStepError() {
	s.tr.err = errors.New("boom")
	s.sess.Write(speech(minSamples))
	require.EqualError(s.T(), s.sess.step(context.Background()), "boom")
}

func (s *SessionSuite) TestFinalize() {
	tests := []struct {
		name  string
		audio []int16
		texts []string
		err   error
		want  Event
		calls int
	}{
		{
			name:  "speech left",
			audio: speech(SampleRate),
			texts: []string{"agreed more"},
			want:  Event{Type: EventFinal, Text: "done agreed more"},
			calls: 1,
		},
		{
			name:  "only silence left",
			audio: silence(SampleRate),
			want:  Event{Type: EventFinal, Text: "done agreed"},
		},
		{
			name:  "transcription fails",
			audio: speech(SampleRate),
			err:   errors.New("gone"),
			want:  Event{Type: EventError, Code: CodeEngineFailed, Message: "gone"},
			calls: 1,
		},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			s.SetupTest()
			s.tr.texts, s.tr.err = tc.texts, tc.err
			s.sess.done, s.sess.agreed = []string{"done"}, []string{"agreed"}
			s.sess.Write(tc.audio)

			s.sess.finalize(context.Background())

			require.Equal(s.T(), []Event{tc.want}, s.emitted())
			require.Len(s.T(), s.tr.calls, tc.calls)
			if tc.calls > 0 {
				require.Equal(s.T(), "done", s.tr.calls[0].prompt)
			}
		})
	}
}

// run starts the session's Run loop, returning a channel closed when it ends.
func (s *SessionSuite) run(ctx context.Context) <-chan struct{} {
	s.sess.interval = time.Millisecond
	done := make(chan struct{})
	go func() {
		s.sess.Run(ctx)
		close(done)
	}()
	return done
}

func (s *SessionSuite) TestRunStreamsThenStops() {
	s.tr.texts = []string{"hello", "hello there"}
	done := s.run(context.Background())
	s.sess.Write(speech(minSamples))
	require.Eventually(s.T(), func() bool { return len(s.emitted()) == 1 }, testWait, testTick)

	s.sess.Write(speech(SampleRate))
	s.sess.Stop()
	s.sess.Stop() // twice is fine
	<-done

	events := s.emitted()
	require.Equal(s.T(), Event{Type: EventFinal, Text: "hello there"}, events[len(events)-1])
}

func (s *SessionSuite) TestRunCancel() {
	done := s.run(context.Background())
	s.sess.Write(speech(SampleRate))
	s.sess.Cancel()
	<-done
	require.Empty(s.T(), s.emitted())
	require.Empty(s.T(), s.tr.calls)
}

func (s *SessionSuite) TestRunContextEnds() {
	ctx, cancel := context.WithCancel(context.Background())
	done := s.run(ctx)
	cancel()
	<-done
	require.Empty(s.T(), s.emitted())
}

func (s *SessionSuite) TestRunError() {
	s.tr.err = errors.New("crashed")
	done := s.run(context.Background())
	s.sess.Write(speech(minSamples))
	<-done
	require.Equal(s.T(), []Event{{Type: EventError, Code: CodeEngineFailed, Message: "crashed"}}, s.emitted())
}
