#!/usr/bin/env python3
"""
UDS Room Generator — continuously sends fake PJSK rooms to the backend via Unix Domain Socket.

Usage:
    python3 uds_room_generator.py                # default: 1 room every 5 seconds
    python3 uds_room_generator.py --interval 1   # 1 room per second
    python3 uds_room_generator.py --burst 5      # 5 rooms per batch
    python3 uds_room_generator.py --edge-ratio 0 # only well-formed rooms (default mixes in 15% edge cases)
    python3 uds_room_generator.py --cases        # send every edge case once, then check /recent
    python3 uds_room_generator.py --cases --avatars-hidden   # same, for a server with v2.send_avatar: false

Edge cases cover how v2 shows room info (handle / url / avatar, see
the Rooms section of API.md): missing avatar, user name, tweet ID, QQ
nickname or number, and rooms without a source.
"""

import argparse
import json
import random
import socket
import string
import sys
import time
import urllib.request

UDS_PATH = "/tmp/pjsk_server_uds"
API_BASE = "http://127.0.0.1:8888/station/api/v2"

# ── Fake data pools ──────────────────────────────────────────────────────────

CHANNELS = ["x-crawler", "qq-bot", "discord-relay"]

# Realistic long messages like actual PJSK room posts
MESSAGES_LONG = [
    "ベテラン MV周回 23時まで‼️\n@ 2\n🗝76738\n主 )☕️\n募 )お着替えしたあなたの推し様♡\n\n揃うまで待てる方 じゃんじゃん選曲してください🎶\n主のおつさきで解散です👾ᩚ\n\n#プロセカ協力 #プロセカ募集",
    "【協力募集】\nMASTER FC周回！\n上級者歓迎🔥\n\n🎮 ルーム: 自動\n⏰ 22時～24時\n👥 あと2人\n\n選曲自由！楽しくやりましょう✨\nリプで参加表明お願いします🙏\n\n#プロセカ協力 #プロセカ募集",
    "チアフルカーニバル周回‼️\n\n🏆 称号狙い\n📍 上級+ / MASTER\n⏰ 終日OK\n\n誰でもウェルカム！\n初心者さんも気軽にどうぞ～\n主は25時ナイトコードのミク推しです💙\n\n#プロセカ #チアフル",
    "🎵 イベントランキング周回 🎵\nブースト消化手伝います！\n\n条件:\n・EXPERT以上選曲できる方\n・途中抜けしない方\n・楽しめる方！\n\n主のスタミナ切れまで！\nよろしくお願いします🎶\n\n#プロセカ募集",
    "APPEND練習部屋🎹\n\n難しい曲に挑戦したい人集合！\nフルコン出来なくても全然OK👌\n\n好きな曲選んでください～\nまったりやりましょう☕️\n\n#プロセカ #APPEND",
    "【初心者歓迎】プロセカ協力🌟\n\nEASY～HARDまでOK！\n始めたばかりの方も大歓迎です✨\n\nゆっくりペースで\n楽しくプレイしましょう🎮\n\n22時頃まで募集中～\n\n#プロセカ初心者 #プロセカ協力",
    "ワンダショ推し集合！！🎪✨\n\nイベント曲周回しませんか？\nワンダショの曲縛りで遊びましょう！\n\n🎭 えむ推し主です\n⏰ 0時まで\n\nワイワイ楽しくやりたい！\n\n#ワンダショ #プロセカ募集",
    "ランク上げ周回部屋🔄\n\nEXPERT/MASTER交互で回します\n効率重視！サクサク行きましょう💨\n\nスタンプ連打歓迎ｗ\n\n#プロセカ協力",
    "🌙 深夜プロセカ部屋 🌙\n\n眠れない人～集合！\n\nゆるーく協力しましょう\n選曲なんでもOK\n雑談しながらまったり🍵\n\n#プロセカ深夜 #プロセカ協力",
    "称号コンプ目指してます💎\n\n各楽曲のFC称号取りたいので\n付き合ってくれる方募集！\n\nMASTER FC出来る方だと嬉しいです\n1時間くらいやりたい！\n\n#プロセカ称号 #プロセカ募集",
]

MESSAGES_SHORT = [
    "協力お願いします！",
    "チアフルカーニバル 上級+",
    "イベント周回 称号狙い",
    "MASTER FC目指す",
    "誰でも歓迎！初心者OK",
    "APPEND練習中",
    "ランク上げ手伝って",
    "初音ミク推しの方！",
    "まったり協力しましょう",
    "イベラン！ブースト消化",
    "EXPERTフルコン目標",
    "称号集め お手伝い",
]

X_SCREEN_NAMES = [
    "プロセカ大好き", "Miku_Fan_2026", "SEKAI推し", "リズムゲーマー",
    "ワンダショ民", "Pruby_TW", "VBS最高", "ニーゴ推し",
    "25時ナイトコード", "レオニ担",
]

