# Compliance Ops MCP servers

This Python package contains two separate FastMCP servers:

- `compliance-ops-mcp`: authenticated, bounded access to the Compliance Ops application.
- `nist-oscal-catalogs`: the existing read-only server for official NIST OSCAL catalogs.

Python 3.11–3.13 is supported. Install the locked development environment from the repository root:

```sh
uv sync --project mcp --extra dev --locked
```

## Authenticated application server

The server uses stdio by default:

```sh
COMPLIANCE_OPS_EVIDENCE_DIR=/absolute/allowlisted/directory \
  uv run --project mcp compliance-ops-mcp
```

Optional Streamable HTTP is restricted to loopback:

```sh
COMPLIANCE_OPS_EVIDENCE_DIR=/absolute/allowlisted/directory \
  uv run --project mcp compliance-ops-mcp \
  --transport streamable-http --host 127.0.0.1 --port 8000
```

### Configuration

| Variable | Behavior |
| --- | --- |
| `COMPLIANCE_OPS_BASE_URL` | Defaults to `http://127.0.0.1:3000`. Only HTTP(S) URLs without credentials, query, or fragment are accepted. Plain HTTP is restricted to loopback. |
| `COMPLIANCE_OPS_TOKEN_FILE` | Protected JSON label-to-token map. Defaults to repository-root `.env.tokens` only when that file exists; otherwise required. The path must be a regular file with mode `0400`, `0600`, `0440`, or `0640`. |
| `COMPLIANCE_OPS_TOKEN_LABEL` | Exact label to select; defaults to `test-api-token`. Tokens and labels are never included in tool results or safe errors. |
| `COMPLIANCE_OPS_EVIDENCE_DIR` | Required resolved directory. `create_file_evidence` may read only relative, non-symlink regular files beneath it. |
| `COMPLIANCE_OPS_MAX_EVIDENCE_BYTES` | Positive upload limit; defaults to 50 MiB and is capped at 1 GiB. |

The token JSON is limited to 1 MiB and must contain 1–100 entries with unique, nonblank labels of at most 100 characters. Values must be unique token68-safe strings of 12–4096 characters. Empty maps, duplicate labels or values, malformed JSON, trailing data, insecure permissions, and invalid entries fail closed without echoing paths, labels, or values.

Before any successful decoded API object or array is serialized, the client recursively checks every nested string key and value. If the configured bearer token occurs exactly or within a larger string, the entire response is rejected with its HTTP status preserved, code `TOKEN_REFLECTION`, and the fixed message `API response contained protected credential material`; no response fragment or request path is returned. Existing non-2xx status/code/message sanitization remains in place.

### Tools

Read tools:

- `get_dashboard`
- `list_frameworks`
- `list_requirements`
- `get_requirement`
- `list_scope_categories`
- `list_evidence`

Write tools:

- `update_part`
- `update_requirement_notes`
- `set_requirement_status_override`
- `create_scope_category`
- `update_scope_category`
- `delete_scope_category`
- `create_link_evidence`
- `create_file_evidence`

`update_part` accepts `requirement_id`, `part_id`, `expected_updated_at`, and any changed tracking fields: `status`, `owner`, `due_date`, `clear_due_date`, `description`, or `scope_category_ids`. Set `clear_due_date` to `true` to send `"dueDate": null`; it is optional and defaults to `false`. Do not supply `due_date` and `clear_due_date: true` together.

For example, these MCP arguments clear only a persisted due date while preserving the other tracking fields:

```json
{
  "requirement_id": "requirement-id",
  "part_id": "statement-part-id",
  "expected_updated_at": "2026-09-16T01:02:03Z",
  "clear_due_date": true
}
```

To set or replace the date instead, supply `"due_date": "2026-12-31"` and leave `clear_due_date` false or omit it. To clear all scopes while preserving the other tracking fields, send an explicit empty list:

```json
{
  "requirement_id": "requirement-id",
  "part_id": "statement-part-id",
  "expected_updated_at": "2026-09-16T01:02:03Z",
  "scope_category_ids": []
}
```

Nonempty scope lists accept 1 to 100 UUIDs. Evidence creation still requires 1 to 100 requirement IDs; an empty evidence requirement list is invalid.

Every update requires caller-supplied `expected_updated_at`. `update_part` accepts a timestamp or explicit `null`; `null` is sent as JSON null and safely requires that no persisted part row exists. Other update preconditions must be nonblank RFC3339 timestamps. Scope deletion sends `expectedUpdatedAt` as a query parameter. API failures expose only status, application code, and application message.

Inputs, query lists, pagination, identifier counts, response bodies, and evidence bytes are bounded. Link evidence accepts only HTTP(S) URLs without credentials. File evidence rejects absolute paths, traversal, symlink components and files, nonregular files, and oversized files; it opens with no-follow semantics and streams multipart data. No tool downloads or returns evidence file contents.

## NIST OSCAL catalog server

The existing read-only server remains available:

```sh
uv run --project mcp nist-oscal-catalogs
```

Its tools are `list_catalogs`, `get_catalog_metadata`, `search_controls`, and `get_control`. It fetches only configured official `raw.githubusercontent.com/usnistgov/oscal-content/main` URLs, bounds downloads to 25 MiB, validates OSCAL catalog shape, and never reads credentials.

## Test and lint

```sh
uv run --project mcp python -m unittest discover -s mcp/tests -v
uv run --project mcp ruff check mcp
uv run --project mcp python -m py_compile mcp/compliance_ops.py mcp/oscal_catalogs.py
```

The application REST tests use a real local HTTP server. The suite also exercises MCP initialize, tools/list, and tools/call over stdio. The NIST live test remains opt-in:

```sh
OSCAL_LIVE_TESTS=1 uv run --project mcp python -m unittest discover -s mcp/tests -p 'test_live.py' -v
```
