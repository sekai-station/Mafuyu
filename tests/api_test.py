import asyncio
import json
import time

import httpx
import pytest
import websockets

from conftest import V2_URL, V1_URL, BASE_URL, AUTH_HEADERS


def make_room(room_id: str, msg: str = "test message", source: str = "x") -> dict:
    return {
        "time": int(time.time()),
        "id": room_id,
        "msg": msg,
        "name": "testuser",
        "source": source,
        "info": {
            "tid": "123456789",
            "userName": "@testuser",
            "screenName": "Test User",
            "avatar": "",
        },
    }


# ─── v2 HTTP Tests ───


class TestV2Recent:
    def test_recent_returns_200(self):
        r = httpx.get(f"{V2_URL}/recent")
        assert r.status_code == 200
        body = r.json()
        assert body["code"] == 200
        assert isinstance(body["data"], list)


class TestV2Submit:
    def test_submit_no_auth(self):
        r = httpx.post(f"{V2_URL}/submit", json={"data": [make_room("99999")]})
        assert r.status_code == 401
        body = r.json()
        assert body["code"] == 401
        assert "data" in body

    def test_submit_bad_token(self):
        r = httpx.post(
            f"{V2_URL}/submit",
            json={"data": [make_room("99998")]},
            headers={"X-API-Key": "wrong"},
        )
        assert r.status_code == 401

    def test_submit_success(self):
        room_id = f"1{int(time.time()) % 10000:04d}"
        r = httpx.post(
            f"{V2_URL}/submit",
            json={"data": [make_room(room_id)]},
            headers=AUTH_HEADERS,
        )
        assert r.status_code == 200
        body = r.json()
        assert body["code"] == 200

    def test_recent_after_submit(self):
        room_id = f"2{int(time.time()) % 10000:04d}"
        httpx.post(
            f"{V2_URL}/submit",
            json={"data": [make_room(room_id)]},
            headers=AUTH_HEADERS,
        )
        r = httpx.get(f"{V2_URL}/recent")
        body = r.json()
        ids = [room["id"] for room in body["data"]]
        assert room_id in ids

    def test_recent_info_shape(self):
        room_id = f"3{int(time.time()) % 10000:04d}"
        httpx.post(
            f"{V2_URL}/submit",
            json={"data": [make_room(room_id)]},
            headers=AUTH_HEADERS,
        )
        room = next(r for r in httpx.get(f"{V2_URL}/recent").json()["data"] if r["id"] == room_id)
        # Same fields whatever the platform; the stored XInfo only with ?extra=1
        assert set(room["info"]) == {"handle", "url", "avatar"}
        assert room["info"]["url"].endswith("/status/123456789")
        assert room["info"]["avatar"] is None
        full = next(r for r in httpx.get(f"{V2_URL}/recent?extra=1").json()["data"] if r["id"] == room_id)
        assert full["info"]["extra"]["type"] == "XInfo"
        assert full["info"]["extra"]["tid"] == "123456789"


class TestV2Statistic:
    def test_statistic(self):
        r = httpx.get(f"{V2_URL}/statistic")
        assert r.status_code == 200
        body = r.json()
        assert body["code"] == 200
        assert "online" in body["data"]
        assert "past15m" in body["data"]


class TestV2Announcement:
    def test_announcement(self):
        r = httpx.get(f"{V2_URL}/announcement")
        assert r.status_code == 200
        body = r.json()
        assert body["code"] == 200
        assert "msg" in body["data"]
        assert "time" in body["data"]


class TestV2Status:
    def test_status(self):
        r = httpx.get(f"{V2_URL}/status")
        assert r.status_code == 200
        body = r.json()
        assert body["code"] == 200
        assert "pastCount" in body["data"]
        assert "channelHealth" in body["data"]
        pc = body["data"]["pastCount"]
        assert "past15m" in pc
        assert "past1h" in pc
        assert "past24h" in pc


class TestV2SSE:
    @pytest.mark.asyncio
    async def test_sse_room_after_submit(self):
        room_id = f"3{int(time.time()) % 10000:04d}"

        async with httpx.AsyncClient() as client:
            async with client.stream("GET", f"{V2_URL}/realtime") as stream:
                # Submit a room after connecting
                await asyncio.sleep(0.3)
                await client.post(
                    f"{V2_URL}/submit",
                    json={"data": [make_room(room_id)]},
                    headers=AUTH_HEADERS,
                )

                found = False
                event = None
                deadline = time.time() + 5
                async for line in stream.aiter_lines():
                    if time.time() > deadline:
                        break
                    if line.startswith("event:"):
                        event = line[6:].strip()
                    elif line.startswith("data:") and event == "room":
                        data = json.loads(line[5:].strip())
                        if data["id"] == room_id:
                            found = True
                            break
                assert found, f"room {room_id} not received via SSE"


