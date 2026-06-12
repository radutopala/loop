//go:build ignore

// gen-chillout-midi.go generates an original chillout MIDI loop for the docs
// journey soundtrack, in a choice of styles. It has no dependencies beyond the Go
// standard library and writes a Standard MIDI File by hand, which FluidSynth + a
// General-MIDI soundfont then renders to audio in scripts/journey-music.sh.
//
// All styles share an 8-bar A/B form in C major:
//
//	A: ii-V-I-vi  (Dm9 - G13 - Cmaj9 - Am9)
//	B: IV-iii-ii-V (Fmaj9 - Em7 - Dm9 - G13)
//
// played by some subset of: a warm sustained pad, rootless Rhodes/piano comping,
// a bass (walking or sustained), a lead melody, and drums. The style picks tempo,
// swing, instruments, comping/drum feel and density:
//
//	lofi    ~74 BPM  swung     Rhodes + vibes lead + warm pad + walking bass + lofi kit
//	lounge  ~96 BPM  light     piano + vibes lead + pad + walking bass + brushed kit
//	ambient ~58 BPM  straight  big pad + sparse long vibes + sustained bass, NO drums
//	jazz   ~122 BPM  hard      piano comping + sax lead + walking bass + swing ride
//
// Timing and velocity are humanised from a FIXED seed, so output is musical but
// fully deterministic (reproducible builds + a stable seamless-loop trim). The
// whole form is rendered twice so the loop is less repetitive. Because it is
// synthesised from note data (no sample, no recording), there is nothing for
// YouTube Content ID to match.
//
// Usage: go run scripts/gen-chillout-midi.go <out.mid> [style]
// (style default: lofi; prints the loop length in seconds on stdout)
package main

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"os"
	"sort"
)

const (
	tpq      = 480 // ticks per quarter note
	beat     = tpq
	bar      = 4 * beat
	numIters = 2
)

// General-MIDI programs (0-indexed wire values).
const (
	grand   = 0
	rhodes  = 4
	vibes   = 11
	padWarm = 89
	padNew  = 88
	sax     = 66
)

// Drum channel + GM percussion note numbers.
const (
	drums     = 9
	kick      = 36
	snare     = 38
	sideStick = 37
	clHat     = 42
	pedHat    = 44
	opHat     = 46
	ride      = 51
)

var rng = rand.New(rand.NewSource(20260612))

var names = map[string]int{"C": 0, "D": 2, "E": 4, "F": 5, "G": 7, "A": 9, "B": 11}

var qual = map[string][]int{
	"maj9": {0, 4, 7, 11, 14},
	"m9":   {0, 3, 7, 10, 14},
	"m7":   {0, 3, 7, 10},
	"13":   {0, 4, 10, 14, 21}, // dominant: root, 3, b7, 9, 13
}

type chord struct{ name, q string }

// 8-bar A/B progression in C major.
var prog = []chord{
	{"D", "m9"}, {"G", "13"}, {"C", "maj9"}, {"A", "m9"}, // A: ii V I vi
	{"F", "maj9"}, {"E", "m7"}, {"D", "m9"}, {"G", "13"}, // B: IV iii ii V
}

type mnote struct {
	pitch      int
	start, dur float64 // in beats
}

// Vibraphone/sax melody per bar (octave 5-6), drawn from chord/scale tones.
var melody = [][]mnote{
	{{81, 0, 1.0}, {77, 2, 1.5}},
	{{79, 0, 1.0}, {83, 2.5, 0.5}, {81, 3, 1.0}},
	{{76, 0, 1.5}, {74, 2.5, 1.0}},
	{{84, 0, 1.0}, {83, 1, 1.0}, {81, 2, 2.0}},
	{{81, 0, 1.0}, {79, 2.5, 0.5}, {77, 3, 1.0}},
	{{79, 0, 1.5}, {76, 2.5, 1.0}},
	{{77, 0, 1.0}, {76, 1.5, 0.5}, {74, 2, 1.5}},
	{{74, 0, 1.0}, {79, 2.5, 0.5}, {77, 3, 1.0}},
}

type style struct {
	bpm                          int
	swing                        float64
	compProg, lead, pad, bass    int // pad = -1 means none
	comp, drums, bassMode, melMd string
}

