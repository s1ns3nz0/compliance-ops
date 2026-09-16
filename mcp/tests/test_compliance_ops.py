import asyncio
import json
import os
import sys
import subprocess
import tempfile
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from unittest.mock import AsyncMock, patch

from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.server.fastmcp.exceptions import ToolError

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import compliance_ops


class ConfigTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.root = Path(self.temp.name)
        self.evidence = self.root / "evidence"
        self.evidence.mkdir()
        self.token_file = self.root / "tokens.json"

    def tearDown(self):
        self.temp.cleanup()

    def write_tokens(self, text='{"test-api-token":"abcdefghijklmnop"}'):
        self.token_file.write_text(text)
        self.token_file.chmod(0o600)

    def env(self, **updates):
        values = {
            "COMPLIANCE_OPS_BASE_URL": "http://127.0.0.1:3000",
            "COMPLIANCE_OPS_TOKEN_FILE": str(self.token_file),
            "COMPLIANCE_OPS_TOKEN_LABEL": "test-api-token",
            "COMPLIANCE_OPS_EVIDENCE_DIR": str(self.evidence),
        }
        values.update(updates)
        return patch.dict(os.environ, values, clear=True)

    def test_loads_exact_named_token_without_exposing_it_in_repr(self):
        self.write_tokens()
        with self.env():
            config = compliance_ops.load_config()
        self.assertEqual(config.token, "abcdefghijklmnop")
        self.assertNotIn(config.token, repr(config))

    def test_rejects_duplicate_labels_trailing_json_and_malformed_values_secretly(self):
        bad = [
            '{"same":"abcdefghijklmnop","same":"qrstuvwxyzABCDEF"}',
            '{"test-api-token":"abcdefghijklmnop"} trailing',
            '{"test-api-token":"short"}',
            '{"test-api-token":"abcdefghijklmnop","two":"abcdefghijklmnop"}',
            '{"test-api-token":"abcdefghijklmnop","":"qrstuvwxyzABCDEF"}',
        ]
        for text in bad:
            with self.subTest(text=text):
                self.write_tokens(text)
                with self.env(), self.assertRaises(compliance_ops.ConfigError) as caught:
                    compliance_ops.load_config()
                message = str(caught.exception)
                self.assertNotIn("same", message)
                self.assertNotIn("test-api-token", message)
                self.assertNotIn("abcdefghijklmnop", message)

    def test_rejects_empty_token_map_secretly(self):
        self.write_tokens("{}")

        with self.env(), self.assertRaises(compliance_ops.ConfigError) as caught:
            compliance_ops.load_config()

        message = str(caught.exception)
        self.assertEqual(message, "token file is not a valid label-to-token map")
        self.assertNotIn(str(self.token_file), message)
        self.assertNotIn("test-api-token", message)

    def test_rejects_more_than_one_hundred_token_entries_secretly(self):
        entries = {"test-api-token": "abcdefghijklmnop"}
        entries.update(
            {f"label-{index}": f"token-value-{index:04d}" for index in range(100)}
        )
        self.write_tokens(json.dumps(entries))

        with self.env(), self.assertRaises(compliance_ops.ConfigError) as caught:
            compliance_ops.load_config()

        message = str(caught.exception)
        self.assertEqual(message, "token file is not a valid label-to-token map")
        self.assertNotIn("label-0", message)
        self.assertNotIn("token-value-0000", message)
        self.assertNotIn("test-api-token", message)

    def test_rejects_token_labels_longer_than_one_hundred_characters_secretly(self):
        long_label = "x" * 101
        self.write_tokens(
            json.dumps(
                {
                    "test-api-token": "abcdefghijklmnop",
                    long_label: "qrstuvwxyzABCDEF",
                }
            )
        )

        with self.env(), self.assertRaises(compliance_ops.ConfigError) as caught:
            compliance_ops.load_config()

        message = str(caught.exception)
        self.assertEqual(message, "token file contains invalid entries")
        self.assertNotIn(long_label, message)
        self.assertNotIn("abcdefghijklmnop", message)
        self.assertNotIn(str(self.token_file), message)

    def test_rejects_overlong_configured_token_label_before_lookup(self):
        self.write_tokens()

        with self.env(COMPLIANCE_OPS_TOKEN_LABEL="x" * 101), self.assertRaises(
            compliance_ops.ConfigError
        ) as caught:
            compliance_ops.load_config()

        message = str(caught.exception)
        self.assertEqual(message, "COMPLIANCE_OPS_TOKEN_LABEL must be at most 100 characters")
        self.assertNotIn("x" * 101, message)
        self.assertNotIn("abcdefghijklmnop", message)
        self.assertNotIn(str(self.token_file), message)

    def test_token_requires_twelve_nonpadding_characters(self):
        for token in ("a===========", "abcdefghijk="):
            with self.subTest(token=token):
                self.write_tokens(json.dumps({"test-api-token": token}))
                with self.env(), self.assertRaises(compliance_ops.ConfigError):
                    compliance_ops.load_config()

        for token in ("abcdefghijkl", "abcdefghijkl="):
            with self.subTest(token=token):
                self.write_tokens(json.dumps({"test-api-token": token}))
                with self.env():
                    config = compliance_ops.load_config()
                self.assertEqual(config.token, token)

    def test_token_length_boundary_is_four_thousand_ninety_six(self):
        maximum_token = "a" * 4096
        self.write_tokens(json.dumps({"test-api-token": maximum_token}))
        with self.env():
            config = compliance_ops.load_config()
        self.assertEqual(config.token, maximum_token)

        oversized_token = "b" * 4097
        self.write_tokens(json.dumps({"test-api-token": oversized_token}))
        with self.env(), self.assertRaises(compliance_ops.ConfigError) as caught:
            compliance_ops.load_config()
        message = str(caught.exception)
        self.assertEqual(message, "token file contains invalid entries")
        self.assertNotIn(oversized_token, message)
        self.assertNotIn("test-api-token", message)
        self.assertNotIn(str(self.token_file), message)

    def test_rejects_oversized_sparse_token_file_before_reading(self):
        self.write_tokens()
        with self.token_file.open("r+b") as file:
            file.truncate(1024 * 1024 + 1)

        with self.env(), self.assertRaises(compliance_ops.ConfigError) as caught:
            compliance_ops.load_config()

        message = str(caught.exception)
        self.assertEqual(message, "token file exceeds the configured byte limit")
        self.assertNotIn(str(self.token_file), message)
        self.assertNotIn("test-api-token", message)

    def test_rejects_insecure_mode_nonregular_token_and_invalid_base_urls(self):
        self.write_tokens()
        self.token_file.chmod(0o644)
        with self.env(), self.assertRaisesRegex(compliance_ops.ConfigError, "permissions"):
            compliance_ops.load_config()
        self.token_file.unlink()
        self.token_file.mkdir()
        with self.env(), self.assertRaisesRegex(compliance_ops.ConfigError, "regular file"):
            compliance_ops.load_config()
        self.token_file.rmdir()
        self.write_tokens()
        invalid = [
            "ftp://localhost/x",
            "http://user:pass@localhost/x",
            "http://localhost/x?q=1",
            "http://localhost/x#frag",
            "http://example.com",
        ]
        for value in invalid:
            with self.subTest(value=value), self.env(COMPLIANCE_OPS_BASE_URL=value):
                with self.assertRaises(compliance_ops.ConfigError):
                    compliance_ops.load_config()

    def test_requires_resolved_evidence_directory_and_bounded_positive_max(self):
        self.write_tokens()
        for value in ("0", "-1", "no", str(1024 * 1024 * 1024 + 1)):
            with self.subTest(value=value), self.env(COMPLIANCE_OPS_MAX_EVIDENCE_BYTES=value):
                with self.assertRaises(compliance_ops.ConfigError):
                    compliance_ops.load_config()
        missing = self.root / "missing"
        with self.env(COMPLIANCE_OPS_EVIDENCE_DIR=str(missing)):
            with self.assertRaisesRegex(compliance_ops.ConfigError, "evidence directory"):
                compliance_ops.load_config()


