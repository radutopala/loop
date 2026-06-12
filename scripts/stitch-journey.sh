#!/bin/bash
#
# Stitch the per-section docs clips (docs/videos/NN_<section>.mp4) into a single
# journey.mp4 and lay a fully-synthesised chillout track under it. Sourced by
# scripts/test-component.sh, so a full `make docs-capture` stitches + scores the
# journey at the end of the run. Also runnable directly
# (`bash scripts/stitch-journey.sh`) to rebuild journey.mp4 from whatever section
# clips already exist — handy when a capture run failed late and skipped it.

# Colours (only set if not already defined by a sourcing script).
: "${RED:=$'\033[0;31m'}"
: "${GREEN:=$'\033[0;32m'}"
: "${YELLOW:=$'\033[1;33m'}"
: "${NC:=$'\033[0m'}"

# Locate a General-MIDI soundfont for FluidSynth. The test-runner image installs
# fluid-soundfont-gm (FluidR3_GM.sf2); fall back to other common names/locations
# so a direct run on a dev box with a different soundfont still works.
find_soundfont() {
    local c
    for c in \
        /usr/share/sounds/sf2/FluidR3_GM.sf2 \
        /usr/share/sounds/sf2/default-GM.sf2 \
        /usr/share/sounds/sf3/default-GM.sf3 \
        /usr/share/soundfonts/FluidR3_GM.sf2 \
        /usr/share/soundfonts/default.sf2; do
        [ -f "$c" ] && { echo "$c"; return 0; }
    done
    # Last resort: first *.sf2/*.sf3 anywhere under the usual sound dirs.
    c="$(find /usr/share/sounds /usr/share/soundfonts -iname '*.sf2' -o -iname '*.sf3' 2>/dev/null | head -1)"
    [ -n "$c" ] && { echo "$c"; return 0; }
    return 1
}

# Generate a chillout backing loop into $1 (a .wav) using a real instrument
# renderer: a synthesised ii–V MIDI (scripts/gen-chillout-midi.py — warm pad +
# Rhodes comp/arp + walking acoustic bass + soft drums over Cmaj7–Am7–Dm7–G7) is
# rendered by FluidSynth through a General-MIDI soundfont, then gently EQ'd and
# loudness-normalised. The audio is synthesised from note data (no sample, no
# recording), so there is nothing for YouTube Content ID to match, but it sounds
# like real instruments rather than raw sines. Falls back to the legacy pure-sine
# generator if FluidSynth/python/a soundfont are unavailable; returns non-zero
# (and the caller leaves the journey silent) only if even that fails.
generate_music() {
    local out="$1"
    local style="${2:-}"   # lofi (default) | lounge | ambient | jazz
    local sdir; sdir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local sf
    if command -v ffmpeg > /dev/null 2>&1 && command -v fluidsynth > /dev/null 2>&1 \
        && command -v go > /dev/null 2>&1 && sf="$(find_soundfont)"; then
        local tmp; tmp="$(mktemp -d)"
        # gen-chillout-midi.py prints the exact musical phrase length (seconds);
        # FluidSynth then rings the reverb out PAST that, leaving a silent tail.
        # Trim to the phrase length and fold the ring-out back onto the head so
        # the clip loops seamlessly (otherwise -stream_loop replays a few seconds
        # of silence between every repeat).
        local plen
        if plen="$(go run "$sdir/gen-chillout-midi.go" "$tmp/chillout.mid" "$style" 2>/dev/null)" \
            && [ -n "$plen" ] \
            && fluidsynth -ni -q -g 0.7 -r 44100 -F "$tmp/raw.wav" "$sf" "$tmp/chillout.mid" > /dev/null 2>&1 \
            && ffmpeg -y -i "$tmp/raw.wav" -filter_complex "\
                [0:a]atrim=0:$plen,asetpts=PTS-STARTPTS[head];\
                [0:a]atrim=$plen,asetpts=PTS-STARTPTS[tail];\
                [head][tail]amix=inputs=2:duration=first:normalize=0,\
                highpass=f=40,lowpass=f=12000,loudnorm=I=-16:TP=-1.5:LRA=11[a]" \
                -map "[a]" -ar 44100 -ac 2 "$out" > /dev/null 2>&1; then
            rm -rf "$tmp"
            echo -e "${GREEN}stitch: rendered chillout track via FluidSynth ($(basename "$sf"))${NC}"
            return 0
        fi
        rm -rf "$tmp"
        echo -e "${YELLOW}stitch: FluidSynth render failed; falling back to sine synth${NC}"
    fi
    generate_music_sine "$out"
}

