#!/usr/bin/env python3
"""Authenticated, bounded MCP access to the Compliance Ops application."""

from __future__ import annotations

import argparse
import ipaddress
import json
import mimetypes
import os
import re
import stat
import uuid
from dataclasses import dataclass, field
from datetime import date, datetime
from pathlib import Path, PurePath
from typing import Any, BinaryIO
from urllib.parse import quote, urlsplit

import httpx
from mcp.server.fastmcp import FastMCP
from pydantic import StrictBool

DEFAULT_BASE_URL = "http://127.0.0.1:3000"
DEFAULT_TOKEN_LABEL = "test-api-token"
DEFAULT_MAX_EVIDENCE_BYTES = 50 * 1024 * 1024
MAX_CONFIGURED_EVIDENCE_BYTES = 1024 * 1024 * 1024
MAX_TOKEN_FILE_BYTES = 1024 * 1024
MAX_TOKEN_ENTRIES = 100
MAX_TOKEN_LABEL_CHARS = 100
_ALLOWED_TOKEN_MODES = {0o400, 0o600, 0o440, 0o640}
_TOKEN_RE = re.compile(r"^[A-Za-z0-9\-._~+/]{12,}=*$")


class ConfigError(ValueError):
    """A safe configuration error that never includes secret material."""


@dataclass(frozen=True)
class Config:
    base_url: str
    token_file: Path
    token_label: str
    evidence_dir: Path
    max_evidence_bytes: int
    token: str = field(repr=False)


def _is_loopback(host: str | None) -> bool:
    if not host:
        return False
    if host.casefold() == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


def _parse_base_url(value: str) -> str:
    try:
        parsed = urlsplit(value)
        port = parsed.port
    except ValueError as exc:
        raise ConfigError("invalid Compliance Ops base URL") from exc
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise ConfigError("invalid Compliance Ops base URL")
    if parsed.username is not None or parsed.password is not None:
        raise ConfigError("base URL credentials are forbidden")
    if parsed.query or parsed.fragment:
        raise ConfigError("base URL query and fragment are forbidden")
    if parsed.scheme == "http" and not _is_loopback(parsed.hostname):
        raise ConfigError("plain HTTP is restricted to loopback hosts")
    if port is not None and not 1 <= port <= 65535:
        raise ConfigError("invalid Compliance Ops base URL")
    return value.rstrip("/")


def _default_token_file() -> Path | None:
    candidate = Path(__file__).resolve().parent.parent / ".env.tokens"
    return candidate if candidate.exists() else None


def _load_token(path: Path, label: str) -> str:
    try:
        info = path.lstat()
    except OSError as exc:
        raise ConfigError("token file is unavailable") from exc
    if not stat.S_ISREG(info.st_mode):
        raise ConfigError("token path must be a regular file")
    if info.st_size > MAX_TOKEN_FILE_BYTES:
        raise ConfigError("token file exceeds the configured byte limit")
    if stat.S_IMODE(info.st_mode) not in _ALLOWED_TOKEN_MODES:
        raise ConfigError("token file permissions must be 0400, 0600, 0440, or 0640")

    duplicate = False

    def pairs(items: list[tuple[str, Any]]) -> dict[str, Any]:
        nonlocal duplicate
        result: dict[str, Any] = {}
        for key, value in items:
            if key in result:
                duplicate = True
            result[key] = value
        return result

    try:
        raw = path.read_text(encoding="utf-8")
        data = json.loads(raw, object_pairs_hook=pairs)
    except (OSError, UnicodeError, json.JSONDecodeError) as exc:
        raise ConfigError("token file is not valid protected JSON") from exc
    if (
        duplicate
        or not isinstance(data, dict)
        or not data
        or len(data) > MAX_TOKEN_ENTRIES
    ):
        raise ConfigError("token file is not a valid label-to-token map")
    if any(
        not isinstance(key, str)
        or not key.strip()
        or len(key) > MAX_TOKEN_LABEL_CHARS
        or not isinstance(value, str)
        for key, value in data.items()
    ):
        raise ConfigError("token file contains invalid entries")
    if len(set(data.values())) != len(data) or any(
        len(value) < 12 or len(value) > 4096 or _TOKEN_RE.fullmatch(value) is None
        for value in data.values()
    ):
        raise ConfigError("token file contains invalid entries")
    token = data.get(label)
    if token is None:
        raise ConfigError("configured token label was not found")
    return token