var styles = map[string]style{
	"lofi":    {74, 0.60, rhodes, vibes, padWarm, 32, "lofi", "lofi", "walk", "main"},
	"lounge":  {96, 0.57, grand, vibes, padWarm, 32, "lounge", "brushes", "walk", "main"},
	"ambient": {58, 0.50, rhodes, vibes, padNew, 32, "none", "none", "sustain", "sparse"},
	"jazz":    {122, 0.66, grand, sax, -1, 32, "jazz", "swing", "walk", "main"},
}

type event struct {
	tick, prio int
	data       []byte
}

var events []event

func add(tick, prio int, data ...byte) {
	if tick < 0 {
		tick = 0
	}
	events = append(events, event{tick, prio, data})
}

func note(ch, pitch, vel, start, dur int) {
	if vel < 1 {
		vel = 1
	} else if vel > 127 {
		vel = 127
	}
	add(start, 1, byte(0x90|ch), byte(pitch), byte(vel))
	// Note-off shares a tick with any re-strike's note-on; prio 0 fires it first.
	add(start+dur, 0, byte(0x80|ch), byte(pitch), 0)
}

func htime(t, amt int) int {
	v := t + rng.Intn(2*amt+1) - amt
	if v < 0 {
		return 0
	}
	return v
}

func hvel(v, amt int) int { return v + rng.Intn(2*amt+1) - amt }

func chordTones(name, q string, base int) []int {
	out := make([]int, 0, len(qual[q]))
	for _, iv := range qual[q] {
		out = append(out, base+names[name]+iv)
	}
	return out
}

func vlq(n int) []byte {
	out := []byte{byte(n & 0x7f)}
	n >>= 7
	for n > 0 {
		out = append([]byte{byte((n & 0x7f) | 0x80)}, out...)
		n >>= 7
	}
	return out
}

