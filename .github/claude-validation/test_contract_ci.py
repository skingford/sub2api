import hashlib
import json
from pathlib import Path
import tempfile
import unittest

from run_contract_ci import verify_evidence, verify_test_events, PREFIX


class ContractGateTest(unittest.TestCase):
    def test_evidence_rejects_mutation_missing_and_unlisted_fixture(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fixture = root / 'sample.json'
            fixture.write_text('{}')
            manifest = {'fixture_globs': ['*.json'], 'files': {'sample.json': hashlib.sha256(b'{}').hexdigest()}, 'versions': {}}
            self.assertEqual(1, verify_evidence(root, manifest))
            fixture.write_text('{"changed":true}')
            with self.assertRaisesRegex(ValueError, 'digest mismatch'):
                verify_evidence(root, manifest)
            fixture.unlink()
            with self.assertRaisesRegex(ValueError, 'inventory changed'):
                verify_evidence(root, manifest)
            fixture.write_text('{}')
            (root / 'unlisted.json').write_text('{}')
            with self.assertRaisesRegex(ValueError, 'inventory changed'):
                verify_evidence(root, manifest)

    def test_provenance_binary_must_match_version_pin(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            path = root / 'provenance.json'
            path.write_text(json.dumps({'version': '2.1.295', 'binary_sha256': 'actual'}))
            manifest = {'fixture_globs': ['*.json'], 'files': {'provenance.json': hashlib.sha256(path.read_bytes()).hexdigest()},
                        'versions': {'2.1.295': {'provenance': 'provenance.json', 'binary_sha256': 'wrong'}}}
            with self.assertRaisesRegex(ValueError, 'binary pin mismatch'):
                verify_evidence(root, manifest)

    def test_missing_skipped_failed_or_incomplete_test_run_is_rejected(self):
        package = PREFIX + 'internal/service'
        required = {'./internal/service': ['TestRequired']}
        test = {'Package': package, 'Test': 'TestRequired', 'Action': 'pass'}
        completion = {'Package': package, 'Action': 'pass'}
        self.assertEqual(1, verify_test_events([test, completion], required))
        cases = [[], [completion], [test], [{**test, 'Action': 'skip'}, completion],
                 [{**test, 'Action': 'fail'}, completion],
                 [test, {**test, 'Test': 'TestRequired/child', 'Action': 'skip'}, completion],
                 [test, {**completion, 'Action': 'fail'}]]
        for events in cases:
            with self.subTest(events=events), self.assertRaises(ValueError):
                verify_test_events(events, required)


if __name__ == '__main__':
    unittest.main()