def load_config() -> Config:
    base_url = _parse_base_url(os.environ.get("COMPLIANCE_OPS_BASE_URL", DEFAULT_BASE_URL))
    token_path_value = os.environ.get("COMPLIANCE_OPS_TOKEN_FILE")
    token_path = Path(token_path_value).expanduser() if token_path_value else _default_token_file()
    if token_path is None:
        raise ConfigError("COMPLIANCE_OPS_TOKEN_FILE is required")
    label = os.environ.get("COMPLIANCE_OPS_TOKEN_LABEL", DEFAULT_TOKEN_LABEL)
    if not label:
        raise ConfigError("COMPLIANCE_OPS_TOKEN_LABEL must not be empty")
    if len(label) > MAX_TOKEN_LABEL_CHARS:
        raise ConfigError("COMPLIANCE_OPS_TOKEN_LABEL must be at most 100 characters")
    evidence_value = os.environ.get("COMPLIANCE_OPS_EVIDENCE_DIR")
    if not evidence_value:
        raise ConfigError("COMPLIANCE_OPS_EVIDENCE_DIR is required")
    try:
        evidence_dir = Path(evidence_value).expanduser().resolve(strict=True)
    except OSError as exc:
        raise ConfigError("configured evidence directory is unavailable") from exc
    if not evidence_dir.is_dir():
        raise ConfigError("configured evidence directory must be a directory")
    raw_max = os.environ.get(
        "COMPLIANCE_OPS_MAX_EVIDENCE_BYTES", str(DEFAULT_MAX_EVIDENCE_BYTES)
    )
    try:
        max_bytes = int(raw_max, 10)
    except ValueError as exc:
        raise ConfigError("evidence byte limit must be a positive integer") from exc
    if not 1 <= max_bytes <= MAX_CONFIGURED_EVIDENCE_BYTES:
        raise ConfigError("evidence byte limit is out of range")
    token = _load_token(token_path, label)
    return Config(base_url, token_path, label, evidence_dir, max_bytes, token)


MAX_RESPONSE_BYTES = 2 * 1024 * 1024
REQUEST_TIMEOUT_SECONDS = 30.0
MAX_QUERY_CHARS = 500
MAX_IDENTIFIER_CHARS = 200
MAX_REQUIREMENT_IDS = 100
MAX_OFFSET = 1_000_000
STATUSES = {"implemented", "partial", "planned", "alternative", "not_applicable"}


class APIError(RuntimeError):
    """A sanitized application API error."""

    def __init__(self, status: int, code: str, message: str):
        self.status = status
        self.code = code
        self.message = message
        super().__init__(f"Compliance Ops API error: status={status} code={code} message={message}")


def _text(value: str, name: str, maximum: int, *, allow_empty: bool = False) -> str:
    if not isinstance(value, str) or (not allow_empty and not value.strip()):
        raise ValueError(f"{name} must not be blank")
    if len(value) > maximum:
        raise ValueError(f"{name} must be at most {maximum} characters")
    return value


def _identifier(value: str, name: str = "id") -> str:
    return _text(value, name, MAX_IDENTIFIER_CHARS)


def _timestamp(value: str) -> str:
    _text(value, "expected_updated_at", 100)
    normalized = value[:-1] + "+00:00" if value.endswith("Z") else value
    try:
        parsed = datetime.fromisoformat(normalized)
    except ValueError as exc:
        raise ValueError("expected_updated_at must be a nonblank RFC3339 timestamp") from exc
    if parsed.tzinfo is None or "T" not in value:
        raise ValueError("expected_updated_at must be a nonblank RFC3339 timestamp")
    return value


def _date(value: str, name: str) -> str:
    _text(value, name, 10)
    try:
        parsed = date.fromisoformat(value)
    except ValueError as exc:
        raise ValueError(f"{name} must be YYYY-MM-DD") from exc
    if parsed.isoformat() != value:
        raise ValueError(f"{name} must be YYYY-MM-DD")
    return value


def _page(limit: int, offset: int) -> None:
    if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 200:
        raise ValueError("limit must be between 1 and 200")
    if isinstance(offset, bool) or not isinstance(offset, int) or not 0 <= offset <= MAX_OFFSET:
        raise ValueError(f"offset must be between 0 and {MAX_OFFSET}")


def _ids(values: list[str]) -> list[str]:
    if not isinstance(values, list) or not 1 <= len(values) <= MAX_REQUIREMENT_IDS:
        raise ValueError(f"ids must contain between 1 and {MAX_REQUIREMENT_IDS} entries")
    return [_identifier(item, "id") for item in values]