func main() {
	out := "chillout.mid"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	st := "lofi"
	if len(os.Args) > 2 && os.Args[2] != "" {
		st = os.Args[2]
	}
	cfg, ok := styles[st]
	if !ok {
		cfg = styles["lofi"]
	}

	eighthTick := func(barStart, e int) int {
		b, half := e/2, e%2
		if half == 0 {
			return barStart + b*beat
		}
		return barStart + b*beat + int(cfg.swing*float64(beat))
	}

	// Program changes.
	add(0, 0, 0xC0, byte(cfg.compProg))
	add(0, 0, 0xC1, byte(cfg.bass))
	if cfg.pad >= 0 {
		add(0, 0, 0xC2, byte(cfg.pad))
	}
	add(0, 0, 0xC3, byte(cfg.lead))

	t := 0
	for range numIters {
		for bi, c := range prog {
			b := t
			nextName := prog[(bi+1)%len(prog)].name

			// Pad.
			if cfg.pad >= 0 {
				for _, p := range chordTones(c.name, c.q, 48)[:4] {
					note(2, p, hvel(34, 8), b, bar-20)
				}
			}

			// Comp.
			comp := chordTones(c.name, c.q, 60)[1:] // rootless
			switch cfg.comp {
			case "lofi":
				for _, p := range comp {
					note(0, p, hvel(48, 8), htime(b, 12), beat+beat/2)
				}
				for _, p := range comp {
					note(0, p, hvel(40, 8), htime(b+3*beat/2, 12), beat)
				}
			case "lounge":
				for _, p := range comp {
					note(0, p, hvel(46, 8), htime(b, 12), beat*2-20)
				}
				for _, p := range comp {
					note(0, p, hvel(42, 8), htime(b+2*beat, 12), beat*2-20)
				}
			case "jazz":
				for _, p := range comp {
					note(0, p, hvel(50, 8), htime(b, 12), beat/2)
				}
				for _, p := range comp {
					note(0, p, hvel(44, 8), eighthTick(b, 3), beat/2)
				}
				for _, p := range comp {
					note(0, p, hvel(46, 8), htime(b+3*beat, 12), beat/2)
				}
			}

			// Bass.
			root := 36 + names[c.name]
			if cfg.bassMode == "walk" {
				walk := []int{root, root + 7, root + 12, 36 + names[nextName] - 1}
				for i, bn := range walk {
					note(1, bn, hvel(64, 8), htime(b+i*beat, 12), beat-30)
				}
			} else { // sustain (ambient)
				note(1, root, hvel(42, 8), b, bar-20)
				note(1, root+7, hvel(34, 8), b, bar-20)
			}

			// Lead melody.
			switch cfg.melMd {
			case "main":
				for _, m := range melody[bi] {
					note(3, m.pitch, hvel(66, 8), htime(b+int(m.start*float64(beat)), 8), int(m.dur*float64(beat))-20)
				}
			case "sparse":
				m := melody[bi][0]
				note(3, m.pitch, hvel(56, 8), htime(b+beat/4, 8), bar-beat)
			}

			// Drums.
			switch cfg.drums {
			case "lofi":
				for e := range 8 {
					v := 38
					if e%2 != 0 {
						v = 30
					}
					note(drums, clHat, hvel(v, 5), eighthTick(b, e), 30)
				}
				note(drums, kick, hvel(70, 8), htime(b, 12), 50)
				note(drums, kick, hvel(58, 8), eighthTick(b, 5), 50)
				note(drums, sideStick, hvel(52, 8), htime(b+beat, 12), 40)
				note(drums, snare, hvel(50, 8), htime(b+3*beat, 12), 40)
			case "brushes":
				for e := range 8 {
					v := 30
					if e%2 != 0 {
						v = 24
					}
					note(drums, clHat, hvel(v, 4), eighthTick(b, e), 30)
				}
				note(drums, kick, hvel(58, 8), htime(b, 12), 50)
				note(drums, kick, hvel(48, 8), htime(b+2*beat, 12), 50)
				note(drums, snare, hvel(44, 8), htime(b+beat, 12), 40)
				note(drums, snare, hvel(46, 8), htime(b+3*beat, 12), 40)
			case "swing":
				for bt := range 4 {
					v := 46
					if bt%2 != 0 {
						v = 40
					}
					note(drums, ride, hvel(v, 8), htime(b+bt*beat, 12), 40)
				}
				note(drums, ride, hvel(38, 8), eighthTick(b, 3), 40)
				note(drums, ride, hvel(38, 8), eighthTick(b, 7), 40)
				note(drums, pedHat, hvel(40, 8), htime(b+beat, 12), 30)
				note(drums, pedHat, hvel(40, 8), htime(b+3*beat, 12), 30)
				if rng.Float64() < 0.5 {
					e := 5
					if rng.Intn(2) == 1 {
						e = 6
					}
					note(drums, snare, hvel(34, 8), eighthTick(b, e), 30)
				}
			}

			// End-of-form fill (skip for the drumless ambient style).
			if bi == len(prog)-1 && cfg.drums != "none" {
				for k := range 4 {
					note(drums, snare, hvel(44+k*4, 8), b+3*beat+k*(beat/4), 30)
				}
				note(drums, opHat, hvel(40, 8), b+4*beat-beat/8, 60)
			}

			t += bar
		}
	}
	endTick := t

	// Serialise a format-0 Standard MIDI File.
	track := []byte{}
	track = append(track, vlq(0)...)
	mpqn := 60000000 / cfg.bpm
	track = append(track, 0xFF, 0x51, 0x03, byte(mpqn>>16), byte(mpqn>>8), byte(mpqn)) // tempo

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].tick != events[j].tick {
			return events[i].tick < events[j].tick
		}
		return events[i].prio < events[j].prio
	})
	prev := 0
	for _, e := range events {
		track = append(track, vlq(e.tick-prev)...)
		track = append(track, e.data...)
		prev = e.tick
	}
	track = append(track, vlq(endTick-prev)...)
	track = append(track, 0xFF, 0x2F, 0x00) // end of track

	var buf []byte
	buf = append(buf, []byte("MThd")...)
	buf = binary.BigEndian.AppendUint32(buf, 6)
	buf = binary.BigEndian.AppendUint16(buf, 0)   // format 0
	buf = binary.BigEndian.AppendUint16(buf, 1)   // 1 track
	buf = binary.BigEndian.AppendUint16(buf, tpq) // division
	buf = append(buf, []byte("MTrk")...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(track)))
	buf = append(buf, track...)

	if err := os.WriteFile(out, buf, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "write:", err)
		os.Exit(1)
	}

	// Loop length (seconds) so the renderer can trim the reverb tail and fold it
	// back for a seamless loop.
	fmt.Printf("%.3f\n", float64(endTick)/float64(tpq)*60.0/float64(cfg.bpm))
}