class RecordingHandler(BaseHTTPRequestHandler):
    records = []
    responses = []

    def _handle(self):
        length = int(self.headers.get("Content-Length", "0"))
        body = self.rfile.read(length)
        type(self).records.append(
            {
                "method": self.command,
                "path": self.path,
                "authorization": self.headers.get("Authorization"),
                "content_type": self.headers.get("Content-Type"),
                "body": body,
            }
        )
        status, content_type, response = type(self).responses.pop(0)
        self.send_response(status)
        self.send_header("Content-Type", content_type)
        self.send_header("Content-Length", str(len(response)))
        self.end_headers()
        self.wfile.write(response)

    do_GET = _handle
    do_POST = _handle
    do_PATCH = _handle
    do_DELETE = _handle

    def log_message(self, _format, *_args):
        pass


class RestToolTests(unittest.IsolatedAsyncioTestCase):
    def setUp(self):
        RecordingHandler.records = []
        RecordingHandler.responses = []
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), RecordingHandler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.temp = tempfile.TemporaryDirectory()
        evidence = Path(self.temp.name).resolve()
        self.config = compliance_ops.Config(
            f"http://127.0.0.1:{self.server.server_port}",
            evidence / "tokens.json",
            "test-api-token",
            evidence,
            100,
            "super-secret-token",
        )
        self.client = compliance_ops.ComplianceOpsClient(self.config)

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join()
        self.temp.cleanup()

    def respond(self, data, status=200, content_type="application/json"):
        body = data if isinstance(data, bytes) else json.dumps(data).encode()
        RecordingHandler.responses.append((status, content_type, body))

    def last(self):
        return RecordingHandler.records[-1]

    async def test_read_tools_send_exact_bounded_queries_and_bearer_auth(self):
        cases = [
            (self.client.get_dashboard(), "/v1/dashboard"),
            (self.client.list_frameworks(), "/v1/frameworks"),
            (
                self.client.list_requirements(
                    framework_id="fw 1",
                    status="partial",
                    query="access",
                    scope_category_ids=["scope-1", "scope-2"],
                    limit=25,
                    offset=5,
                ),
                "/v1/requirements?frameworkId=fw+1&status=partial&q=access&scopeCategoryId=scope-1&scopeCategoryId=scope-2&limit=25&offset=5",
            ),
            (self.client.get_requirement("req/1"), "/v1/requirements/req%2F1"),
            (
                self.client.list_scope_categories(query="prod", limit=20),
                "/v1/scope-categories?q=prod&limit=20",
            ),
            (
                self.client.list_evidence(
                    requirement_id="req-1", kind="link", query="policy", limit=10, offset=2
                ),
                "/v1/evidence?requirementId=req-1&kind=link&q=policy&limit=10&offset=2",
            ),
        ]
        for call, path in cases:
            with self.subTest(path=path):
                self.respond({"ok": True})
                self.assertEqual(json.loads(await call), {"ok": True})
                self.assertEqual(self.last()["method"], "GET")
                self.assertEqual(self.last()["path"], path)
                self.assertEqual(self.last()["authorization"], "Bearer super-secret-token")

    async def test_write_tools_send_exact_paths_payloads_and_preconditions(self):
        timestamp = "2026-09-16T01:02:03Z"
        cases = [
            (
                self.client.update_part(
                    "req", "part", None, status="implemented", owner="A", due_date=None,
                    description="done", scope_category_ids=["11111111-1111-4111-8111-111111111111"]
                ),
                "PATCH", "/v1/requirements/req/parts/part",
                {"status": "implemented", "owner": "A", "description": "done", "scopeCategoryIds": ["11111111-1111-4111-8111-111111111111"], "expectedUpdatedAt": None},
            ),
            (
                self.client.update_requirement_notes("req", "notes", timestamp),
                "PATCH", "/v1/requirements/req",
                {"notes": "notes", "expectedUpdatedAt": timestamp},
            ),
            (
                self.client.set_requirement_status_override("req", None, timestamp),
                "PATCH", "/v1/requirements/req",
                {"statusOverride": None, "expectedUpdatedAt": timestamp},
            ),
            (
                self.client.create_scope_category("Production"),
                "POST", "/v1/scope-categories", {"name": "Production"},
            ),
            (
                self.client.update_scope_category("scope", "Renamed", timestamp),
                "PATCH", "/v1/scope-categories/scope",
                {"name": "Renamed", "expectedUpdatedAt": timestamp},
            ),
            (
                self.client.delete_scope_category("scope", timestamp),
                "DELETE", "/v1/scope-categories/scope?expectedUpdatedAt=2026-09-16T01%3A02%3A03Z", None,
            ),
            (
                self.client.create_link_evidence(
                    "https://example.com/policy", "Policy", ["req-1"],
                    description="desc", valid_from="2026-01-01", valid_until="2026-12-31"
                ),
                "POST", "/v1/evidence",
                {"kind": "link", "url": "https://example.com/policy", "title": "Policy", "requirementIds": ["req-1"], "description": "desc", "validFrom": "2026-01-01", "validUntil": "2026-12-31"},
            ),
        ]
        for call, method, path, payload in cases:
            with self.subTest(path=path, payload=payload):
                self.respond({"ok": True})
                await call
                record = self.last()
                self.assertEqual((record["method"], record["path"]), (method, path))
                if payload is not None:
                    self.assertEqual(json.loads(record["body"]), payload)
                else:
                    self.assertEqual(record["body"], b"")

    async def test_update_part_clear_due_date_sends_exact_json_null(self):
        timestamp = "2026-09-16T01:02:03Z"
        self.respond({"ok": True})

        await self.client.update_part(
            "req", "part", timestamp, clear_due_date=True
        )

        record = self.last()
        self.assertEqual(record["method"], "PATCH")
        self.assertEqual(record["path"], "/v1/requirements/req/parts/part")
        self.assertEqual(
            json.loads(record["body"]),
            {"dueDate": None, "expectedUpdatedAt": timestamp},
        )

    async def test_update_part_empty_scope_ids_sends_exact_empty_array(self):
        timestamp = "2026-09-16T01:02:03Z"
        self.respond({"ok": True})

        await self.client.update_part(
            "req", "part", timestamp, scope_category_ids=[]
        )

        record = self.last()
        self.assertEqual(record["method"], "PATCH")
        self.assertEqual(record["path"], "/v1/requirements/req/parts/part")
        self.assertEqual(
            json.loads(record["body"]),
            {"scopeCategoryIds": [], "expectedUpdatedAt": timestamp},
        )

    async def test_update_part_rejects_due_date_with_clear_due_date(self):
        with self.assertRaisesRegex(
            ValueError, "due_date and clear_due_date are mutually exclusive"
        ):
            await self.client.update_part(
                "req",
                "part",
                "2026-09-16T01:02:03Z",
                due_date="2026-12-31",
                clear_due_date=True,
            )

        self.assertEqual(RecordingHandler.records, [])

    async def test_update_part_rejects_non_boolean_clear_due_date(self):
        values: tuple[Any, ...] = (1, "true", None)
        for value in values:
            with self.subTest(value=value), self.assertRaisesRegex(
                ValueError, "clear_due_date must be a boolean"
            ):
                await self.client.update_part(
                    "req",
                    "part",
                    "2026-09-16T01:02:03Z",
                    clear_due_date=value,
                )

        self.assertEqual(RecordingHandler.records, [])

    async def test_update_part_nonempty_scope_ids_remain_uuid_validated_and_bounded(self):
        invalid_scope_ids = [
            ["not-a-uuid"],
            ["11111111-1111-4111-8111-111111111111"] * 101,
        ]
        for scope_ids in invalid_scope_ids:
            with self.subTest(count=len(scope_ids)), self.assertRaises(ValueError):
                await self.client.update_part(
                    "req",
                    "part",
                    "2026-09-16T01:02:03Z",
                    scope_category_ids=scope_ids,
                )

        self.assertEqual(RecordingHandler.records, [])

    async def test_evidence_requirement_ids_still_reject_empty_lists(self):
        calls = [
            self.client.create_link_evidence("https://example.com", "Title", []),
            self.client.create_file_evidence("report.txt", "Title", []),
        ]
        for call in calls:
            with self.subTest(call=call), self.assertRaisesRegex(
                ValueError, "ids must contain between 1 and 100 entries"
            ):
                await call

        self.assertEqual(RecordingHandler.records, [])

    async def test_rejects_successful_nested_object_token_reflection(self):
        reflected = {
            "safe": {
                "super-secret-token": "prefix-super-secret-token-suffix",
            }
        }
        self.respond(reflected, 201)

        with self.assertRaises(compliance_ops.APIError) as caught:
            await self.client.get_dashboard()

        error = caught.exception
        self.assertEqual(error.status, 201)
        self.assertEqual(error.code, "TOKEN_REFLECTION")
        self.assertEqual(error.message, "API response contained protected credential material")
        rendered = str(error)
        self.assertNotIn("super-secret-token", rendered)
        self.assertNotIn("prefix-", rendered)
        self.assertNotIn("suffix", rendered)
        self.assertNotIn("safe", rendered)

    async def test_rejects_successful_nested_array_token_reflection(self):
        reflected = [
            "super-secret-token",
            {"prefix-super-secret-token-suffix": ["super-secret-token"]},
        ]
        self.respond(reflected)

        with self.assertRaises(compliance_ops.APIError) as caught:
            await self.client.get_dashboard()

        error = caught.exception
        self.assertEqual(error.status, 200)
        self.assertEqual(error.code, "TOKEN_REFLECTION")
        self.assertEqual(error.message, "API response contained protected credential material")
        rendered = str(error)
        self.assertNotIn("super-secret-token", rendered)
        self.assertNotIn("prefix-", rendered)
        self.assertNotIn("suffix", rendered)
        self.assertNotIn("dashboard", rendered)

    async def test_errors_preserve_only_status_code_and_message_including_stale_conflict(self):
        self.respond({"code": "CONFLICT", "message": "resource was modified"}, 409)
        with self.assertRaises(compliance_ops.APIError) as caught:
            await self.client.update_requirement_notes(
                "req", "notes", "2026-09-16T01:02:03Z"
            )
        self.assertEqual(caught.exception.status, 409)
        self.assertEqual(caught.exception.code, "CONFLICT")
        self.assertEqual(caught.exception.message, "resource was modified")
        self.assertNotIn("super-secret-token", str(caught.exception))

        self.respond(b"<html>secret proxy page</html>", 401, "text/html")
        with self.assertRaises(compliance_ops.APIError) as caught:
            await self.client.get_dashboard()
        self.assertEqual(caught.exception.status, 401)
        self.assertNotIn("secret proxy page", str(caught.exception))
        self.assertNotIn("super-secret-token", str(caught.exception))

        self.respond(
            {"code": "UNAUTHORIZED", "message": "bad super-secret-token"}, 401
        )
        with self.assertRaises(compliance_ops.APIError) as caught:
            await self.client.get_dashboard()
        self.assertEqual(caught.exception.status, 401)
        self.assertNotIn("super-secret-token", str(caught.exception))

    async def test_rejects_invalid_expected_timestamp_link_and_bounded_inputs(self):
        invalid_calls = [
            self.client.update_requirement_notes("req", "n", ""),
            self.client.update_scope_category("scope", "name", "not-a-date"),
            self.client.delete_scope_category("scope", "2026-01-01"),
            self.client.create_link_evidence("file:///etc/passwd", "x", ["req"]),
            self.client.create_link_evidence("https://u:p@example.com", "x", ["req"]),
            self.client.list_requirements(query="x" * 501),
            self.client.list_requirements(scope_category_ids=[str(i) for i in range(101)]),
            self.client.list_evidence(limit=201),
        ]
        for call in invalid_calls:
            with self.subTest(call=call), self.assertRaises(ValueError):
                await call
        self.assertEqual(RecordingHandler.records, [])

    async def test_response_body_is_bounded_while_streaming(self):
        self.respond(b"{\"data\":\"" + b"x" * 300 + b"\"}")
        with patch.object(compliance_ops, "MAX_RESPONSE_BYTES", 128):
            with self.assertRaisesRegex(compliance_ops.APIError, "response exceeds"):
                await self.client.get_dashboard()