def _scope_ids(values: list[str]) -> list[str]:
    if not isinstance(values, list) or len(values) > MAX_REQUIREMENT_IDS:
        raise ValueError(
            f"scope category ids must contain at most {MAX_REQUIREMENT_IDS} entries"
        )
    result = [_identifier(item, "scope category id") for item in values]
    for item in result:
        try:
            uuid.UUID(item)
        except ValueError as exc:
            raise ValueError("scope category ids must be UUIDs") from exc
    return result


def _link_url(value: str) -> str:
    _text(value, "url", 2048)
    try:
        parsed = urlsplit(value)
        parsed.port
    except ValueError as exc:
        raise ValueError("url must be a valid HTTP(S) URL") from exc
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise ValueError("url must be a valid HTTP(S) URL")
    if parsed.username is not None or parsed.password is not None:
        raise ValueError("URL credentials are forbidden")
    return value


class _LimitedFile:
    def __init__(self, file: BinaryIO, limit: int):
        self._file = file
        self._limit = limit
        self._sent = 0

    def read(self, size: int = -1) -> bytes:
        data = self._file.read(size)
        self._sent += len(data)
        if self._sent > self._limit:
            raise ValueError("evidence file exceeds the configured byte limit")
        return data

    def seek(self, offset: int, whence: int = 0) -> int:
        result = self._file.seek(offset, whence)
        if result == 0:
            self._sent = 0
        return result

    def tell(self) -> int:
        return self._file.tell()

    def fileno(self) -> int:
        return self._file.fileno()


def _open_evidence(root: Path, relative: str, limit: int) -> tuple[BinaryIO, str]:
    _text(relative, "relative_path", 1024)
    path = PurePath(relative)
    if path.is_absolute() or not path.parts or any(part in {".", "..", ""} for part in path.parts):
        raise ValueError("evidence path must be a relative path beneath the configured directory")
    directory_flags = os.O_RDONLY | getattr(os, "O_DIRECTORY", 0) | getattr(os, "O_NOFOLLOW", 0)
    file_flags = os.O_RDONLY | getattr(os, "O_NOFOLLOW", 0)
    descriptors: list[int] = []
    try:
        current = os.open(root, directory_flags)
        descriptors.append(current)
        for component in path.parts[:-1]:
            current = os.open(component, directory_flags, dir_fd=current)
            descriptors.append(current)
        fd = os.open(path.parts[-1], file_flags, dir_fd=current)
        info = os.fstat(fd)
        if not stat.S_ISREG(info.st_mode):
            os.close(fd)
            raise ValueError("evidence path must name a regular file")
        if info.st_size > limit:
            os.close(fd)
            raise ValueError("evidence file exceeds the configured byte limit")
        return os.fdopen(fd, "rb"), path.name
    except (OSError, ValueError) as exc:
        if isinstance(exc, ValueError):
            raise
        raise ValueError("evidence path is unavailable or contains a symlink") from exc
    finally:
        for descriptor in reversed(descriptors):
            os.close(descriptor)


def _contains_token(value: Any, token: str) -> bool:
    if isinstance(value, str):
        return token in value
    if isinstance(value, dict):
        return any(
            _contains_token(key, token) or _contains_token(item, token)
            for key, item in value.items()
        )
    if isinstance(value, list):
        return any(_contains_token(item, token) for item in value)
    return False


