"""Resource replacement reads back representations after OMLOX 204 responses."""
import unittest
from unittest.mock import Mock
from hub_client import HubRESTClient

class HubContractTests(unittest.TestCase):
    def test_204_update_reads_resource(self):
        client = object.__new__(HubRESTClient)
        existing = Mock(status_code=200)
        updated = Mock(status_code=204)
        readback = Mock(status_code=200)
        readback.json.return_value = {"id": "p", "type": "gps"}
        client._request = Mock(side_effect=[existing, updated, readback])
        result = client._ensure_resource("/v2/providers", "/v2/providers/p", {"type": "gps"})
        self.assertEqual(result["id"], "p")
        updated.json.assert_not_called()
        self.assertEqual([call.args[0] for call in client._request.call_args_list], ["GET", "PUT", "GET"])
