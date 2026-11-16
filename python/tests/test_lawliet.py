"""Tests for the Python package. They run the real server binary: either the one
bundled in the installed wheel or LAWLIET_BINARY."""

from __future__ import annotations

import os
import socket
import subprocess
import sys

import pytest

import lawliet
from lawliet import LawlietError, Server


def port_closed(port: int) -> bool:
    # A raw socket: redis-py retries refused connections with backoff, which only slows tests.
    try:
        socket.create_connection(("127.0.0.1", port), timeout=0.5).close()
        return False
    except OSError:
        return True


def test_binary_is_available():
    path = lawliet.binary_path()
    assert os.path.isfile(path)
    assert os.name == "nt" or os.access(path, os.X_OK)


def test_start_stop():
    r = lawliet.start()
    try:
        assert r.ping()
        assert r.execute_command("VCREATE", "docs", "DIM", 3) in (b"OK", "OK", True)
        r.execute_command("VADD", "docs", "a", "VALUES", 3, 1, 0, 0, "TEXT", "first")
        r.execute_command("VADD", "docs", "b", "VALUES", 3, 0, 1, 0, "TEXT", "second")
        hits = r.execute_command("VSEARCH", "docs", "VALUES", 3, 1, 0.1, 0, "K", 1, "WITHTEXT")
        assert hits[0][0] == b"a"
        assert hits[0][2] == b"first"
        with pytest.raises(LawlietError, match="already started"):
            lawliet.start()
    finally:
        lawliet.stop()
    assert port_closed(r.connection_pool.connection_kwargs["port"])
    lawliet.stop()  # idempotent


def test_context_manager_stops_server():
    with lawliet.server() as r:
        assert r.ping()
        port = r.connection_pool.connection_kwargs["port"]
    assert port_closed(port)


def test_servers_are_independent():
    with Server() as a, Server() as b:
        assert a.port != b.port
        a.client().execute_command("VCREATE", "only_a", "DIM", 2)
        assert a.client().execute_command("VLIST") == [b"only_a"]
        assert b.client().execute_command("VLIST") == []


def test_explicit_port_in_use():
    with Server() as first:
        with pytest.raises(LawlietError, match="in use"):
            Server(first.port).start()


def test_bad_flag_reports_server_error():
    with pytest.raises(LawlietError, match="maxmemory-policy"):
        Server(args=["--maxmemory-policy", "bogus"]).start()


def test_snapshot_survives_restart(tmp_path):
    with Server(data_dir=tmp_path) as srv:
        r = srv.client()
        r.execute_command("VCREATE", "docs", "DIM", 2)
        r.execute_command("VADD", "docs", "a", "VALUES", 2, 1, 0)
    # stop() sends SIGTERM; the server writes a snapshot on shutdown (not on Windows).
    if os.name == "nt":
        pytest.skip("graceful shutdown snapshot needs SIGTERM")
    with Server(data_dir=tmp_path) as srv:
        assert srv.client().execute_command("VCOUNT", "docs") == 1


def test_missing_binary(monkeypatch, tmp_path):
    monkeypatch.setenv("LAWLIET_BINARY", str(tmp_path / "nope"))
    with pytest.raises(LawlietError, match="LAWLIET_BINARY"):
        lawliet.binary_path()


def test_cli_version():
    out = subprocess.run(
        [sys.executable, "-m", "lawliet", "--version"],
        capture_output=True, text=True, check=True,
    ).stdout
    assert out.startswith("lawliet ")
