"""Strict Tier-2 SDK for the versioned Nimbus data-access contract.

The top-level package calls only ``/v1`` routes and never converts a transport,
HTTP, decode, contract, or dependency failure into an empty value. Existing
callers that intentionally need the old weak-dependency behavior must import
``data_access.legacy`` explicitly.
"""
from __future__ import annotations

import builtins
import hashlib
import json
import math
import os
import socket
import urllib.error
import urllib.parse
import urllib.request
import warnings
from dataclasses import dataclass as _dataclass
from datetime import datetime
from typing import Any

FACADE = os.environ.get("DATA_FACADE_URL", "http://127.0.0.1:8800").rstrip("/")
TIMEOUT = float(os.environ.get("DATA_FACADE_TIMEOUT", "20"))
OBSERVATION_VERSION = 1
OBSERVATION_MEDIA_TYPE = "application/vnd.nimbus.observation+json; version=1"
_ENVELOPE_FIELDS = {
    "version", "status", "provider", "endpoint", "params",
    "retrieved_at", "raw_hash", "data", "error",
}
_ERROR_FIELDS = {"code", "message", "dependency", "retryable"}

__all__ = [
    "ContractError", "DataAccessError", "DecodeError", "DegradedWarning",
    "DependencyError", "HTTPError", "Observation", "ObservationFailure",
    "TimeoutError", "TransportError", "history", "quote",
]


class DataAccessError(Exception):
    """Base class for strict SDK failures."""

    def __init__(self, message: str, *, url: str = "") -> None:
        super().__init__(message)
        self.url = url


class TransportError(DataAccessError):
    """The facade could not be reached or the response body could not be read."""


class TimeoutError(DataAccessError, builtins.TimeoutError):
    """The strict request exceeded its configured timeout."""


class HTTPError(DataAccessError):
    """The facade returned a non-success response without a valid error envelope."""

    def __init__(self, status: int, message: str, *, url: str, body: bytes = b"") -> None:
        super().__init__(message, url=url)
        self.status = status
        self.body = body


class DecodeError(DataAccessError):
    """The facade response was not valid JSON."""


class ContractError(DataAccessError):
    """The JSON response violated ObservationEnvelope v1."""


@_dataclass(frozen=True)
class ObservationFailure:
    code: str
    message: str
    dependency: str | None
    retryable: bool


@_dataclass(frozen=True)
class Observation:
    version: int
    status: str
    provider: str
    endpoint: str
    params: dict[str, Any]
    retrieved_at: str
    raw_hash: str
    data: Any
    error: ObservationFailure | None


class DependencyError(DataAccessError):
    """A valid error observation reported that no usable data exists."""

    def __init__(self, observation: Observation, *, url: str, status: int | None = None) -> None:
        failure = observation.error
        assert failure is not None
        super().__init__(failure.message, url=url)
        self.observation = observation
        self.failure = failure
        self.status = status


class DegradedWarning(UserWarning):
    """Usable data was returned with a structured dependency warning."""

    def __init__(self, observation: Observation) -> None:
        failure = observation.error
        assert failure is not None
        super().__init__(failure.message)
        self.observation = observation
        self.failure = failure


def _reject_duplicate_pairs(pairs: list[tuple[str, Any]]) -> dict[str, Any]:
    value: dict[str, Any] = {}
    for key, item in pairs:
        if key in value:
            raise ContractError(f"duplicate observation object key: {key}")
        value[key] = item
    return value


def _reject_nonfinite_constant(value: str) -> Any:
    raise DecodeError(f"invalid observation JSON: non-finite number {value}")


def _parse_finite_float(value: str) -> float:
    parsed = float(value)
    if not math.isfinite(parsed):
        raise DecodeError(f"invalid observation JSON: non-finite number {value}")
    return parsed