# Legacy fallback: a chillout loop built from pure sine tones (no soundfont
# needed). Used only when FluidSynth/python/a soundfont are missing. Same musical
# idea (pad + walking bass + ascending arpeggio over Cmaj7–Am7–Dm7–G7) but with a
# more synthetic timbre.
generate_music_sine() {
    local out="$1"
    local tmp; tmp="$(mktemp -d)"
    local concat="$tmp/concat.txt"; : > "$concat"
    # One entry per 4s bar: "pad1 pad2 pad3 | bass | arp1 arp2 arp3 arp4" (Hz).
    # Pad = chord (octave 4), bass = root (octave 2-3), arp = ascending chord
    # tones (octave 5) played as four quarter-notes.
    local bars=(
        "261.63 329.63 392.00 | 130.81 | 523.25 659.25 783.99 987.77"   # Cmaj7
        "220.00 261.63 329.63 | 110.00 | 440.00 523.25 659.25 783.99"   # Am7
        "293.66 349.23 440.00 | 146.83 | 587.33 698.46 880.00 1046.50"  # Dm7
        "196.00 246.94 293.66 |  98.00 | 392.00 493.88 587.33 698.46"   # G7
    )
    local i=0
    for bar in "${bars[@]}"; do
        local pad bass arp
        pad="$(echo "$bar"  | cut -d'|' -f1)"
        bass="$(echo "$bar" | cut -d'|' -f2 | tr -d ' ')"
        arp="$(echo "$bar"  | cut -d'|' -f3)"
        # shellcheck disable=SC2086
        set -- $pad; local p1=$1 p2=$2 p3=$3
        # shellcheck disable=SC2086
        set -- $arp; local a1=$1 a2=$2 a3=$3 a4=$4
        if ! ffmpeg -y \
            -f lavfi -i "sine=frequency=$p1:duration=4" \
            -f lavfi -i "sine=frequency=$p2:duration=4" \
            -f lavfi -i "sine=frequency=$p3:duration=4" \
            -f lavfi -i "sine=frequency=$bass:duration=4" \
            -f lavfi -i "sine=frequency=$a1:duration=1" \
            -f lavfi -i "sine=frequency=$a2:duration=1" \
            -f lavfi -i "sine=frequency=$a3:duration=1" \
            -f lavfi -i "sine=frequency=$a4:duration=1" \
            -filter_complex "\
                [0]volume=0.09[p1];[1]volume=0.09[p2];[2]volume=0.09[p3];\
                [3]volume=0.22,lowpass=f=320[bs];\
                [4]volume=0.34,afade=t=in:d=0.02,afade=t=out:st=0.55:d=0.45,adelay=0|0[n1];\
                [5]volume=0.34,afade=t=in:d=0.02,afade=t=out:st=0.55:d=0.45,adelay=1000|1000[n2];\
                [6]volume=0.34,afade=t=in:d=0.02,afade=t=out:st=0.55:d=0.45,adelay=2000|2000[n3];\
                [7]volume=0.34,afade=t=in:d=0.02,afade=t=out:st=0.55:d=0.45,adelay=3000|3000[n4];\
                [p1][p2][p3][bs][n1][n2][n3][n4]amix=inputs=8:normalize=0:duration=longest,\
                afade=t=in:st=0:d=0.15,afade=t=out:st=3.75:d=0.25[a]" \
            -map "[a]" -t 4 -ar 44100 -ac 2 "$tmp/bar_$i.wav" > /dev/null 2>&1; then
            rm -rf "$tmp"; return 1
        fi
        echo "file 'bar_$i.wav'" >> "$concat"
        i=$((i + 1))
    done
    # Concatenate the four bars, then add a warm reverb tail + a gentle tremolo
    # and a low-pass to take the edge off the sines, and normalise to a
    # consistent loudness (the raw mix is quiet, so the later background mix
    # would otherwise be inaudible).
    if ! ffmpeg -y -f concat -safe 0 -i "$concat" \
        -af "aecho=0.8:0.85:90|180:0.35|0.18,lowpass=f=3000,tremolo=f=4:d=0.12,loudnorm=I=-16:TP=-1.5:LRA=11" \
        -ar 44100 -ac 2 "$out" > /dev/null 2>&1; then
        rm -rf "$tmp"; return 1
    fi
    rm -rf "$tmp"
    return 0
}