class ComplianceOpsClient:
    def __init__(self, config: Config):
        self.config = config

    async def _request(
        self,
        method: str,
        path: str,
        *,
        params: list[tuple[str, str]] | None = None,
        payload: dict[str, Any] | None = None,
        data: dict[str, str] | None = None,
        files: dict[str, tuple[str, Any, str]] | None = None,
    ) -> str:
        headers = {"Authorization": f"Bearer {self.config.token}", "Accept": "application/json"}
        kwargs: dict[str, Any] = {"headers": headers, "params": params}
        if payload is not None:
            kwargs["json"] = payload
        if data is not None:
            kwargs["data"] = data
        if files is not None:
            kwargs["files"] = files
        try:
            async with httpx.AsyncClient(timeout=REQUEST_TIMEOUT_SECONDS) as client:
                async with client.stream(method, self.config.base_url + path, **kwargs) as response:
                    body = bytearray()
                    async for chunk in response.aiter_bytes():
                        if len(body) + len(chunk) > MAX_RESPONSE_BYTES:
                            raise APIError(response.status_code, "RESPONSE_TOO_LARGE", "response exceeds the configured byte limit")
                        body.extend(chunk)
                    status = response.status_code
        except APIError:
            raise
        except (httpx.HTTPError, OSError) as exc:
            raise APIError(0, "NETWORK_ERROR", "Compliance Ops API request failed") from exc
        if status == 204:
            return json.dumps({"deleted": True})
        try:
            decoded = json.loads(body)
        except (UnicodeError, json.JSONDecodeError):
            raise APIError(status, "INVALID_RESPONSE", "API returned a non-JSON response") from None
        if not 200 <= status < 300:
            code = decoded.get("code") if isinstance(decoded, dict) else None
            message = decoded.get("message") if isinstance(decoded, dict) else None
            safe_code = code if isinstance(code, str) and len(code) <= 100 else "HTTP_ERROR"
            safe_message = message if isinstance(message, str) and len(message) <= 1000 else "request failed"
            safe_code = safe_code.replace(self.config.token, "[redacted]")
            safe_message = safe_message.replace(self.config.token, "[redacted]")
            raise APIError(status, safe_code, safe_message)
        if not isinstance(decoded, (dict, list)):
            raise APIError(status, "INVALID_RESPONSE", "API response must be a JSON object or array")
        if _contains_token(decoded, self.config.token):
            raise APIError(
                status,
                "TOKEN_REFLECTION",
                "API response contained protected credential material",
            )
        return json.dumps(decoded, separators=(",", ":"))

    async def get_dashboard(self) -> str:
        return await self._request("GET", "/v1/dashboard")

    async def list_frameworks(self) -> str:
        return await self._request("GET", "/v1/frameworks")

    async def list_requirements(self, framework_id: str | None = None, status: str | None = None, query: str | None = None, scope_category_ids: list[str] | None = None, limit: int = 50, offset: int = 0) -> str:
        _page(limit, offset)
        params: list[tuple[str, str]] = []
        if framework_id is not None:
            params.append(("frameworkId", _identifier(framework_id, "framework_id")))
        if status is not None:
            if status not in STATUSES:
                raise ValueError("invalid requirement status")
            params.append(("status", status))
        if query is not None:
            params.append(("q", _text(query, "query", MAX_QUERY_CHARS)))
        if scope_category_ids is not None:
            if len(scope_category_ids) > MAX_REQUIREMENT_IDS:
                raise ValueError(f"scope_category_ids must contain at most {MAX_REQUIREMENT_IDS} entries")
            params.extend(("scopeCategoryId", _identifier(item, "scope_category_id")) for item in scope_category_ids)
        params.extend((("limit", str(limit)), ("offset", str(offset))))
        return await self._request("GET", "/v1/requirements", params=params)

    async def get_requirement(self, requirement_id: str) -> str:
        value = quote(_identifier(requirement_id, "requirement_id"), safe="")
        return await self._request("GET", f"/v1/requirements/{value}")

    async def list_scope_categories(self, query: str | None = None, limit: int = 100) -> str:
        if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 200:
            raise ValueError("limit must be between 1 and 200")
        params = [] if query is None else [("q", _text(query, "query", MAX_QUERY_CHARS))]
        params.append(("limit", str(limit)))
        return await self._request("GET", "/v1/scope-categories", params=params)

    async def list_evidence(self, requirement_id: str | None = None, kind: str | None = None, query: str | None = None, limit: int = 50, offset: int = 0) -> str:
        _page(limit, offset)
        params: list[tuple[str, str]] = []
        if requirement_id is not None:
            params.append(("requirementId", _identifier(requirement_id, "requirement_id")))
        if kind is not None:
            if kind not in {"file", "link"}:
                raise ValueError("kind must be file or link")
            params.append(("kind", kind))
        if query is not None:
            params.append(("q", _text(query, "query", MAX_QUERY_CHARS)))
        params.extend((("limit", str(limit)), ("offset", str(offset))))
        return await self._request("GET", "/v1/evidence", params=params)

    async def update_part(self, requirement_id: str, part_id: str, expected_updated_at: str | None, status: str | None = None, owner: str | None = None, due_date: str | None = None, clear_due_date: bool = False, description: str | None = None, scope_category_ids: list[str] | None = None) -> str:
        if type(clear_due_date) is not bool:
            raise ValueError("clear_due_date must be a boolean")
        if due_date is not None and clear_due_date:
            raise ValueError("due_date and clear_due_date are mutually exclusive")
        payload: dict[str, Any] = {"expectedUpdatedAt": None if expected_updated_at is None else _timestamp(expected_updated_at)}
        if status is not None:
            if status not in STATUSES:
                raise ValueError("invalid requirement status")
            payload["status"] = status
        if owner is not None:
            payload["owner"] = _text(owner, "owner", 200, allow_empty=True)
        if due_date is not None:
            payload["dueDate"] = _date(due_date, "due_date")
        if clear_due_date:
            payload["dueDate"] = None
        if description is not None:
            payload["description"] = _text(description, "description", 20000, allow_empty=True)
        if scope_category_ids is not None:
            payload["scopeCategoryIds"] = _scope_ids(scope_category_ids)
        if len(payload) == 1:
            raise ValueError("update_part requires at least one changed field")
        rid = quote(_identifier(requirement_id, "requirement_id"), safe="")
        pid = quote(_identifier(part_id, "part_id"), safe="")
        return await self._request("PATCH", f"/v1/requirements/{rid}/parts/{pid}", payload=payload)

    async def update_requirement_notes(self, requirement_id: str, notes: str, expected_updated_at: str) -> str:
        payload = {"notes": _text(notes, "notes", 10000, allow_empty=True), "expectedUpdatedAt": _timestamp(expected_updated_at)}
        rid = quote(_identifier(requirement_id, "requirement_id"), safe="")
        return await self._request("PATCH", f"/v1/requirements/{rid}", payload=payload)

    async def set_requirement_status_override(self, requirement_id: str, status: str | None, expected_updated_at: str) -> str:
        if status is not None and status not in STATUSES:
            raise ValueError("invalid requirement status")
        payload = {"statusOverride": status, "expectedUpdatedAt": _timestamp(expected_updated_at)}
        rid = quote(_identifier(requirement_id, "requirement_id"), safe="")
        return await self._request("PATCH", f"/v1/requirements/{rid}", payload=payload)

    async def create_scope_category(self, name: str) -> str:
        return await self._request("POST", "/v1/scope-categories", payload={"name": _text(name, "name", 100)})

    async def update_scope_category(self, category_id: str, name: str, expected_updated_at: str) -> str:
        cid = quote(_identifier(category_id, "category_id"), safe="")
        payload = {"name": _text(name, "name", 100), "expectedUpdatedAt": _timestamp(expected_updated_at)}
        return await self._request("PATCH", f"/v1/scope-categories/{cid}", payload=payload)

    async def delete_scope_category(self, category_id: str, expected_updated_at: str) -> str:
        cid = quote(_identifier(category_id, "category_id"), safe="")
        params = [("expectedUpdatedAt", _timestamp(expected_updated_at))]
        return await self._request("DELETE", f"/v1/scope-categories/{cid}", params=params)

    async def create_link_evidence(self, url: str, title: str, requirement_ids: list[str], description: str | None = None, valid_from: str | None = None, valid_until: str | None = None) -> str:
        payload: dict[str, Any] = {"kind": "link", "url": _link_url(url), "title": _text(title, "title", 200), "requirementIds": _ids(requirement_ids)}
        if description is not None:
            payload["description"] = _text(description, "description", 10000, allow_empty=True)
        if valid_from is not None:
            payload["validFrom"] = _date(valid_from, "valid_from")
        if valid_until is not None:
            payload["validUntil"] = _date(valid_until, "valid_until")
        if valid_from is not None and valid_until is not None and valid_until < valid_from:
            raise ValueError("valid_until must not precede valid_from")
        return await self._request("POST", "/v1/evidence", payload=payload)

    async def create_file_evidence(self, relative_path: str, title: str, requirement_ids: list[str], description: str | None = None, valid_from: str | None = None, valid_until: str | None = None) -> str:
        title = _text(title, "title", 200)
        ids = _ids(requirement_ids)
        fields = {"title": title, "requirementIds": ",".join(ids)}
        if description is not None:
            fields["description"] = _text(description, "description", 10000, allow_empty=True)
        if valid_from is not None:
            fields["validFrom"] = _date(valid_from, "valid_from")
        if valid_until is not None:
            fields["validUntil"] = _date(valid_until, "valid_until")
        if valid_from is not None and valid_until is not None and valid_until < valid_from:
            raise ValueError("valid_until must not precede valid_from")
        file, filename = _open_evidence(self.config.evidence_dir, relative_path, self.config.max_evidence_bytes)
        try:
            limited = _LimitedFile(file, self.config.max_evidence_bytes)
            content_type = mimetypes.guess_type(filename)[0] or "application/octet-stream"
            return await self._request("POST", "/v1/evidence", data=fields, files={"file": (filename, limited, content_type)})
        finally:
            file.close()


