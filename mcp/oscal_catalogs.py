#!/usr/bin/env python3
"""Bounded MCP access to official NIST OSCAL catalogs."""

from __future__ import annotations

import argparse
import json
from collections import Counter
from typing import Any

import httpx
from mcp.server.fastmcp import FastMCP

NIST_OSCAL_BASE = "https://raw.githubusercontent.com/usnistgov/oscal-content/main"
MAX_CATALOG_BYTES = 25 * 1024 * 1024
REQUEST_TIMEOUT_SECONDS = 30.0

# Every path below was discovered in the usnistgov/oscal-content main-branch tree.
# fetch_catalog additionally verifies that the response is an OSCAL catalog document.
CATALOGS: dict[str, dict[str, str]] = {
    "ssdf-1.1": {
        "path": "nist.gov/SP800-218/ver1/json/NIST_SP800-218_ver1_catalog.json",
        "title": "NIST SP 800-218 Secure Software Development Framework (SSDF) v1.1",
        "version": "1.1",
    },
    "sp800-53-r5": {
        "path": "nist.gov/SP800-53/rev5/json/NIST_SP-800-53_rev5_catalog.json",
        "title": "NIST SP 800-53 Rev. 5 Security and Privacy Controls",
        "version": "5.2.0",
    },
    "sp800-53-r4": {
        "path": "nist.gov/SP800-53/rev4/json/NIST_SP-800-53_rev4_catalog.json",
        "title": "NIST SP 800-53 Rev. 4 Security and Privacy Controls",
        "version": "4",
    },
    "sp800-171-r3": {
        "path": "nist.gov/SP800-171/rev3/json/NIST_SP800-171_rev3_catalog.json",
        "title": "NIST SP 800-171 Rev. 3 Protecting Controlled Unclassified Information",
        "version": "3",
    },
    "csf-2.0": {
        "path": "nist.gov/CSF/v2.0/json/NIST_CSF_v2.0_catalog.json",
        "title": "NIST Cybersecurity Framework (CSF) 2.0",
        "version": "2.0",
    },
}

mcp = FastMCP("nist-oscal-catalogs")
catalog_cache: dict[str, dict[str, Any]] = {}


def _source_url(catalog_id: str) -> str:
    config = CATALOGS.get(catalog_id)
    if config is None:
        available = ", ".join(CATALOGS)
        raise ValueError(f"Unknown catalog: {catalog_id}. Available: {available}")
    return f"{NIST_OSCAL_BASE}/{config['path']}"


def _catalog_root(data: dict[str, Any]) -> dict[str, Any]:
    catalog = data.get("catalog")
    if not isinstance(catalog, dict):
        raise ValueError("Response is not an OSCAL catalog JSON document")
    if not isinstance(catalog.get("metadata"), dict):
        raise ValueError("OSCAL catalog is missing metadata")
    return catalog


async def fetch_catalog(catalog_id: str) -> dict[str, Any]:
    """Fetch and validate one catalog from the official NIST GitHub repository."""
    cached = catalog_cache.get(catalog_id)
    if cached is not None:
        return cached

    url = _source_url(catalog_id)
    async with httpx.AsyncClient(timeout=REQUEST_TIMEOUT_SECONDS) as client:
        async with client.stream(
            "GET", url, headers={"Accept": "application/json"}
        ) as response:
            response.raise_for_status()
            body = bytearray()
            async for chunk in response.aiter_bytes():
                if len(body) + len(chunk) > MAX_CATALOG_BYTES:
                    raise ValueError(
                        f"Catalog exceeds the {MAX_CATALOG_BYTES}-byte download limit"
                    )
                body.extend(chunk)
        data = json.loads(body)

    if not isinstance(data, dict):
        raise ValueError("Response is not a JSON object")
    _catalog_root(data)
    catalog_cache[catalog_id] = data
    return data


def _statement_prose(parts: Any) -> str:
    prose: list[str] = []

    def walk(items: Any, in_statement: bool = False) -> None:
        if not isinstance(items, list):
            return
        for part in items:
            if not isinstance(part, dict):
                continue
            is_statement = in_statement or part.get("name") == "statement"
            text = part.get("prose")
            if is_statement and isinstance(text, str) and text.strip():
                prose.append(text.strip())
            walk(part.get("parts"), is_statement)

    walk(parts)
    return "\n".join(prose)


