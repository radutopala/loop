#!/bin/bash
#
# Generate the chillout soundtrack for the docs journey and lay it under the
# (silent) screen recording. Sourced by scripts/test-component.sh, so a
# `make docs-journey` run scores docs/videos/journey.mp4 at the end. Also runnable
# directly (`bash scripts/journey-music.sh`) to re-score an existing journey.mp4.

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

# Generate a chillout backing loop into $1 (a .wav), style $2 (default lofi). A
# synthesised MIDI (scripts/gen-chillout-midi.go) is rendered by FluidSynth through
# a General-MIDI soundfont, then gently EQ'd and loudness-normalised. The audio is
# synthesised from note data (no sample, no recording), so there is nothing for
# YouTube Content ID to match, yet it sounds like real instruments. Returns
# non-zero (and the caller leaves the journey silent) if FluidSynth / go / a
# soundfont are unavailable.
generate_music() {
    local out="$1"
    local style="${2:-}"   # lofi (default) | lounge | ambient | jazz
    local sdir; sdir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local sf
    if ! command -v ffmpeg > /dev/null 2>&1 || ! command -v fluidsynth > /dev/null 2>&1 \
        || ! command -v go > /dev/null 2>&1 || ! sf="$(find_soundfont)"; then
        return 1
    fi
    local tmp; tmp="$(mktemp -d)"
    # gen-chillout-midi.go prints the exact musical phrase length (seconds);
    # FluidSynth then rings the reverb out PAST that, leaving a silent tail. Trim
    # to the phrase length and fold the ring-out back onto the head so the clip
    # loops seamlessly (otherwise -stream_loop replays silence between repeats).
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
        echo -e "${GREEN}music: rendered chillout track via FluidSynth ($(basename "$sf"))${NC}"
        return 0
    fi
    rm -rf "$tmp"
    echo -e "${YELLOW}music: FluidSynth render failed; journey left silent${NC}"
    return 1
}

# Lay the generated chillout soundtrack under a (silent) journey video in place.
# The single-take @journey run records one continuous docs/videos/journey.mp4, so
# it only needs scoring. The music loops seamlessly (see generate_music) under the
# full video length, with a 2s fade-in and a 3s fade-out at the tail.
mux_journey_music() {
    local video="$1"
    local dir; dir="$(dirname "$video")"
    if ! command -v ffmpeg > /dev/null 2>&1; then
        echo -e "${YELLOW}music: ffmpeg not found; leaving $video silent${NC}"
        return 0
    fi
    # Bail early if the video is missing/unreadable — e.g. a journey run that died
    # before `stop recording` leaves a truncated, moov-less file. No point
    # synthesising a soundtrack for a file we can't mux.
    local vdur
    vdur="$(ffprobe -v error -show_entries format=duration -of csv=p=0 "$video" 2>/dev/null)"
    if [ -z "$vdur" ]; then
        echo -e "${YELLOW}music: $video missing or invalid (no duration); nothing to score${NC}"
        return 0
    fi
    local music="$dir/.journey-music.wav"
    # Soundtrack style: lofi (Rhodes + vibes lead + warm pad + walking bass + soft
    # swung kit). See scripts/gen-chillout-midi.go for the others (lounge|ambient|jazz).
    if generate_music "$music" "lofi"; then
        local fout
        fout="$(awk "BEGIN{d=$vdur-3; if (d<0) d=0; print d}" 2>/dev/null)"
        if ffmpeg -y -i "$video" -stream_loop -1 -i "$music" \
            -filter_complex "[1:a]volume=0.6,afade=t=in:st=0:d=2,afade=t=out:st=$fout:d=3[a]" \
            -map 0:v -map "[a]" -shortest -c:v copy -c:a aac -b:a 128k -movflags +faststart "$dir/.journey-music.mp4" > /dev/null 2>&1; then
            mv "$dir/.journey-music.mp4" "$video"
            echo -e "${GREEN}Added generated background music to $video${NC}"
        else
            echo -e "${YELLOW}music: mux skipped (kept silent $video)${NC}"
            rm -f "$dir/.journey-music.mp4"
        fi
        rm -f "$music"
    else
        echo -e "${YELLOW}music: generation unavailable; $video left silent${NC}"
    fi
}

# When executed directly (not sourced), re-score the existing journey video.
if [ "${BASH_SOURCE[0]}" = "$0" ]; then
    mux_journey_music "docs/videos/journey.mp4"
fi
