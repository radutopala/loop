#!/usr/bin/env bash
# Starts a built whisper-server, transcribes one WAV through POST /inference
# and checks for HTTP 200 and a JSON "text" containing an expected word.
#
#   scripts/smoke-whisper-server.sh --bin DIR --model FILE --audio FILE [--expect WORD] [--port N] [--docker IMAGE]
#
# DIR holds whisper-server (or whisper-server.exe) and any libraries next to it.
# --docker runs the server inside a container of IMAGE (Linux builds), sharing
# the host network, to check it on another distro.
# The server's log (backend selection, timings) is printed at the end.
set -euo pipefail

BIN="" MODEL="" AUDIO="" EXPECT="" PORT=18765 IMAGE=""
while [ $# -gt 0 ]; do
	case "$1" in
		--bin) BIN="$2"; shift 2 ;;
		--model) MODEL="$2"; shift 2 ;;
		--audio) AUDIO="$2"; shift 2 ;;
		--expect) EXPECT="$2"; shift 2 ;;
		--port) PORT="$2"; shift 2 ;;
		--docker) IMAGE="$2"; shift 2 ;;
		*) echo "smoke-whisper-server: unknown argument $1" >&2; exit 2 ;;
	esac
done
[ -n "$BIN" ] && [ -n "$MODEL" ] && [ -n "$AUDIO" ] || { echo "usage: $0 --bin DIR --model FILE --audio FILE [--expect WORD]" >&2; exit 2; }

EXE="$BIN/whisper-server"
[ -f "$EXE.exe" ] && EXE="$EXE.exe"
[ -f "$EXE" ] || { echo "smoke-whisper-server: no whisper-server in $BIN" >&2; exit 1; }

LOG="$(mktemp)"
RESP="$(mktemp)"
CONTAINER=""
if [ -n "$IMAGE" ]; then
	CONTAINER="whisper-smoke-$$"
	docker run --rm --name "$CONTAINER" --network host \
		-v "$(cd "$BIN" && pwd):/whisper:ro" \
		-v "$(cd "$(dirname "$MODEL")" && pwd)/$(basename "$MODEL"):/model.bin:ro" \
		"$IMAGE" /whisper/whisper-server -m /model.bin --host 127.0.0.1 --port "$PORT" -t 2 -bs 1 -bo 1 >"$LOG" 2>&1 &
else
	"$EXE" -m "$MODEL" --host 127.0.0.1 --port "$PORT" -t 2 -bs 1 -bo 1 >"$LOG" 2>&1 &
fi
PID=$!
cleanup() {
	[ -n "$CONTAINER" ] && docker rm -f "$CONTAINER" >/dev/null 2>&1
	kill "$PID" 2>/dev/null || true
	wait "$PID" 2>/dev/null || true
	echo "--- whisper-server log ---"
	cat "$LOG"
	rm -f "$LOG" "$RESP"
}
trap cleanup EXIT

# Wait for the listener (model load included), failing early if it exits.
ready=0
for _ in $(seq 1 300); do
	if ! kill -0 "$PID" 2>/dev/null; then
		echo "smoke-whisper-server: whisper-server exited during startup" >&2
		exit 1
	fi
	if curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$PORT/"; then
		ready=1
		break
	fi
	sleep 1
done
[ "$ready" = 1 ] || { echo "smoke-whisper-server: whisper-server did not start listening" >&2; exit 1; }

code="$(curl -s -o "$RESP" -w '%{http_code}' --max-time 300 \
	-F "file=@$AUDIO" -F language=en -F temperature=0 -F response_format=json \
	"http://127.0.0.1:$PORT/inference")"
echo "HTTP $code: $(cat "$RESP")"
[ "$code" = 200 ] || { echo "smoke-whisper-server: expected HTTP 200" >&2; exit 1; }
grep -Eq '"text"[[:space:]]*:[[:space:]]*"[^"]*[[:alnum:]]' "$RESP" || { echo "smoke-whisper-server: empty text" >&2; exit 1; }
if [ -n "$EXPECT" ]; then
	grep -qi "$EXPECT" "$RESP" || { echo "smoke-whisper-server: text lacks '$EXPECT'" >&2; exit 1; }
fi
echo "smoke-whisper-server: ok"
