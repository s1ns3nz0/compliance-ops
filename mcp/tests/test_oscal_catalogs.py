import json
import sys
import unittest
from pathlib import Path
from unittest.mock import AsyncMock, patch

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import oscal_catalogs


SAMPLE_CATALOG = {
    "catalog": {
        "uuid": "catalog-uuid",
        "metadata": {
            "title": "Example Catalog",
            "version": "1.2.3",
            "oscal-version": "1.1.2",
            "last-modified": "2025-01-01T00:00:00Z",
        },
        "groups": [
            {
                "id": "group-1",
                "controls": [
                    {
                        "id": "control-1",
                        "class": "unexpected-control-class",
                        "title": "First control",
                        "parts": [
                            {
                                "name": "statement",
                                "prose": "Top-level statement.",
                                "parts": [
                                    {"name": "item", "prose": "Nested statement prose."}
                                ],
                            }
                        ],
                        "controls": [
                            {
                                "id": "control-1.1",
                                "class": "another-class",
                                "title": "Nested control",
                                "parts": [
                                    {"name": "statement", "prose": "Nested control text."}
                                ],
                            }
                        ],
                    }
                ],
                "metadata-like-object": {
                    "id": "not-a-control",
                    "class": "control",
                    "title": "Must not be extracted",
                },
            }
        ],
    }
}


class FakeStreamingResponse:
    def __init__(self, chunks):
        self.chunks = chunks
        self.chunks_read = 0
        self.status_checked = False

    @property
    def content(self):
        raise AssertionError("streaming fetch must not access buffered response.content")

    def raise_for_status(self):
        self.status_checked = True

    async def aiter_bytes(self):
        for chunk in self.chunks:
            self.chunks_read += 1
            yield chunk


class FakeStreamContext:
    def __init__(self, response):
        self.response = response

    async def __aenter__(self):
        return self.response

    async def __aexit__(self, *_args):
        return False


class FakeAsyncClient:
    response = None
    timeout_seen = None

    def __init__(self, *, timeout):
        type(self).timeout_seen = timeout

    async def __aenter__(self):
        return self

    async def __aexit__(self, *_args):
        return False

    def stream(self, method, url, *, headers):
        assert method == "GET"
        assert url.startswith("https://")
        assert headers == {"Accept": "application/json"}
        return FakeStreamContext(type(self).response)


class ExtractControlsTests(unittest.TestCase):
    def test_extracts_only_objects_in_controls_arrays_including_nested_controls(self):
        controls = oscal_catalogs.extract_controls_from_catalog(SAMPLE_CATALOG)

        self.assertEqual([item["id"] for item in controls], ["control-1", "control-1.1"])
        self.assertEqual(controls[0]["class"], "unexpected-control-class")
        self.assertEqual(
            controls[0]["statement"],
            "Top-level statement.\nNested statement prose.",
        )

    def test_rejects_non_catalog_documents(self):
        with self.assertRaisesRegex(ValueError, "OSCAL catalog"):
            oscal_catalogs.extract_controls_from_catalog({"profile": {}})


class ToolTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        oscal_catalogs.catalog_cache.clear()

    async def test_fetch_rejects_oversize_stream_before_reading_later_chunks(self):
        response = FakeStreamingResponse([b"123456", b"78901", b"must-not-be-read"])
        FakeAsyncClient.response = response

        with (
            patch.object(oscal_catalogs, "MAX_CATALOG_BYTES", 10),
            patch.object(oscal_catalogs.httpx, "AsyncClient", FakeAsyncClient),
        ):
            with self.assertRaisesRegex(ValueError, "10-byte download limit"):
                await oscal_catalogs.fetch_catalog("ssdf-1.1")

        self.assertTrue(response.status_checked)
        self.assertEqual(response.chunks_read, 2)
        self.assertEqual(FakeAsyncClient.timeout_seen, oscal_catalogs.REQUEST_TIMEOUT_SECONDS)
        self.assertNotIn("ssdf-1.1", oscal_catalogs.catalog_cache)

    async def test_list_catalogs_includes_source_url(self):
        listed = json.loads(await oscal_catalogs.list_catalogs())

        self.assertGreaterEqual(len(listed), 2)
        self.assertTrue(all(item["source_url"].startswith("https://") for item in listed))

    async def test_metadata_is_bounded_and_reports_generic_control_counts(self):
        with patch.object(
            oscal_catalogs, "fetch_catalog", AsyncMock(return_value=SAMPLE_CATALOG)
        ):
            result = json.loads(await oscal_catalogs.get_catalog_metadata("ssdf-1.1"))

        self.assertEqual(result["total_controls"], 2)
        self.assertEqual(result["by_class"], {"another-class": 1, "unexpected-control-class": 1})
        self.assertEqual(result["metadata"]["title"], "Example Catalog")
        self.assertNotIn("groups", result)

    async def test_search_validates_query_and_limit(self):
        invalid_cases = [
            ("", 1, "query must not be empty"),
            ("x" * 501, 1, "query must be at most 500"),
            ("control", 0, "limit must be between 1 and 100"),
            ("control", 101, "limit must be between 1 and 100"),
        ]
        for query, limit, message in invalid_cases:
            with self.subTest(query_length=len(query), limit=limit):
                with self.assertRaisesRegex(ValueError, message):
                    await oscal_catalogs.search_controls("ssdf-1.1", query, limit)

    async def test_search_returns_summaries_and_respects_limit(self):
        with patch.object(
            oscal_catalogs, "fetch_catalog", AsyncMock(return_value=SAMPLE_CATALOG)
        ):
            result = json.loads(
                await oscal_catalogs.search_controls("ssdf-1.1", "statement", 1)
            )

        self.assertEqual(len(result), 1)
        self.assertEqual(
            set(result[0]), {"id", "class", "title", "statement"}
        )

    async def test_get_control_only_finds_actual_controls(self):
        with patch.object(
            oscal_catalogs, "fetch_catalog", AsyncMock(return_value=SAMPLE_CATALOG)
        ):
            missing = json.loads(
                await oscal_catalogs.get_control("ssdf-1.1", "not-a-control")
            )
            found = json.loads(
                await oscal_catalogs.get_control("ssdf-1.1", "control-1.1")
            )

        self.assertIn("error", missing)
        self.assertEqual(found["statement"], "Nested control text.")


if __name__ == "__main__":
    unittest.main()
