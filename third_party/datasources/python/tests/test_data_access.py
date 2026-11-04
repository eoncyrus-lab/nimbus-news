from __future__ import annotations

import builtins
import hashlib
import io
import json
import pathlib
import sys
import unittest
import urllib.error
import warnings
from unittest import mock

PYTHON_ROOT = pathlib.Path(__file__).resolve().parents[1]
HASH_GOLDEN = pathlib.Path(__file__).resolve().parents[2] / "testdata" / "observation_hash_golden.json"
if str(PYTHON_ROOT) not in sys.path:
    sys.path.insert(0, str(PYTHON_ROOT))

import data_access  # noqa: E402
from data_access import legacy  # noqa: E402


class FakeResponse:
    def __init__(self, body: bytes, status: int = 200) -> None:
        self.body = body
        self.status = status

    def __enter__(self) -> FakeResponse:
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def getcode(self) -> int:
        return self.status

    def read(self) -> bytes:
        return self.body


def observation(
    status: str,
    data: object,
    *,
    error: dict[str, object] | None = None,
    version: int = 1,
    extra: dict[str, object] | None = None,
) -> bytes:
    data_json = json.dumps(data, ensure_ascii=True, separators=(",", ":"), allow_nan=False)
    value: dict[str, object] = {
        "version": version,
        "status": status,
        "provider": "stub",
        "endpoint": "history",
        "params": {"symbol": "US:EMPTY"},
        "retrieved_at": "2026-07-20T08:00:00Z",
        "raw_hash": "sha256:" + hashlib.sha256(data_json.encode()).hexdigest(),
        "data": data,
        "error": error,
    }
    if extra:
        value.update(extra)
    return json.dumps(value, separators=(",", ":")).encode()


