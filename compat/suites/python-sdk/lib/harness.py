"""
lib/harness.py — Core test framework for the Overcast compat Python suite.

Mirrors the Node.js harness: emits NDJSON events to stdout, runs groups
sequentially, emits "skip" / "unimplemented" / "pass" / "fail" per test.

Rules:
- Never write non-NDJSON to stdout — use ctx.log() for debug output.
- Tests raise to signal failure; returning normally means pass.
- Teardown always runs, even if tests failed.
"""

from __future__ import annotations

import json
import os
import sys
import threading
import time
from concurrent.futures import ThreadPoolExecutor, as_completed, TimeoutError as FutureTimeoutError
from dataclasses import dataclass, field
from typing import Any, Callable, Awaitable, Optional, Union


# ─── Context ─────────────────────────────────────────────────────────────────

class TestContext:
    """
    Per-run context passed to every test function.

    Attributes are read-only (endpoint, region, run_id).
    Tests may add arbitrary keys for cross-test state within a group.
    """

    def __init__(self, endpoint: str, region: str, run_id: str) -> None:
        self.endpoint = endpoint
        self.region = region
        self.run_id = run_id
        self._state: dict[str, Any] = {}

    # Allow tests to store per-group state via ctx["key"] = value
    def __getitem__(self, key: str) -> Any:
        return self._state[key]

    def __setitem__(self, key: str, value: Any) -> None:
        self._state[key] = value

    def __contains__(self, key: str) -> bool:
        return key in self._state

    def get(self, key: str, default: Any = None) -> Any:
        return self._state.get(key, default)

    def log(self, msg: str) -> None:
        """Write a debug message to stderr (never stdout)."""
        sys.stderr.write(f"[compat:python-sdk] {msg}\n")


# ─── Types ────────────────────────────────────────────────────────────────────

TestFn = Callable[["TestContext"], None]

@dataclass
class TestCase:
    name: str
    fn: TestFn
    skip: Union[bool, str, None] = None
    op: Union[str, bool, None] = None   # False = suppress doc link
    na: Optional[str] = None  # N/A reason: SDK doesn't expose this operation


@dataclass
class TestGroup:
    suite: str
    service: str
    name: str
    tests: list[TestCase]
    setup: Optional[TestFn] = None
    teardown: Optional[TestFn] = None


# ─── NDJSON emitters ─────────────────────────────────────────────────────────

# _emit_lock serialises writes to stdout so that threads running groups in
# parallel never produce interleaved NDJSON lines.
_emit_lock = threading.Lock()

def _emit(event: dict) -> None:
    line = json.dumps(event) + "\n"
    with _emit_lock:
        sys.stdout.write(line)
        sys.stdout.flush()


def emit_run_start(suite: str, endpoint: str, total_tests: int = 0) -> None:
    _emit({
        "event": "run_start",
        "suite": suite,
        "started_at": _iso_now(),
        "endpoint": endpoint,
        "version": "1",
        **(({"total_tests": total_tests}) if total_tests else {}),
    })


def emit_run_end(suite: str, passed: int, failed: int, skipped: int,
                 unimplemented: int, duration_ms: int) -> None:
    _emit({
        "event": "run_end",
        "suite": suite,
        "passed": passed,
        "failed": failed,
        "skipped": skipped,
        "unimplemented": unimplemented,
        "duration_ms": duration_ms,
    })


def emit_building(suite: str, message: str) -> None:
    _emit({"event": "building", "suite": suite, "message": message})


def emit_ready(suite: str, total_tests: int) -> None:
    _emit({"event": "ready", "suite": suite, "total_tests": total_tests})


def emit_batch_complete(suite: str, batch_id: str, passed: int, failed: int,
                        skipped: int, unimplemented: int, cancelled: int,
                        duration_ms: int) -> None:
    _emit({
        "event": "batch_complete",
        "suite": suite,
        "batch_id": batch_id,
        "passed": passed,
        "failed": failed,
        "skipped": skipped,
        "unimplemented": unimplemented,
        "cancelled": cancelled,
        "duration_ms": duration_ms,
    })


def emit_cancelled(suite: str, batch_id: str, group: str, test: str,
                   reason: str = "") -> None:
    ev: dict = {"event": "cancelled", "suite": suite, "batch_id": batch_id,
                "group": group, "test": test}
    if reason:
        ev["reason"] = reason
    _emit(ev)


def _iso_now() -> str:
    from datetime import datetime, timezone
    return datetime.now(timezone.utc).isoformat()


# ─── Unimplemented detection ─────────────────────────────────────────────────

def _is_unimplemented(exc: Exception) -> bool:
    """Return True if the exception represents a 501 Not Implemented response."""
    from botocore.exceptions import ClientError, BotoCoreError
    if isinstance(exc, ClientError):
        code = exc.response.get("ResponseMetadata", {}).get("HTTPStatusCode")
        if code == 501:
            return True
        # Some services return "NotImplemented" or "UnknownOperationException"
        err_code = exc.response.get("Error", {}).get("Code", "")
        if err_code in ("NotImplemented", "UnknownOperationException"):
            return True
    # Catch HTTP-level errors wrapped in EndpointResolutionError etc.
    msg = str(exc)
    if "501" in msg and "Not Implemented" in msg:
        return True
    return False


# ─── Group runner ─────────────────────────────────────────────────────────────