class TestEnvelope:
    @pytest.mark.asyncio
    async def test_stream_survives_multiple_heartbeat_intervals(self):
        async with asyncio.timeout(50):
            async with httpx.AsyncClient(timeout=35) as client:
                async with client.stream("GET", f"{V2_URL}/realtime") as stream:
                    assert stream.status_code == 200
                    heartbeats = 0
                    async for line in stream.aiter_lines():
                        if line == "event: heartbeat":
                            heartbeats += 1
                            if heartbeats == 2:
                                return
                    assert False, "SSE ended before the second heartbeat"

    def test_error_has_data_field(self):
        r = httpx.post(f"{V2_URL}/submit", json={"data": []})
        body = r.json()
        assert "data" in body

    def test_timestamps_are_seconds(self):
        room_id = f"4{int(time.time()) % 10000:04d}"
        httpx.post(
            f"{V2_URL}/submit",
            json={"data": [make_room(room_id)]},
            headers=AUTH_HEADERS,
        )
        r = httpx.get(f"{V2_URL}/recent")
        body = r.json()
        for room in body["data"]:
            assert room["time"] < 9999999999, "timestamp looks like milliseconds"


# ─── v1 WebSocket Tests ───


class TestV1:
    @pytest.mark.asyncio
    async def test_ws_handshake(self):
        ws_url = V1_URL.replace("http://", "ws://") + "/"
        async with websockets.connect(ws_url) as ws:
            # Message 1: server time
            msg1 = json.loads(await asyncio.wait_for(ws.recv(), timeout=3))
            assert msg1["action"] == "sendServerTime"
            assert msg1["status"] == "success"
            assert "time" in msg1["response"]

            # Message 2: room list
            msg2 = json.loads(await asyncio.wait_for(ws.recv(), timeout=3))
            assert msg2["action"] == "sendRoomNumberList"
            assert isinstance(msg2["response"], list)

            # Message 3: announcement
            msg3 = json.loads(await asyncio.wait_for(ws.recv(), timeout=3))
            assert msg3["action"] == "sendRoomNumberList"
            assert isinstance(msg3["response"], list)
            if len(msg3["response"]) > 0:
                ann = msg3["response"][0]
                assert ann["source_info"]["type"] == "system"

    @pytest.mark.asyncio
    async def test_ws_heartbeat(self):
        ws_url = V1_URL.replace("http://", "ws://") + "/"
        async with websockets.connect(ws_url) as ws:
            # drain handshake
            for _ in range(3):
                await asyncio.wait_for(ws.recv(), timeout=3)

            await ws.send(json.dumps({"action": "heartbeat"}))
            resp = json.loads(await asyncio.wait_for(ws.recv(), timeout=3))
            assert resp["action"] == "heartbeat"
            assert resp["response"] == "alive"

    @pytest.mark.asyncio
    async def test_ws_get_room_list(self):
        ws_url = V1_URL.replace("http://", "ws://") + "/"
        async with websockets.connect(ws_url) as ws:
            for _ in range(3):
                await asyncio.wait_for(ws.recv(), timeout=3)

            await ws.send(json.dumps({"action": "getRoomNumberList"}))
            resp = json.loads(await asyncio.wait_for(ws.recv(), timeout=3))
            assert resp["action"] == "sendRoomNumberList"
            assert isinstance(resp["response"], list)

    @pytest.mark.asyncio
    async def test_ws_setClient_noop(self):
        ws_url = V1_URL.replace("http://", "ws://") + "/"
        async with websockets.connect(ws_url) as ws:
            for _ in range(3):
                await asyncio.wait_for(ws.recv(), timeout=3)

            await ws.send(json.dumps({"action": "setClient"}))
            # setClient is a no-op, send heartbeat to verify connection still works
            await ws.send(json.dumps({"action": "heartbeat"}))
            resp = json.loads(await asyncio.wait_for(ws.recv(), timeout=3))
            assert resp["action"] == "heartbeat"

    @pytest.mark.asyncio
    async def test_ws_broadcast_after_submit(self):
        room_id = f"5{int(time.time()) % 10000:04d}"
        ws_url = V1_URL.replace("http://", "ws://") + "/"
        async with websockets.connect(ws_url) as ws:
            for _ in range(3):
                await asyncio.wait_for(ws.recv(), timeout=3)

            # Submit a room via v2
            async with httpx.AsyncClient() as client:
                await client.post(
                    f"{V2_URL}/submit",
                    json={"data": [make_room(room_id)]},
                    headers=AUTH_HEADERS,
                )

            # Should receive broadcast
            resp = json.loads(await asyncio.wait_for(ws.recv(), timeout=5))
            assert resp["action"] == "sendRoomNumberList"
            assert any(r["number"] == room_id for r in resp["response"])

    def test_submit_redirect(self):
        r = httpx.post(
            f"{BASE_URL}/station/api/submitRoomNumber",
            json={
                "create_time": time.time(),
                "username": "user",
                "user_send_scene": "x",
                "user_game_id": "12345",
                "room_id": "77777",
                "description": "test",
            },
            follow_redirects=False,
            headers=AUTH_HEADERS,
        )
        assert r.status_code == 307
        assert "/station/api/v1/" in r.headers.get("location", "")
