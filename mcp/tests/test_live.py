import asyncio
import os
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

import oscal_catalogs


@unittest.skipUnless(
    os.environ.get("OSCAL_LIVE_TESTS") == "1",
    "set OSCAL_LIVE_TESTS=1 to call official NIST catalog URLs",
)
class OfficialCatalogLiveTests(unittest.TestCase):
    def test_all_advertised_urls_are_oscal_catalogs_with_expected_key_counts(self):
        async def check() -> None:
            expected_classes = {
                "ssdf-1.1": {"practice": 19, "task": 42},
                "sp800-53-r5": {"SP800-53": 324, "SP800-53-enhancement": 872},
            }
            for catalog_id in oscal_catalogs.CATALOGS:
                with self.subTest(catalog_id=catalog_id):
                    data = await oscal_catalogs.fetch_catalog(catalog_id)
                    controls = oscal_catalogs.extract_controls_from_catalog(data)
                    self.assertGreater(len(controls), 0)
                    if catalog_id in expected_classes:
                        actual_classes = {
                            class_name: sum(
                                control["class"] == class_name for control in controls
                            )
                            for class_name in expected_classes[catalog_id]
                        }
                        self.assertEqual(
                            actual_classes, expected_classes[catalog_id]
                        )

        asyncio.run(check())


if __name__ == "__main__":
    unittest.main()