def run_group(group: TestGroup, ctx: TestContext, *,
              cancel_event: Optional[threading.Event] = None,
              batch_id: str = "") -> tuple[int, int, int, int, int]:
    """
    Run one test group synchronously.
    Returns (passed, failed, skipped, unimplemented, cancelled).
    """
    passed = failed = skipped = unimplemented = cancelled_count = 0

    # Setup phase
    if group.setup:
        try:
            group.setup(ctx)
        except Exception as exc:
            reason = f"setup failed: {exc}"
            for tc in group.tests:
                _emit({
                    "event": "test_result",
                    "suite": group.suite,
                    "service": group.service,
                    "group": group.name,
                    "test": tc.name,
                    "status": "skip",
                    "duration_ms": 0,
                    "error": reason,
                })
                skipped += 1
            _run_teardown(group, ctx)
            return passed, failed, skipped, unimplemented, cancelled_count

    for tc in group.tests:
        # Check cancellation before each test
        if cancel_event and cancel_event.is_set():
            emit_cancelled(group.suite, batch_id, group.name, tc.name, "user")
            cancelled_count += 1
            continue

        _emit({"event": "test_start", "suite": group.suite,
               "service": group.service, "group": group.name, "test": tc.name})

        if tc.na:
            _emit({
                "event": "test_result",
                "suite": group.suite,
                "service": group.service,
                "group": group.name,
                "test": tc.name,
                "status": "na",
                "duration_ms": 0,
                "error": tc.na,
            })
            continue

        if tc.skip:
            reason = tc.skip if isinstance(tc.skip, str) else "skipped"
            _emit({
                "event": "test_result",
                "suite": group.suite,
                "service": group.service,
                "group": group.name,
                "test": tc.name,
                "status": "skip",
                "duration_ms": 0,
                "error": reason,
            })
            skipped += 1
            continue

        start = time.monotonic()
        try:
            tc.fn(ctx)
            duration = int((time.monotonic() - start) * 1000)
            result: dict[str, Any] = {
                "event": "test_result",
                "suite": group.suite,
                "service": group.service,
                "group": group.name,
                "test": tc.name,
                "status": "pass",
                "duration_ms": duration,
            }
            if tc.op is not None and tc.op is not False:
                result["op"] = tc.op
            elif tc.op is False:
                pass  # suppress doc link — don't set "op"
            _emit(result)
            passed += 1
        except Exception as exc:
            duration = int((time.monotonic() - start) * 1000)
            if _is_unimplemented(exc):
                status = "unimplemented"
                unimplemented += 1
            else:
                status = "fail"
                failed += 1
            result = {
                "event": "test_result",
                "suite": group.suite,
                "service": group.service,
                "group": group.name,
                "test": tc.name,
                "status": status,
                "duration_ms": duration,
                "error": str(exc),
            }
            if tc.op is not None and tc.op is not False:
                result["op"] = tc.op
            _emit(result)

    _run_teardown(group, ctx)
    return passed, failed, skipped, unimplemented, cancelled_count


def _run_teardown(group: TestGroup, ctx: TestContext) -> None:
    if group.teardown:
        try:
            group.teardown(ctx)
        except Exception as exc:
            sys.stderr.write(f"[compat:python-sdk] teardown {group.name} failed: {exc}\n")


# ─── Suite runner ─────────────────────────────────────────────────────────────

def run_suite(suite: str, groups: list[TestGroup], endpoint: str,
              region: str, run_id: str) -> None:
    """Run all groups in parallel, emit NDJSON events, finalize with run_end.

    Each group receives its own fresh TestContext so that per-group state
    stored via ctx["key"] = value does not leak between concurrent groups.
    """
    total_tests = sum(len(g.tests) for g in groups)
    emit_run_start(suite, endpoint, total_tests=total_tests)
    start = time.monotonic()

    def _run_one(group: TestGroup) -> tuple[int, int, int, int, int]:
        ctx = TestContext(endpoint=endpoint, region=region, run_id=run_id)
        return run_group(group, ctx)

    # Limit concurrent group execution to avoid overwhelming the emulator.
    # OVERCAST_COMPAT_PARALLEL_SLOTS is injected by the Go runner based on
    # CPU count and the number of active suites (default: 8).
    max_workers = max(1, int(os.environ.get("OVERCAST_COMPAT_PARALLEL_SLOTS", "8") or "8"))

    total_passed = total_failed = total_skipped = total_unimplemented = 0
    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        futures = {executor.submit(_run_one, g): g for g in groups}
        # as_completed with a total budget prevents one stuck group from
        # blocking the suite forever.  25 minutes covers the worst-case
        # scenario of many slow groups; individual groups that hang will be
        # cancelled by the runner.go suite-level timeout (25 min).
        SUITE_TIMEOUT_S = 25 * 60
        try:
            for future in as_completed(futures, timeout=SUITE_TIMEOUT_S):
                group = futures[future]
                try:
                    p, f, s, u, _c = future.result()
                    total_passed += p
                    total_failed += f
                    total_skipped += s
                    total_unimplemented += u
                except Exception as exc:
                    sys.stderr.write(
                        f"[compat:python-sdk] group {group.name} raised: {exc}\n"
                    )
        except FutureTimeoutError:
            sys.stderr.write(
                "[compat:python-sdk] suite timed out — some groups did not finish\n"
            )

    duration_ms = int((time.monotonic() - start) * 1000)
    emit_run_end(suite, total_passed, total_failed, total_skipped,
                 total_unimplemented, duration_ms)


def make_run_id() -> str:
    import secrets
    return "oc-" + secrets.token_hex(4)


# ─── Stdin command reader (interactive mode) ─────────────────────────────────

def read_commands():
    """Generator that yields parsed dicts from stdin NDJSON."""
    for line in sys.stdin:
        line = line.strip()
        if not line:
            continue
        try:
            cmd = json.loads(line)
            yield cmd
        except json.JSONDecodeError:
            sys.stderr.write(f"[harness] invalid JSON on stdin: {line}\n")