class StrictDataAccessTests(unittest.TestCase):
    def test_public_route_surface_is_only_migrated_contracts(self) -> None:
        self.assertIn("quote", data_access.__all__)
        self.assertIn("history", data_access.__all__)
        for name in ("kline", "fundamentals", "screening", "funding", "health"):
            self.assertFalse(hasattr(data_access, name), name)

    def test_http_200_empty_is_legitimate_data(self) -> None:
        body = observation("empty", [])
        with mock.patch.object(data_access.urllib.request, "urlopen", return_value=FakeResponse(body)) as request:
            self.assertEqual(data_access.history("US:EMPTY", limit=3), [])
        sent = request.call_args.args[0]
        self.assertEqual(sent.full_url, "http://127.0.0.1:8800/v1/history?symbol=US%3AEMPTY&limit=3")
        self.assertEqual(sent.headers["Accept"], data_access.OBSERVATION_MEDIA_TYPE)

    def test_degraded_returns_data_and_structured_warning(self) -> None:
        failure = {
            "code": "dependency_partial_failure",
            "message": "warehouse unavailable",
            "dependency": "warehouse",
            "retryable": True,
        }
        body = observation("degraded", [{"close": 1}], error=failure)
        with mock.patch.object(data_access.urllib.request, "urlopen", return_value=FakeResponse(body)):
            with warnings.catch_warnings(record=True) as captured:
                warnings.simplefilter("always")
                result = data_access.history("US:SPY")
        self.assertEqual(result, [{"close": 1}])
        self.assertEqual(len(captured), 1)
        warning = captured[0].message
        self.assertIsInstance(warning, data_access.DegradedWarning)
        self.assertEqual(warning.failure.dependency, "warehouse")
        self.assertTrue(warning.failure.retryable)

    def test_plain_http_502_raises_http_error(self) -> None:
        error = urllib.error.HTTPError(
            "http://facade/v1/history", 502, "Bad Gateway", None, io.BytesIO(b'{"error":"down"}')
        )
        with mock.patch.object(data_access.urllib.request, "urlopen", side_effect=error):
            with self.assertRaises(data_access.HTTPError) as raised:
                data_access.history("US:AAPL")
        self.assertEqual(raised.exception.status, 502)

    def test_timeout_and_transport_are_distinct(self) -> None:
        with mock.patch.object(data_access.urllib.request, "urlopen", side_effect=builtins.TimeoutError("slow")):
            with self.assertRaises(data_access.TimeoutError):
                data_access.quote("US:AAPL")
        with mock.patch.object(
            data_access.urllib.request,
            "urlopen",
            side_effect=urllib.error.URLError("connection refused"),
        ):
            with self.assertRaises(data_access.TransportError):
                data_access.quote("US:AAPL")

    def test_bad_json_raises_decode_error(self) -> None:
        with mock.patch.object(data_access.urllib.request, "urlopen", return_value=FakeResponse(b"not json")):
            with self.assertRaises(data_access.DecodeError):
                data_access.quote("US:AAPL")

    def test_error_envelope_raises_dependency_error_even_on_http_error(self) -> None:
        failure = {
            "code": "dependency_failure",
            "message": "yahoo unavailable",
            "dependency": "yahoo",
            "retryable": True,
        }
        body = observation("error", None, error=failure)
        error = urllib.error.HTTPError(
            "http://facade/v1/quote", 502, "Bad Gateway", None, io.BytesIO(body)
        )
        with mock.patch.object(data_access.urllib.request, "urlopen", side_effect=error):
            with self.assertRaises(data_access.DependencyError) as raised:
                data_access.quote("US:AAPL")
        self.assertEqual(raised.exception.status, 502)
        self.assertEqual(raised.exception.failure.code, "dependency_failure")
        self.assertEqual(raised.exception.failure.dependency, "yahoo")

    def test_contract_rejects_version_status_fields_and_hash(self) -> None:
        cases = [
            observation("ok", [], version=2),
            observation("mystery", []),
            observation("ok", [], extra={"unexpected": True}),
        ]
        mismatched = json.loads(observation("ok", []))
        mismatched["raw_hash"] = "sha256:" + hashlib.sha256(b"[1]").hexdigest()
        cases.append(json.dumps(mismatched).encode())
        missing = json.loads(observation("ok", []))
        del missing["params"]
        cases.append(json.dumps(missing).encode())
        for body in cases:
            with self.subTest(body=body):
                with self.assertRaises(data_access.ContractError):
                    data_access.decode_observation(body)

    def test_http_200_error_envelope_still_raises(self) -> None:
        failure = {"code": "failed", "message": "no data", "retryable": False}
        body = observation("error", None, error=failure)
        with mock.patch.object(data_access.urllib.request, "urlopen", return_value=FakeResponse(body)):
            with self.assertRaises(data_access.DependencyError):
                data_access.history("US:AAPL")

    def test_go_wire_token_hash_cross_language_golden(self) -> None:
        fixtures = json.loads(HASH_GOLDEN.read_text())
        for fixture in fixtures:
            with self.subTest(name=fixture["name"]):
                body = (
                    '{"version":1,"status":"ok","provider":"golden","endpoint":"hash",'
                    '"params":{},"retrieved_at":"2026-07-20T08:00:00Z",'
                    f'"raw_hash":"{fixture["raw_hash"]}","data":{fixture["data_json"]},'
                    '"error":null}'
                ).encode()
                decoded = data_access.decode_observation(body)
                self.assertEqual(decoded.raw_hash, fixture["raw_hash"])

    def test_hash_uses_exact_data_wire_token(self) -> None:
        for data_token in (b"1e-07", b"[ 1 ]", b'"a\\/b"'):
            with self.subTest(data_token=data_token):
                raw_hash = "sha256:" + hashlib.sha256(data_token).hexdigest()
                body = (
                    b'{"version":1,"status":"ok","provider":"golden","endpoint":"hash",'
                    b'"params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"'
                    + raw_hash.encode()
                    + b'","data":'
                    + data_token
                    + b',"error":null}'
                )
                decoded = data_access.decode_observation(body)
                self.assertEqual(decoded.raw_hash, raw_hash)
                wrong_hash = body.replace(raw_hash.encode(), b"sha256:" + b"0" * 64)
                with self.assertRaises(data_access.ContractError):
                    data_access.decode_observation(wrong_hash)

    def test_nonfinite_numbers_are_rejected_at_any_depth(self) -> None:
        null_hash = b"sha256:" + hashlib.sha256(b"null").hexdigest().encode()
        empty_hash = b"sha256:" + hashlib.sha256(b"[]").hexdigest().encode()
        bodies = [
            b'{"version":1,"status":"ok","provider":"stub","endpoint":"hash",'
            b'"params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"'
            + b"sha256:" + hashlib.sha256(b"NaN").hexdigest().encode()
            + b'","data":NaN,"error":null}',
            b'{"version":1,"status":"ok","provider":"stub","endpoint":"hash",'
            b'"params":{"value":Infinity},"retrieved_at":"2026-07-20T08:00:00Z",'
            b'"raw_hash":"' + empty_hash + b'","data":[],"error":null}',
            b'{"version":1,"status":"error","provider":"stub","endpoint":"hash",'
            b'"params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"'
            + null_hash
            + b'","data":null,"error":{"code":"failed","message":"bad",'
            b'"retryable":-Infinity}}',
            b'{"version":1,"status":"ok","provider":"stub","endpoint":"hash",'
            b'"params":{},"retrieved_at":"2026-07-20T08:00:00Z","raw_hash":"'
            + b"sha256:" + hashlib.sha256(b"1e999").hexdigest().encode()
            + b'","data":1e999,"error":null}',
        ]
        for body in bodies:
            with self.subTest(body=body):
                with self.assertRaises(data_access.DecodeError):
                    data_access.decode_observation(body)

    def test_duplicate_top_level_data_is_rejected(self) -> None:
        body = observation("ok", [])
        duplicate = body[:-1] + b',"data":[]}'
        with self.assertRaises(data_access.ContractError):
            data_access.decode_observation(duplicate)

    def test_duplicate_nested_object_keys_are_rejected(self) -> None:
        failure = {
            "code": "failed", "message": "no data", "retryable": False,
        }
        cases = [
            observation("ok", [], extra=None).replace(
                b'"params":{"symbol":"US:EMPTY"}',
                b'"params":{"symbol":"US:EMPTY","symbol":"US:EMPTY"}',
            ),
            observation("ok", {"outer": {"value": 1}}).replace(
                b'"outer":{"value":1}', b'"outer":{"value":1,"value":1}',
            ),
            observation("error", None, error=failure).replace(
                b'"code":"failed"', b'"code":"failed","code":"failed"',
            ),
        ]
        for body in cases:
            with self.subTest(body=body):
                with self.assertRaisesRegex(data_access.ContractError, "duplicate"):
                    data_access.decode_observation(body)

    def test_arrays_and_escaped_keys_are_valid_json_contract(self) -> None:
        data = {"items": [{"a": 1}, {"b": 2}], 'quote"key': "value"}
        decoded = data_access.decode_observation(observation("ok", data))
        self.assertEqual(decoded.data, data)


class LegacyDataAccessTests(unittest.TestCase):
    def test_failures_keep_legacy_list_and_object_shapes(self) -> None:
        with mock.patch.object(legacy.urllib.request, "urlopen", side_effect=OSError("offline")):
            self.assertEqual(legacy.history("US:AAPL"), [])
            self.assertEqual(legacy.short("US:AAPL"), {})
            self.assertEqual(legacy.health(), {})
            self.assertEqual(legacy.news(symbol="US:AAPL"), [])

    def test_legacy_uses_unversioned_response_shape(self) -> None:
        body = json.dumps({"data": [{"symbol": "US:AAPL"}]}).encode()
        with mock.patch.object(legacy.urllib.request, "urlopen", return_value=FakeResponse(body)) as request:
            self.assertEqual(legacy.quote("US:AAPL"), [{"symbol": "US:AAPL"}])
        self.assertEqual(request.call_args.args[0], "http://127.0.0.1:8800/quote?symbols=US%3AAAPL")


if __name__ == "__main__":
    unittest.main()