# Stitch the per-section docs clips into one journey.mp4. Each clip is named
# NN_<section>.mp4 (NN = feature order), so a plain lexical sort gives the
# journey order — a missing/failed section just leaves a numbering gap and is
# skipped.
stitch_journey() {
    local vdir="docs/videos"
    if ! command -v ffmpeg > /dev/null 2>&1; then
        echo -e "${YELLOW}stitch: ffmpeg not found; skipping journey.mp4${NC}"
        return 0
    fi
    local list="$vdir/.journey-concat.txt"
    : > "$list"
    local n=0
    for f in $(ls "$vdir"/[0-9][0-9]_*.mp4 2>/dev/null | sort); do
        echo "file '$(basename "$f")'" >> "$list"
        n=$((n + 1))
    done
    if [ "$n" -eq 0 ]; then
        echo -e "${YELLOW}stitch: no section clips found; skipping journey.mp4${NC}"
        rm -f "$list"
        return 0
    fi
    echo -e "${YELLOW}Stitching $n section clips into $vdir/journey.mp4...${NC}"
    # All section clips are encoded identically (libx264/yuv420p/30fps), so try a
    # fast stream-copy concat first; fall back to a re-encode if copy rejects it.
    if ! ffmpeg -y -f concat -safe 0 -i "$list" -c copy -movflags +faststart "$vdir/journey.mp4" > /dev/null 2>&1; then
        ffmpeg -y -f concat -safe 0 -i "$list" -vf "fps=30" -c:v libx264 -pix_fmt yuv420p -movflags +faststart "$vdir/journey.mp4" \
            || { echo -e "${RED}stitch: ffmpeg concat failed${NC}"; rm -f "$list"; return 1; }
    fi
    rm -f "$list"
    echo -e "${GREEN}Wrote $vdir/journey.mp4 ($n clips)${NC}"

    mux_journey_music "$vdir/journey.mp4"
}

# Lay the generated chillout soundtrack under a (silent) journey video in place.
# Used by stitch_journey (after concatenating section clips) and directly by the
# single-take @journey run (whose one continuous recording already IS journey.mp4,
# so it only needs scoring). The music loops seamlessly (see generate_music) under
# the full video length, with a 2s fade-in and a 3s fade-out at the tail.
mux_journey_music() {
    local video="$1"
    local dir; dir="$(dirname "$video")"
    if ! command -v ffmpeg > /dev/null 2>&1; then
        echo -e "${YELLOW}stitch: ffmpeg not found; leaving $video silent${NC}"
        return 0
    fi
    # Bail early if the video is missing/unreadable — e.g. a journey run that died
    # before `stop recording` leaves a truncated, moov-less file. No point
    # synthesising a soundtrack for a file we can't mux.
    local vdur
    vdur="$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$video" 2>/dev/null)"
    if [ -z "$vdur" ]; then
        echo -e "${YELLOW}stitch: $video missing or invalid (no duration); nothing to score${NC}"
        return 0
    fi
    local music="$dir/.journey-music.wav"
    # Journey soundtrack style: lofi (Rhodes + vibes lead + warm pad + walking
    # bass + soft swung kit). See scripts/gen-chillout-midi.py for the others
    # (lounge | ambient | jazz).
    if generate_music "$music" "lofi"; then
        local fout
        fout="$(awk "BEGIN{d=$vdur-3; if (d<0) d=0; print d}" 2>/dev/null)"
        if ffmpeg -y -i "$video" -stream_loop -1 -i "$music" \
            -filter_complex "[1:a]volume=0.6,afade=t=in:st=0:d=2,afade=t=out:st=$fout:d=3[a]" \
            -map 0:v -map "[a]" -shortest -c:v copy -c:a aac -b:a 128k -movflags +faststart "$dir/.journey-music.mp4" > /dev/null 2>&1; then
            mv "$dir/.journey-music.mp4" "$video"
            echo -e "${GREEN}Added generated background music to $video${NC}"
        else
            echo -e "${YELLOW}stitch: music mux skipped (kept silent $video)${NC}"
            rm -f "$dir/.journey-music.mp4"
        fi
        rm -f "$music"
    else
        echo -e "${YELLOW}stitch: music generation unavailable; $video left silent${NC}"
    fi
}

# When executed directly (not sourced), stitch immediately.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    stitch_journey
fi