X_USERNAMES = [
    "@proseka_love", "@miku_fan", "@sekai_player", "@rhythm_gamer",
    "@wondershow", "@pruby_tw", "@vbs_best", "@niigo_fan",
    "@25nightcode", "@leoni_oshi",
]

QQ_NICKNAMES = [
    "世界第一", "啪嗒玩家", "节奏达人", "初音未来",
    "虚拟歌手", "音游大佬", "PJSK新人", "协力达人",
]


def gen_room_id() -> str:
    """Generate a random 5-digit room ID."""
    return "".join(random.choices(string.digits, k=5))


def gen_x_info() -> dict:
    """Generate a fake X/Twitter info object."""
    idx = random.randint(0, len(X_SCREEN_NAMES) - 1)
    return {
        "tid": str(random.randint(1000000000000, 9999999999999)),
        "userName": X_USERNAMES[idx],
        "screenName": X_SCREEN_NAMES[idx],
        "avatar": f"https://pbs.twimg.com/profile_images/{random.randint(1000000, 9999999)}/photo.jpg",
    }


def gen_qq_info() -> dict:
    """Generate a fake QQ info object."""
    return {
        "qq": random.randint(100000, 9999999999),
        "nickname": random.choice(QQ_NICKNAMES),
        "avatar": f"https://q1.qlogo.cn/g?b=qq&nk={random.randint(100000, 9999999999)}&s=640",
    }


# ── Edge cases ───────────────────────────────────────────────────────────────
# Each case: label, source (None = key left out), submitted info, and the v2
# info /recent should return for it (None = avatar null).

EDGE_CASES = [
    ("X without avatar", "x",
     {"tid": "1111111111111", "userName": "@no_avatar", "screenName": "No Avatar", "avatar": ""},
     {"handle": "@no_avatar", "url": "https://x.com/no_avatar/status/1111111111111", "avatar": None}),
    ("X without user name", "x",
     {"tid": "2222222222222", "userName": "", "screenName": "Screen Only", "avatar": "https://pbs.twimg.com/profile_images/1/a.jpg"},
     {"handle": "Screen Only", "url": "https://x.com/i/status/2222222222222", "avatar": "https://pbs.twimg.com/profile_images/1/a.jpg"}),
    ("X without tweet ID", "x",
     {"tid": "", "userName": "@no_tid", "screenName": "No Tid", "avatar": ""},
     {"handle": "@no_tid", "url": "", "avatar": None}),
    ("QQ without nickname", "qq",
     {"qq": 123456789, "nickname": "", "avatar": "https://q1.qlogo.cn/g?b=qq&nk=123456789&s=640"},
     {"handle": "QQ:123456789", "url": "", "avatar": "https://q1.qlogo.cn/g?b=qq&nk=123456789&s=640"}),
    ("QQ with null number and no nickname", "qq",
     {"qq": None, "nickname": "", "avatar": ""},
     {"handle": "", "url": "", "avatar": None}),
    ("QQ without avatar", "qq",
     {"qq": 987654321, "nickname": "无头像", "avatar": ""},
     {"handle": "无头像", "url": "", "avatar": None}),
    ("empty source (no badge)", "",
     {"qq": 555555, "nickname": "无来源", "avatar": ""},
     {"handle": "无来源", "url": "", "avatar": None}),
    ("source left out (no badge)", None,
     {"tid": "3333333333333", "userName": "@no_source", "screenName": "No Source", "avatar": ""},
     {"handle": "@no_source", "url": "https://x.com/no_source/status/3333333333333", "avatar": None}),
]


def gen_edge_room(case) -> dict:
    """A room for one edge case, posted just now."""
    label, source, info, _ = case
    room = {
        "time": int(time.time()) - 1,
        "id": gen_room_id(),
        "msg": f"[edge case] {label}",
        "name": label,
        "info": dict(info),
    }
    if source is not None:
        room["source"] = source
    return room


def gen_room_data(count: int = 1, edge_ratio: float = 0.0) -> list[dict]:
    """Generate a list of room data items; edge_ratio of them are edge cases."""
    rooms = []
    for _ in range(count):
        if random.random() < edge_ratio:
            rooms.append(gen_edge_room(random.choice(EDGE_CASES)))
            continue
        source = random.choice(["x", "x", "x", "qq"])  # 75% X, 25% QQ
        name_pool = X_SCREEN_NAMES if source == "x" else QQ_NICKNAMES
        # 60% long messages, 40% short
        if random.random() < 0.6:
            msg = random.choice(MESSAGES_LONG)
        else:
            msg = random.choice(MESSAGES_SHORT)
        # Simulate crawl delay: time is 3-15 seconds in the past
        crawl_delay = random.randint(3, 15)
        rooms.append({
            "time": int(time.time()) - crawl_delay,
            "id": gen_room_id(),
            "msg": msg,
            "name": random.choice(name_pool),
            "source": source,
            "info": gen_x_info() if source == "x" else gen_qq_info(),
        })
    return rooms


