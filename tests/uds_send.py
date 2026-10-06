#!/usr/bin/env python3
"""
Manual UDS test tool — send JSON messages to the Mafuyu Unix domain socket.

Room IDs must be exactly 5 digits (e.g. "01231").

Usage examples:

  # Send a room message using the default socket path
  python tests/uds_send.py room --channel test_bot --id 00101 --msg "hello" \
      --name Alice --source qq --qq 12345678 --nickname Alice

  # Send a room with X (Twitter) info
  python tests/uds_send.py room --channel test_bot --id 00102 --msg "hi" \
      --name "@alice" --source x --tid "1234567890" --username "@alice" \
      --screen-name "Alice" --avatar ""

  # Send a roomSkills message
  python tests/uds_send.py skills --channel test_bot --id 00101 \
      --data 100 200 150 300

  # Use a custom socket path
  python tests/uds_send.py --socket /tmp/my_uds room --channel ch --id 00103 \
      --msg hello --name n --source qq --qq 0 --nickname n

  # Raw mode: send arbitrary JSON from stdin
  echo '{"channel":"ch","data":[{"time":1700000000,"id":"00101","msg":"m","name":"n","source":"qq","info":{"qq":1,"nickname":"n","avatar":""}}]}' \
      | python tests/uds_send.py raw
"""

import argparse
import json
import socket
import sys
import time


DEFAULT_SOCKET = "/tmp/pjsk_server_uds"


def send(sock_path: str, payload: dict) -> None:
    line = json.dumps(payload, ensure_ascii=False) + "\n"
    with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as s:
        s.connect(sock_path)
        s.sendall(line.encode())
    print(f"Sent to {sock_path}:")
    print(json.dumps(payload, indent=2, ensure_ascii=False))


def cmd_room(args) -> None:
    if args.source == "qq" or args.qq is not None:
        info = {
            "qq": args.qq,
            "nickname": args.nickname or "",
            "avatar": args.avatar or "",
        }
    else:
        info = {
            "tid": args.tid or "",
            "userName": args.username or "",
            "screenName": args.screen_name or "",
            "avatar": args.avatar or "",
        }

    item = {
        "time": args.time or int(time.time()),
        "id": args.id,
        "msg": args.msg,
        "name": args.name,
        "source": args.source,
        "info": info,
    }
    payload = {"channel": args.channel, "data": [item]}
    send(args.socket, payload)


def cmd_skills(args) -> None:
    item = {
        "id": args.id,
        "time": args.time or int(time.time()),
        "data": args.data,
    }
    payload = {"type": "roomSkills", "channel": args.channel, "data": [item]}
    send(args.socket, payload)


def cmd_raw(args) -> None:
    raw = sys.stdin.read().strip()
    payload = json.loads(raw)
    send(args.socket, payload)


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Send JSON messages to the Mafuyu UDS socket.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=__doc__,
    )
    parser.add_argument(
        "--socket",
        default=DEFAULT_SOCKET,
        metavar="PATH",
        help=f"UDS socket path (default: {DEFAULT_SOCKET})",
    )

    sub = parser.add_subparsers(dest="cmd", required=True)

    # ── room ──────────────────────────────────────────────────────────────────
    p_room = sub.add_parser("room", help="Submit a room entry")
    p_room.add_argument("--channel", required=True, help="Channel name (e.g. test_bot)")
    p_room.add_argument("--id", required=True, help="Room ID")
    p_room.add_argument("--msg", required=True, help="Room message / description")
    p_room.add_argument("--name", required=True, help="Submitter display name")
    p_room.add_argument("--source", required=True, help="Platform (qq / x / twi / …)")
    p_room.add_argument("--time", type=int, default=None, help="Unix timestamp (default: now)")
    # QQ info
    p_room.add_argument("--qq", type=int, default=None, help="QQ number (for QQ rooms)")
    p_room.add_argument("--nickname", default="", help="QQ nickname")
    # X/Twitter info
    p_room.add_argument("--tid", default="", help="Tweet/X ID (for X rooms)")
    p_room.add_argument("--username", default="", help="@username on X")
    p_room.add_argument("--screen-name", default="", help="Screen name on X")
    # Shared
    p_room.add_argument("--avatar", default="", help="Avatar URL")
    p_room.set_defaults(func=cmd_room)

    # ── skills ─────────────────────────────────────────────────────────────────
    p_skills = sub.add_parser("skills", help="Submit a roomSkills entry")
    p_skills.add_argument("--channel", required=True, help="Channel name")
    p_skills.add_argument("--id", required=True, help="Room ID to attach skills data to")
    p_skills.add_argument("--time", type=int, default=None, help="Unix timestamp (default: now)")
    p_skills.add_argument("--data", nargs="+", type=int, required=True, help="Integer data values")
    p_skills.set_defaults(func=cmd_skills)

    # ── raw ───────────────────────────────────────────────────────────────────
    p_raw = sub.add_parser("raw", help="Send raw JSON from stdin (one line)")
    p_raw.set_defaults(func=cmd_raw)

    args = parser.parse_args()
    try:
        args.func(args)
    except ConnectionRefusedError:
        print(f"Error: could not connect to {args.socket} — is the server running?", file=sys.stderr)
        sys.exit(1)
    except FileNotFoundError:
        print(f"Error: socket not found at {args.socket}", file=sys.stderr)
        sys.exit(1)
    except json.JSONDecodeError as e:
        print(f"Error: invalid JSON — {e}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