def _decode_envelope_json(payload: bytes | str) -> tuple[Any, bytes | None]:
    """Decode one JSON value while preserving the exact top-level data token.

    Observation raw_hash is over encoding/json's wire token, not a Python
    re-serialization. The stdlib decoder supplies reliable token boundaries;
    slicing the original text retains exponent spelling, escapes, and key order.
    """
    try:
        text = payload.decode("utf-8") if isinstance(payload, bytes) else payload
    except UnicodeError as exc:
        raise DecodeError(f"invalid observation JSON: {exc}") from exc
    if not isinstance(text, str):
        raise DecodeError("invalid observation JSON: payload must be bytes or string")

    # object_pairs_hook runs for every nested object in params/data/error while
    # the manual outer-object walk below also preserves the exact data span.
    decoder = json.JSONDecoder(
        object_pairs_hook=_reject_duplicate_pairs,
        parse_constant=_reject_nonfinite_constant,
        parse_float=_parse_finite_float,
    )
    length = len(text)

    def skip_ws(index: int) -> int:
        while index < length and text[index] in " \t\r\n":
            index += 1
        return index

    index = skip_ws(0)
    try:
        if index >= length or text[index] != "{":
            value, end = decoder.raw_decode(text, index)
            if skip_ws(end) != length:
                raise json.JSONDecodeError("extra data", text, end)
            return value, None

        index += 1
        value: dict[str, Any] = {}
        data_raw: bytes | None = None
        index = skip_ws(index)
        if index < length and text[index] == "}":
            index += 1
        else:
            while True:
                index = skip_ws(index)
                key, end = decoder.raw_decode(text, index)
                if not isinstance(key, str):
                    raise json.JSONDecodeError("object key must be string", text, index)
                if key in value:
                    raise ContractError(f"duplicate observation field: {key}")
                index = skip_ws(end)
                if index >= length or text[index] != ":":
                    raise json.JSONDecodeError("expected ':'", text, index)
                item_start = skip_ws(index + 1)
                item, item_end = decoder.raw_decode(text, item_start)
                value[key] = item
                if key == "data":
                    data_raw = text[item_start:item_end].encode("utf-8")
                index = skip_ws(item_end)
                if index < length and text[index] == ",":
                    index += 1
                    continue
                if index < length and text[index] == "}":
                    index += 1
                    break
                raise json.JSONDecodeError("expected ',' or '}'", text, index)
        if skip_ws(index) != length:
            raise json.JSONDecodeError("extra data", text, index)
        return value, data_raw
    except json.JSONDecodeError as exc:
        raise DecodeError(f"invalid observation JSON: {exc}") from exc


def decode_observation(payload: bytes | str) -> Observation:
    """Decode and validate one ObservationEnvelope v1 payload."""
    value, data_raw = _decode_envelope_json(payload)
    if not isinstance(value, dict):
        raise ContractError("observation envelope must be an object")
    fields = set(value)
    if fields != _ENVELOPE_FIELDS:
        missing = sorted(_ENVELOPE_FIELDS - fields)
        unknown = sorted(fields - _ENVELOPE_FIELDS)
        details = []
        if missing:
            details.append(f"missing fields: {', '.join(missing)}")
        if unknown:
            details.append(f"unknown fields: {', '.join(unknown)}")
        raise ContractError("observation envelope fields invalid: " + "; ".join(details))

    version = value["version"]
    if isinstance(version, bool) or not isinstance(version, int) or version != OBSERVATION_VERSION:
        raise ContractError(f"observation version must be {OBSERVATION_VERSION}")
    status = value["status"]
    if not isinstance(status, str) or status not in {"ok", "empty", "degraded", "error"}:
        raise ContractError(f"unsupported observation status {status!r}")
    provider = _required_text(value["provider"], "provider")
    endpoint = _required_text(value["endpoint"], "endpoint")
    params = value["params"]
    if not isinstance(params, dict):
        raise ContractError("observation params must be an object")
    retrieved_at = _validate_timestamp(value["retrieved_at"])
    raw_hash = value["raw_hash"]
    if not isinstance(raw_hash, str) or not _valid_raw_hash(raw_hash):
        raise ContractError("observation raw_hash must be a lowercase sha256 digest")

    data = value["data"]
    failure = _decode_failure(value["error"])
    if status in {"ok", "empty"}:
        if data is None:
            raise ContractError(f"observation status {status!r} requires non-null data")
        if failure is not None:
            raise ContractError(f"observation status {status!r} requires null error")
    elif status == "degraded":
        if data is None:
            raise ContractError("degraded observation requires non-null data")
        if failure is None:
            raise ContractError("degraded observation requires a structured error")
    else:
        if data is not None:
            raise ContractError("error observation requires null data")
        if failure is None:
            raise ContractError("error observation requires a structured error")

    assert data_raw is not None  # exact field check above guarantees presence
    expected_hash = "sha256:" + hashlib.sha256(data_raw).hexdigest()
    if raw_hash != expected_hash:
        raise ContractError("observation raw_hash does not match data")
    return Observation(
        version=version,
        status=status,
        provider=provider,
        endpoint=endpoint,
        params=params,
        retrieved_at=retrieved_at,
        raw_hash=raw_hash,
        data=data,
        error=failure,
    )


