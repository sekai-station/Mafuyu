"""Exercise process shutdown with actual long-lived connections and SQLite."""
import json
import socket
import sqlite3
import subprocess
import time

import httpx
from websockets.sync.client import connect

from conftest import AUTH_HEADERS


def test_shutdown_closes_streams_and_saves_final_snapshot(server, tmp_path):
    with socket.socket() as reserve:
        reserve.bind(("127.0.0.1", 0))
        port = reserve.getsockname()[1]
    announcements = tmp_path / "announcements"
    announcements.mkdir()
    uds_path = tmp_path / "server.sock"
    db_path = tmp_path / "data.db"
    config = tmp_path / "config.yaml"
    config.write_text(f"""host: 127.0.0.1
port: {port}
uds_path: {uds_path}
database_path: {db_path}
announcement_dir: {announcements}
log_level: error
backend:
  unique_time: 0
  expire_time: 500
  sqlite_record_keep_day: 1
v1:
  break_websocket_time: 6000
submission:
  http:
    enabled: true
auth:
  static:
    enabled: true
    clients:
      - name: test_client
        token: {json.dumps(AUTH_HEADERS["X-API-Key"])}
""")
    base = f"http://127.0.0.1:{port}"
    with (tmp_path / "server.log").open("w+") as log:
        proc = subprocess.Popen([server.args[0], "-config", str(config)],
                                cwd=tmp_path, stdout=log, stderr=subprocess.STDOUT)
        try:
            for _ in range(100):
                assert proc.poll() is None, "server failed to start"
                try:
                    if uds_path.exists() and httpx.get(base + "/health").status_code == 200:
                        break
                except httpx.TransportError:
                    pass
                time.sleep(0.05)
            else:
                raise AssertionError("server did not become ready")

            room = {"time": int(time.time()), "id": "12345", "msg": "final snapshot",
                    "name": "test", "source": "x", "info": {"tid": "1"}}
            response = httpx.post(base + "/station/api/v2/submit",
                                  json={"data": [room]}, headers=AUTH_HEADERS)
            assert response.status_code == 200

            with socket.socket(socket.AF_UNIX) as uds:
                uds.connect(str(uds_path))  # idle reader must be closed on shutdown
                with connect(base.replace("http://", "ws://") + "/station/api/v1/") as ws:
                    assert json.loads(ws.recv(timeout=3))["action"] == "sendServerTime"
                    with httpx.stream("GET", base + "/station/api/v2/realtime", timeout=3) as sse:
                        assert sse.status_code == 200
                        proc.terminate()
                        assert proc.wait(timeout=10) == 0

            with sqlite3.connect(db_path) as db:
                assert db.execute("SELECT msg FROM rooms WHERE room_id='12345'").fetchone() == ("final snapshot",)
                assert db.execute("SELECT SUM(count) FROM channel_stats").fetchone() == (1,)
        finally:
            if proc.poll() is None:
                proc.kill()
                proc.wait()
