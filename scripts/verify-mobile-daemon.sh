#!/usr/bin/env bash
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

usage() {
    echo "Usage: $0 {start|status|stop} --device <udid> [--allow-physical]"
    exit "${1:-1}"
}

[ $# -lt 1 ] && usage 1
SUBCOMMAND="$1"
shift

case "$SUBCOMMAND" in
    start|status|stop) ;;
    -h|--help) usage 0 ;;
    *) echo "ERROR: Unknown subcommand: $SUBCOMMAND" >&2; usage 1 ;;
esac

DEVICE=""
ALLOW_PHYSICAL=false

while [ $# -gt 0 ]; do
    case "$1" in
        --device)
            [ -z "${2:-}" ] && { echo "ERROR: --device requires a device identifier" >&2; exit 1; }
            DEVICE="$2"
            shift 2
            ;;
        --allow-physical)
            ALLOW_PHYSICAL=true
            shift
            ;;
        -h|--help)
            usage 0
            ;;
        *)
            echo "ERROR: Unknown option: $1" >&2
            usage 1
            ;;
    esac
done

[ -z "$DEVICE" ] && { echo "ERROR: --device is required" >&2; exit 1; }
[[ ! "$DEVICE" =~ ^[A-Za-z0-9._:-]+$ ]] && { echo "ERROR: Invalid device serial: '$DEVICE'" >&2; exit 1; }

STATE_DIR="${TMPDIR:-/tmp}/ackbar-mobile-n9-${DEVICE}"

pick_free_port() {
    python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1]); s.close()'
}

cmd_start() {
    if [ "$ALLOW_PHYSICAL" = false ] && [[ "$DEVICE" != emulator-* ]]; then
        echo "ERROR: Refusing non-emulator device serial '$DEVICE'. Pass --allow-physical to override." >&2
        exit 1
    fi

    mkdir -p "$STATE_DIR" && chmod 700 "$STATE_DIR"

    if [ -f "$STATE_DIR/ackbard.pid" ] && [ -f "$STATE_DIR/ackbard.port" ]; then
        OLD_PID="$(tr -d '[:space:]' < "$STATE_DIR/ackbard.pid")"
        OLD_PORT="$(tr -d '[:space:]' < "$STATE_DIR/ackbard.port")"
        if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null && curl -fsS "http://127.0.0.1:${OLD_PORT}/healthz" >/dev/null 2>&1; then
            adb -s "$DEVICE" reverse "tcp:${OLD_PORT}" "tcp:${OLD_PORT}" >/dev/null
            echo "ACKBAR_VERIFY_URL=http://127.0.0.1:${OLD_PORT}"
            echo "ACKBAR_VERIFY_STATE_DIR=${STATE_DIR}"
            exit 0
        fi
        rm -f "$STATE_DIR/ackbard.pid" "$STATE_DIR/ackbard.port"
    fi

    mkdir -p "$STATE_DIR/bin" "$STATE_DIR/home/.config/ackbar" "$STATE_DIR/logs"
    (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -o "$STATE_DIR/bin/ackbard" ./cmd/ackbard)

    PORT="$(pick_free_port)"
    while [ "$PORT" = "7777" ]; do PORT="$(pick_free_port)"; done

    HOME="$STATE_DIR/home" XDG_CONFIG_HOME="$STATE_DIR/home/.config" ACKBAR_DB="$STATE_DIR/ackbard.db" \
    ACKBAR_MIC_BRIDGE_ADDR="127.0.0.1:0" ACKBAR_TOKEN="" ACKBAR_RELAY_URL="" ACKBAR_RELAY_SECRET="" ACKBAR_NTFY_TOPIC="" \
    "$STATE_DIR/bin/ackbard" --host 127.0.0.1 --port "$PORT" --db "$STATE_DIR/ackbard.db" \
        --log-dir "$STATE_DIR/logs" --host-name "mobile-n9-${DEVICE}" < /dev/null > "$STATE_DIR/ackbard.out" 2>&1 &
    PID=$!
    echo "$PID" > "$STATE_DIR/ackbard.pid"
    echo "$PORT" > "$STATE_DIR/ackbard.port"

    ready=false
    for _ in $(seq 1 50); do
        kill -0 "$PID" 2>/dev/null || break
        if curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
            ready=true; break
        fi
        sleep 0.2
    done

    if [ "$ready" != true ]; then
        echo "ERROR: ackbard failed to become ready on port $PORT" >&2
        [ -f "$STATE_DIR/ackbard.out" ] && tail -n 20 "$STATE_DIR/ackbard.out" >&2
        kill "$PID" 2>/dev/null || true
        rm -f "$STATE_DIR/ackbard.pid" "$STATE_DIR/ackbard.port"
        exit 1
    fi

    if ! adb -s "$DEVICE" reverse "tcp:${PORT}" "tcp:${PORT}" >/dev/null; then
        echo "ERROR: adb reverse failed for $DEVICE on port $PORT" >&2
        kill "$PID" 2>/dev/null || true
        rm -f "$STATE_DIR/ackbard.pid" "$STATE_DIR/ackbard.port"
        exit 1
    fi

    echo "ACKBAR_VERIFY_URL=http://127.0.0.1:${PORT}"
    echo "ACKBAR_VERIFY_STATE_DIR=${STATE_DIR}"
}

cmd_status() {
    if [ ! -f "$STATE_DIR/ackbard.pid" ] || [ ! -f "$STATE_DIR/ackbard.port" ]; then
        echo "STATUS=stopped" >&2; exit 1
    fi
    PID="$(tr -d '[:space:]' < "$STATE_DIR/ackbard.pid")"
    PORT="$(tr -d '[:space:]' < "$STATE_DIR/ackbard.port")"
    if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null && curl -fsS "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1; then
        echo "STATUS=running"
        echo "PID=${PID}"
        echo "ACKBAR_VERIFY_URL=http://127.0.0.1:${PORT}"
        echo "ACKBAR_VERIFY_STATE_DIR=${STATE_DIR}"
        exit 0
    fi
    echo "STATUS=stopped" >&2
    exit 1
}

cmd_stop() {
    if [ -f "$STATE_DIR/ackbard.pid" ]; then
        PID="$(tr -d '[:space:]' < "$STATE_DIR/ackbard.pid")"
        if [ -n "$PID" ] && kill -0 "$PID" 2>/dev/null; then
            if ps -p "$PID" -o args= 2>/dev/null | grep -q "ackbard"; then
                kill "$PID" 2>/dev/null || true
                for _ in $(seq 1 15); do
                    kill -0 "$PID" 2>/dev/null || break
                    sleep 0.2
                done
                kill -0 "$PID" 2>/dev/null && kill -9 "$PID" 2>/dev/null || true
            fi
        fi
    fi
    if [ -f "$STATE_DIR/ackbard.port" ]; then
        PORT="$(tr -d '[:space:]' < "$STATE_DIR/ackbard.port")"
        [ -n "$PORT" ] && adb -s "$DEVICE" reverse --remove "tcp:${PORT}" >/dev/null 2>&1 || true
    fi
    [[ "$STATE_DIR" == */ackbar-mobile-n9-* ]] && rm -rf "$STATE_DIR"
    echo "Stopped mobile N9 daemon for ${DEVICE}"
}

case "$SUBCOMMAND" in
    start) cmd_start ;;
    status) cmd_status ;;
    stop) cmd_stop ;;
esac
