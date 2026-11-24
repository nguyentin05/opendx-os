# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors

"""Static checks for the gateway limits on the GraphQL route."""
import unittest
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[1]
REQUIRED_PLUGINS = {'client-control', 'limit-count', 'serverless-pre-function'}


class GatewayLimits(unittest.TestCase):
    def setUp(self):
        routes = yaml.safe_load((ROOT / 'platform/apisix/apisix.yaml').read_text())['routes']
        self.route = next(route for route in routes if route['id'] == 'hasura-graphql')
        self.config = yaml.safe_load((ROOT / 'platform/apisix/config.yaml').read_text())

    def test_graphql_route_bounds_request_size_and_rate(self):
        plugins = self.route['plugins']
        self.assertEqual(plugins['client-control']['max_body_size'], 262144)
        limit = plugins['limit-count']
        self.assertEqual(limit['count'], 600)
        self.assertEqual(limit['time_window'], 60)
        self.assertEqual(limit['key'], 'remote_addr')
        self.assertEqual(limit['policy'], 'local')
        self.assertEqual(limit['rejected_code'], 429)

    def test_json_array_bodies_are_rejected(self):
        functions = self.route['plugins']['serverless-pre-function']['functions']
        self.assertTrue(any('^%s*%[' in function for function in functions))

    def test_plugin_defaults_are_not_restricted(self):
        enabled = self.config.get('plugins')
        if enabled is not None:
            self.assertLessEqual(REQUIRED_PLUGINS, set(enabled))


if __name__ == '__main__':
    unittest.main()