def send_uds(payload: dict) -> None:
    """Send a JSON payload to the UDS socket."""
    sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    try:
        sock.connect(UDS_PATH)
        data = json.dumps(payload, ensure_ascii=False) + "\n"
        sock.sendall(data.encode("utf-8"))
    finally:
        sock.close()


def fetch_recent(api: str, extra: bool) -> dict[str, dict]:
    """Rooms from /recent, by room ID."""
    with urllib.request.urlopen(f"{api}/recent{'?extra=1' if extra else ''}", timeout=10) as res:
        return {room["id"]: room for room in json.load(res)["data"]}


def run_cases(api: str, avatars_hidden: bool = False) -> bool:
    """Send every edge case once, then check what /recent returns for each.
    avatars_hidden: the server runs with v2.send_avatar: false, so every avatar is null."""
    rooms = [gen_edge_room(case) for case in EDGE_CASES]
    send_uds({"type": "room", "channel": "edge-cases", "data": rooms})
    print(f"Sent {len(rooms)} edge cases; checking {api}/recent\n")
    time.sleep(1)
    plain, full = fetch_recent(api, False), fetch_recent(api, True)

    ok = True
    for case, room in zip(EDGE_CASES, rooms):
        label, source, info, want = case
        if avatars_hidden:
            want = {**want, "avatar": None}
        got = plain.get(room["id"])
        problems = []
        if got is None:
            problems.append("not in /recent")
        else:
            if got["info"] != want:
                problems.append(f"info {got['info']} != {want}")
            if got["source"] != (source or ""):
                problems.append(f"source {got['source']!r} != {source or ''!r}")
            extra = full.get(room["id"], {}).get("info", {}).get("extra")
            want_extra = {**info, "avatar": None} if avatars_hidden else info
            if not extra or any(extra.get(k) != v for k, v in want_extra.items()):
                problems.append(f"?extra=1 gave {extra}")
        ok &= not problems
        print(f"  {'PASS' if not problems else 'FAIL'}  {room['id']}  {label}" + "".join(f"\n        {p}" for p in problems))
    print("\nAll edge cases passed" if ok else "\nSome edge cases failed")
    if not ok and not avatars_hidden:
        print("(if the server runs with v2.send_avatar: false, add --avatars-hidden)")
    return ok


def main() -> None:
    global UDS_PATH
    parser = argparse.ArgumentParser(description="PJSK UDS Room Generator")
    parser.add_argument("--interval", type=float, default=5.0, help="Seconds between sends (default: 5)")
    parser.add_argument("--burst", type=int, default=1, help="Number of rooms per send (default: 1)")
    parser.add_argument("--count", type=int, default=0, help="Total sends (0 = infinite, default: 0)")
    parser.add_argument("--edge-ratio", type=float, default=0.15, help="Share of rooms that are edge cases (default: 0.15)")
    parser.add_argument("--cases", action="store_true", help="Send every edge case once, check /recent, then exit")
    parser.add_argument("--avatars-hidden", action="store_true", help="With --cases: the server has v2.send_avatar: false")
    parser.add_argument("--uds", default=UDS_PATH, help=f"Socket path (default: {UDS_PATH})")
    parser.add_argument("--api", default=API_BASE, help=f"v2 API base for --cases (default: {API_BASE})")
    args = parser.parse_args()

    UDS_PATH = args.uds
    if args.cases:
        sys.exit(0 if run_cases(args.api, args.avatars_hidden) else 1)

    print(f"🎮 PJSK Room Generator started")
    print(f"   UDS: {UDS_PATH}")
    print(f"   Interval: {args.interval}s | Burst: {args.burst} rooms | Count: {'∞' if args.count == 0 else args.count}")
    print(f"   Press Ctrl+C to stop\n")

    sent = 0
    try:
        while args.count == 0 or sent < args.count:
            channel = random.choice(CHANNELS)
            rooms = gen_room_data(args.burst, args.edge_ratio)
            payload = {
                "type": "room",
                "channel": channel,
                "data": rooms,
            }

            try:
                send_uds(payload)
                sent += 1
                for r in rooms:
                    first_line = r['msg'].split('\n')[0][:40]
                    print(f"  [{sent:>4}] ch={channel:<15} id={r['id']} src={r.get('source') or '-':<3} delay={int(time.time())-r['time']}s msg={first_line}")
            except (ConnectionRefusedError, FileNotFoundError) as e:
                print(f"  ⚠ UDS error: {e} — retrying in {args.interval}s", file=sys.stderr)

            time.sleep(args.interval)

    except KeyboardInterrupt:
        print(f"\n✅ Stopped after {sent} sends")


if __name__ == "__main__":
    main()