class FileEvidenceTests(RestToolTests):
    async def test_uploads_multipart_without_exposing_file_contents_in_result(self):
        path = Path(self.temp.name) / "report.txt"
        path.write_bytes(b"evidence-body")
        self.respond({"id": "ev-1", "kind": "file"}, 201)
        result = json.loads(
            await self.client.create_file_evidence(
                "report.txt", "Report", ["req-1", "req-2"], description="desc"
            )
        )
        self.assertEqual(result, {"id": "ev-1", "kind": "file"})
        record = self.last()
        self.assertTrue(record["content_type"].startswith("multipart/form-data; boundary="))
        self.assertIn(b'evidence-body', record["body"])
        self.assertIn(b'name="requirementIds"', record["body"])
        self.assertNotIn("evidence-body", json.dumps(result))

    async def test_rejects_traversal_absolute_symlinks_and_oversize_files(self):
        root = Path(self.temp.name)
        outside_dir = tempfile.TemporaryDirectory()
        self.addCleanup(outside_dir.cleanup)
        outside = Path(outside_dir.name) / "outside.txt"
        outside.write_text("outside")
        (root / "small.txt").write_text("ok")
        (root / "large.txt").write_bytes(b"x" * 101)
        (root / "inside-link").symlink_to(root / "small.txt")
        (root / "outside-link").symlink_to(outside)
        (root / "dir-link").symlink_to(Path(outside_dir.name), target_is_directory=True)
        invalid = ["../outside.txt", str(outside), "inside-link", "outside-link", "dir-link/outside.txt", "large.txt"]
        for relative in invalid:
            with self.subTest(relative=relative):
                with self.assertRaises(ValueError):
                    await self.client.create_file_evidence(relative, "Title", ["req"])
        self.assertEqual(RecordingHandler.records, [])


