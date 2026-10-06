import json
import secrets
import socket
import subprocess
import time
from pathlib import Path

import pytest

BASE_URL = "http://127.0.0.1:18888"
V2_URL = f"{BASE_URL}/station/api/v2"
V1_URL = f"{BASE_URL}/station/api/v1"
AUTH_HEADERS = {"X-API-Key": secrets.token_urlsafe(32)}


@pytest.fixture(scope="session", autouse=True)
def server(tmp_path_factory):
    # Never overwrite the working server's announcement, database or socket.
    directory = tmp_path_factory.mktemp("backend-integration")
    announcements = directory / "announcements"
    announcements.mkdir()
    (announcements / "en.txt").write_text("Test announcement message")
    socket_path = directory / "server.sock"
    config = directory / "config.yaml"
    config.write_text(f"""host: 127.0.0.1
port: 18888
uds_path: {socket_path}
database_path: {directory / 'data.db'}
announcement_dir: {announcements}
log_level: error
backend:
  unique_time: 2
  expire_time: 300
  sqlite_record_keep_day: 1
v1:
  break_websocket_time: 60
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
    binary = directory / "server"
    subprocess.run(["go", "build", "-o", str(binary), "./cmd/server/"],
                   cwd=Path(__file__).resolve().parent.parent, check=True)
    log = (directory / "server.log").open("w+")
    proc = subprocess.Popen([str(binary), "-config", str(config)], cwd=directory,
                            stdout=log, stderr=subprocess.STDOUT)
    try:
        for _ in range(100):
            if proc.poll() is not None:
                log.seek(0)
                raise RuntimeError(f"Server failed to start: {log.read()}")
            try:
                with socket.create_connection(("127.0.0.1", 18888), timeout=0.5):
                    pass
                break
            except OSError:
                time.sleep(0.1)
        else:
            raise RuntimeError("Server failed to start")
        yield proc
    finally:
        if proc.poll() is None:
            proc.terminate()
            try:
                proc.wait(timeout=30)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait()
                pytest.fail("Server did not shut down within 30 seconds")
        log.close()