async def _call(method: str, *args: Any, **kwargs: Any) -> str:
    return await getattr(ComplianceOpsClient(load_config()), method)(*args, **kwargs)


mcp = FastMCP("compliance-ops")


@mcp.tool()
async def get_dashboard() -> str:
    """Get the bounded compliance dashboard."""
    return await _call("get_dashboard")


@mcp.tool()
async def list_frameworks() -> str:
    """List imported compliance frameworks."""
    return await _call("list_frameworks")


@mcp.tool()
async def list_requirements(framework_id: str | None = None, status: str | None = None, query: str | None = None, scope_category_ids: list[str] | None = None, limit: int = 50, offset: int = 0) -> str:
    """List requirements using bounded application filters and pagination."""
    return await _call("list_requirements", framework_id, status, query, scope_category_ids, limit, offset)


@mcp.tool()
async def get_requirement(requirement_id: str) -> str:
    """Get one requirement with parts, tracking, and evidence metadata."""
    return await _call("get_requirement", requirement_id)


@mcp.tool()
async def list_scope_categories(query: str | None = None, limit: int = 100) -> str:
    """List reusable scope categories."""
    return await _call("list_scope_categories", query, limit)


@mcp.tool()
async def list_evidence(requirement_id: str | None = None, kind: str | None = None, query: str | None = None, limit: int = 50, offset: int = 0) -> str:
    """List bounded evidence metadata; never downloads file contents."""
    return await _call("list_evidence", requirement_id, kind, query, limit, offset)


