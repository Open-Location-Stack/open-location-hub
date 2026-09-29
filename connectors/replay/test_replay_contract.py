"""Compatibility checks for immutable vendor recordings."""
import unittest
from datetime import UTC, datetime
from replay_support import normalize_technology, prepare_location_for_replay

class ReplayContractTests(unittest.TestCase):
    def test_vendor_technology_preserves_original_and_metadata(self):
        original = {"provider_type": "omlox", "provider_id": "vendor-id", "crs": "local", "source": "foreign-zone", "position": {"type": "Point", "coordinates": [1, 2, 3]}, "properties": {"custom": 42}}
        normalized = normalize_technology(original)
        self.assertEqual(normalized["provider_type"], "unknown")
        self.assertEqual(normalized["properties"], {"custom": 42, "upstream_provider_type": "omlox"})
        self.assertEqual(original["properties"], {"custom": 42})
        for key in ("provider_id", "source", "crs", "position"):
            self.assertEqual(original[key], normalized[key])
        now = datetime.now(UTC)
        replay = prepare_location_for_replay(original, now, now, False)
        self.assertEqual(replay["provider_type"], "unknown")
        self.assertIn("replay_original_timestamp_generated", replay["properties"])

    def test_standard_technology_is_preserved(self):
        for value in ("uwb", "gps", "wifi", "rfid", "ibeacon", "virtual", "unknown"):
            self.assertEqual(normalize_technology({"provider_type": value})["provider_type"], value)