class ProtocolIntegrationTests(unittest.IsolatedAsyncioTestCase):
    async def test_initialize_tools_list_and_tools_call_over_stdio(self):
        RecordingHandler.records = []
        RecordingHandler.responses = []
        server = ThreadingHTTPServer(("127.0.0.1", 0), RecordingHandler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        with tempfile.TemporaryDirectory() as temp:
            root = Path(temp)
            token_file = root / "tokens.json"
            token_file.write_text('{"test-api-token":"protocol-secret-token"}')
            token_file.chmod(0o600)
            env = {
                **os.environ,
                "COMPLIANCE_OPS_BASE_URL": f"http://127.0.0.1:{server.server_port}",
                "COMPLIANCE_OPS_TOKEN_FILE": str(token_file),
                "COMPLIANCE_OPS_EVIDENCE_DIR": str(root),
            }
            RecordingHandler.responses.append(
                (200, "application/json", b'{"generatedAt":"2026-09-16T00:00:00Z"}')
            )
            parameters = StdioServerParameters(
                command=sys.executable,
                args=[str(Path(compliance_ops.__file__).resolve())],
                env=env,
            )
            async with stdio_client(parameters) as (read, write):
                async with ClientSession(read, write) as session:
                    initialized = await session.initialize()
                    self.assertEqual(initialized.serverInfo.name, "compliance-ops")
                    listed = await session.list_tools()
                    self.assertIn("get_dashboard", {tool.name for tool in listed.tools})
                    called = await session.call_tool("get_dashboard", {})
                    self.assertFalse(called.isError)
                    self.assertIn("generatedAt", called.content[0].text)
                    missing_precondition = await session.call_tool(
                        "update_requirement_notes",
                        {"requirement_id": "req", "notes": "notes"},
                    )
                    self.assertTrue(missing_precondition.isError)
            self.assertEqual(len(RecordingHandler.records), 1)
            self.assertEqual(RecordingHandler.records[0]["path"], "/v1/dashboard")
            self.assertEqual(
                RecordingHandler.records[0]["authorization"],
                "Bearer protocol-secret-token",
            )
        server.shutdown()
        server.server_close()
        thread.join()

    async def test_cli_rejects_non_loopback_streamable_http_bind(self):
        result = await asyncio.to_thread(
            subprocess.run,
            [
                sys.executable,
                str(Path(compliance_ops.__file__).resolve()),
                "--transport",
                "streamable-http",
                "--host",
                "0.0.0.0",
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )
        self.assertEqual(result.returncode, 2)
        self.assertIn("loopback", result.stderr)


class ToolSchemaTests(unittest.IsolatedAsyncioTestCase):
    async def test_exact_tool_inventory_and_required_expected_updated_at_schema(self):
        tools = await compliance_ops.mcp.list_tools()
        by_name = {tool.name: tool for tool in tools}
        self.assertEqual(
            set(by_name),
            {
                "get_dashboard", "list_frameworks", "list_requirements", "get_requirement",
                "list_scope_categories", "list_evidence", "update_part",
                "update_requirement_notes", "set_requirement_status_override",
                "create_scope_category", "update_scope_category", "delete_scope_category",
                "create_link_evidence", "create_file_evidence",
            },
        )
        for name in ("update_part", "update_requirement_notes", "set_requirement_status_override", "update_scope_category", "delete_scope_category"):
            self.assertIn("expected_updated_at", by_name[name].inputSchema["required"])
        expected = by_name["update_part"].inputSchema["properties"]["expected_updated_at"]
        self.assertIn("null", json.dumps(expected))

    async def test_update_part_schema_exposes_optional_boolean_clear_due_date(self):
        tools = await compliance_ops.mcp.list_tools()
        update_part = next(tool for tool in tools if tool.name == "update_part")

        schema = update_part.inputSchema
        self.assertEqual(
            schema["properties"]["clear_due_date"],
            {"default": False, "title": "Clear Due Date", "type": "boolean"},
        )
        self.assertNotIn("clear_due_date", schema["required"])

    async def test_update_part_tool_rejects_coercible_non_boolean_clear_due_date(self):
        arguments = {
            "requirement_id": "req",
            "part_id": "part",
            "expected_updated_at": "2026-09-16T01:02:03Z",
            "clear_due_date": "true",
        }
        with patch.object(
            compliance_ops, "_call", new_callable=AsyncMock
        ) as mocked_call:
            with self.assertRaises(ToolError):
                await compliance_ops.mcp.call_tool("update_part", arguments)

        mocked_call.assert_not_awaited()


if __name__ == "__main__":
    unittest.main()