def _control_summary(control: dict[str, Any]) -> dict[str, str]:
    return {
        "id": str(control.get("id", "")),
        "class": str(control.get("class", "")),
        "title": str(control.get("title", "")),
        "statement": _statement_prose(control.get("parts")),
    }


def extract_controls_from_catalog(data: dict[str, Any]) -> list[dict[str, str]]:
    """Extract objects under OSCAL ``controls`` arrays, including nested controls."""
    catalog = _catalog_root(data)
    controls: list[dict[str, str]] = []

    def walk_container(container: Any) -> None:
        if not isinstance(container, dict):
            return
        raw_controls = container.get("controls", [])
        if isinstance(raw_controls, list):
            for control in raw_controls:
                if not isinstance(control, dict):
                    continue
                controls.append(_control_summary(control))
                walk_container(control)
        groups = container.get("groups", [])
        if isinstance(groups, list):
            for group in groups:
                walk_container(group)

    walk_container(catalog)
    return controls


@mcp.tool()
async def list_catalogs() -> str:
    """List available official NIST OSCAL catalogs and their source URLs."""
    result = [
        {
            "id": catalog_id,
            "title": config["title"],
            "version": config["version"],
            "source_url": _source_url(catalog_id),
        }
        for catalog_id, config in CATALOGS.items()
    ]
    return json.dumps(result, indent=2)


@mcp.tool()
async def get_catalog_metadata(catalog_id: str) -> str:
    """Return bounded document metadata and control counts, never the full catalog."""
    data = await fetch_catalog(catalog_id)
    catalog = _catalog_root(data)
    controls = extract_controls_from_catalog(data)
    metadata = catalog["metadata"]
    class_counts = Counter(control["class"] for control in controls)
    result = {
        "catalog_id": catalog_id,
        "source_url": _source_url(catalog_id),
        "catalog_uuid": catalog.get("uuid", ""),
        "metadata": {
            "title": metadata.get("title", ""),
            "version": metadata.get("version", ""),
            "oscal_version": metadata.get("oscal-version", ""),
            "last_modified": metadata.get("last-modified", ""),
        },
        "total_controls": len(controls),
        "by_class": dict(sorted(class_counts.items())),
    }
    return json.dumps(result, indent=2)


@mcp.tool()
async def search_controls(catalog_id: str, query: str, limit: int = 50) -> str:
    """Search IDs, classes, titles, and statements; return at most 100 summaries."""
    if not isinstance(query, str) or not query.strip():
        raise ValueError("query must not be empty")
    if len(query) > 500:
        raise ValueError("query must be at most 500 characters")
    if isinstance(limit, bool) or not isinstance(limit, int) or not 1 <= limit <= 100:
        raise ValueError("limit must be between 1 and 100")

    controls = extract_controls_from_catalog(await fetch_catalog(catalog_id))
    normalized_query = query.casefold()
    results = [
        control
        for control in controls
        if any(normalized_query in value.casefold() for value in control.values())
    ][:limit]
    return json.dumps(results, indent=2)


@mcp.tool()
async def get_control(catalog_id: str, control_id: str) -> str:
    """Get one bounded control summary by its exact OSCAL identifier."""
    if not isinstance(control_id, str) or not control_id.strip():
        raise ValueError("control_id must not be empty")
    if len(control_id) > 200:
        raise ValueError("control_id must be at most 200 characters")

    controls = extract_controls_from_catalog(await fetch_catalog(catalog_id))
    control = next((item for item in controls if item["id"] == control_id), None)
    if control is None:
        return json.dumps(
            {"error": f"Control {control_id} not found in {catalog_id}"}, indent=2
        )
    return json.dumps(control, indent=2)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--transport",
        choices=("stdio", "streamable-http"),
        default="stdio",
    )
    parser.add_argument("--host", default="127.0.0.1")
    parser.add_argument("--port", type=int, default=8000)
    args = parser.parse_args()
    if not 1 <= args.port <= 65535:
        parser.error("--port must be between 1 and 65535")
    mcp.settings.host = args.host
    mcp.settings.port = args.port
    mcp.run(transport=args.transport)


if __name__ == "__main__":
    main()