def _observe(path: str, **params: Any) -> Observation:
    """Fetch one strict route and return the validated observation."""
    clean_path = "/" + path.lstrip("/")
    if not clean_path.startswith("/v1/"):
        clean_path = "/v1" + clean_path
    query = urllib.parse.urlencode({key: value for key, value in params.items() if value is not None})
    url = f"{FACADE}{clean_path}" + (f"?{query}" if query else "")
    request = urllib.request.Request(url, headers={"Accept": OBSERVATION_MEDIA_TYPE})
    http_status: int | None = None
    body: bytes
    try:
        with urllib.request.urlopen(request, timeout=TIMEOUT) as response:  # noqa: S310
            http_status = response.getcode()
            body = _read_body(response, url)
    except urllib.error.HTTPError as exc:
        http_status = exc.code
        body = _read_body(exc, url)
    except (builtins.TimeoutError, socket.timeout) as exc:
        raise TimeoutError(f"data-access request timed out: {url}", url=url) from exc
    except urllib.error.URLError as exc:
        if isinstance(exc.reason, (builtins.TimeoutError, socket.timeout)):
            raise TimeoutError(f"data-access request timed out: {url}", url=url) from exc
        raise TransportError(f"data-access transport failure: {exc.reason}", url=url) from exc
    except OSError as exc:
        raise TransportError(f"data-access transport failure: {exc}", url=url) from exc

    if http_status is not None and not 200 <= http_status < 300:
        try:
            observation = decode_observation(body)
        except (DecodeError, ContractError):
            raise HTTPError(
                http_status,
                f"data-access returned HTTP {http_status}",
                url=url,
                body=body,
            )
        if observation.status == "error":
            raise DependencyError(observation, url=url, status=http_status)
        raise HTTPError(
            http_status,
            f"data-access returned HTTP {http_status} with status {observation.status!r}",
            url=url,
            body=body,
        )

    observation = decode_observation(body)
    if observation.status == "error":
        raise DependencyError(observation, url=url, status=http_status)
    return observation


def _get(path: str, **params: Any) -> Any:
    observation = _observe(path, **params)
    if observation.status == "degraded":
        warnings.warn(DegradedWarning(observation), stacklevel=2)
    return observation.data


def _read_body(response: Any, url: str) -> bytes:
    try:
        return response.read()
    except (builtins.TimeoutError, socket.timeout) as exc:
        raise TimeoutError(f"data-access response timed out: {url}", url=url) from exc
    except OSError as exc:
        raise TransportError(f"data-access response read failure: {exc}", url=url) from exc


def _required_text(value: Any, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise ContractError(f"observation {field} is required")
    return value


def _validate_timestamp(value: Any) -> str:
    if not isinstance(value, str):
        raise ContractError("observation retrieved_at must be RFC3339")
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as exc:
        raise ContractError("observation retrieved_at must be RFC3339") from exc
    if parsed.tzinfo is None:
        raise ContractError("observation retrieved_at must include a timezone")
    return value


def _decode_failure(value: Any) -> ObservationFailure | None:
    if value is None:
        return None
    if not isinstance(value, dict):
        raise ContractError("observation error must be an object or null")
    fields = set(value)
    required = {"code", "message", "retryable"}
    if not required.issubset(fields) or not fields.issubset(_ERROR_FIELDS):
        raise ContractError("observation error fields are invalid")
    code = _required_text(value["code"], "error.code")
    message = _required_text(value["message"], "error.message")
    retryable = value["retryable"]
    if not isinstance(retryable, bool):
        raise ContractError("observation error.retryable must be boolean")
    dependency = value.get("dependency")
    if dependency is not None:
        dependency = _required_text(dependency, "error.dependency")
    return ObservationFailure(code=code, message=message, dependency=dependency, retryable=retryable)


def _valid_raw_hash(value: str) -> bool:
    if not value.startswith("sha256:") or len(value) != 71:
        return False
    digest = value[7:]
    return digest == digest.lower() and all(char in "0123456789abcdef" for char in digest)


def quote(*symbols: str) -> list[dict]:
    return _get("/quote", symbols=",".join(symbols))


def history(symbol: str, from_: str | None = None, to: str | None = None, limit: int | None = None) -> list[dict]:
    return _get("/history", symbol=symbol, **{"from": from_}, to=to, limit=limit)