@mcp.tool()
async def update_part(requirement_id: str, part_id: str, expected_updated_at: str | None, status: str | None = None, owner: str | None = None, due_date: str | None = None, clear_due_date: StrictBool = False, description: str | None = None, scope_category_ids: list[str] | None = None) -> str:
    """Update part tracking; clear_due_date clears the date and empty scope IDs clear scopes."""
    return await _call("update_part", requirement_id, part_id, expected_updated_at, status, owner, due_date, clear_due_date, description, scope_category_ids)


@mcp.tool()
async def update_requirement_notes(requirement_id: str, notes: str, expected_updated_at: str) -> str:
    """Update requirement notes with a mandatory optimistic precondition."""
    return await _call("update_requirement_notes", requirement_id, notes, expected_updated_at)


@mcp.tool()
async def set_requirement_status_override(requirement_id: str, status: str | None, expected_updated_at: str) -> str:
    """Set or clear a status override with a mandatory optimistic precondition."""
    return await _call("set_requirement_status_override", requirement_id, status, expected_updated_at)


@mcp.tool()
async def create_scope_category(name: str) -> str:
    """Create a reusable scope category with the supplied name."""
    return await _call("create_scope_category", name)


@mcp.tool()
async def update_scope_category(category_id: str, name: str, expected_updated_at: str) -> str:
    """Rename a scope category with a mandatory optimistic precondition."""
    return await _call("update_scope_category", category_id, name, expected_updated_at)


@mcp.tool()
async def delete_scope_category(category_id: str, expected_updated_at: str) -> str:
    """Delete an unused scope category with a mandatory optimistic precondition."""
    return await _call("delete_scope_category", category_id, expected_updated_at)


@mcp.tool()
async def create_link_evidence(url: str, title: str, requirement_ids: list[str], description: str | None = None, valid_from: str | None = None, valid_until: str | None = None) -> str:
    """Create HTTP(S) link evidence linked to explicit requirement IDs."""
    return await _call("create_link_evidence", url, title, requirement_ids, description, valid_from, valid_until)


@mcp.tool()
async def create_file_evidence(relative_path: str, title: str, requirement_ids: list[str], description: str | None = None, valid_from: str | None = None, valid_until: str | None = None) -> str:
    """Stream one allowlisted relative regular file as evidence; never returns file contents."""
    return await _call("create_file_evidence", relative_path, title, requirement_ids, description, valid_from, valid_until)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--transport", choices=("stdio", "streamable-http"), default="stdio")
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args()
    if not 1 <= args.port <= 65535:
        parser.error("--port must be between 1 and 65535")
    if args.transport == "streamable-http" and not _is_loopback(args.host):
        parser.error("Streamable HTTP may bind only to a loopback address")
    load_config()
    mcp.settings.host = args.host
    mcp.settings.port = args.port
    mcp.run(transport=args.transport)


if __name__ == "__main__":
    main()
