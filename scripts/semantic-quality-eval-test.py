import copy
import importlib.util
import json
import stat
import tempfile
import unittest
from pathlib import Path

spec = importlib.util.spec_from_file_location("evaluation", Path(__file__).with_name("semantic-quality-eval.py"))
evaltool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(evaltool)


def manifest():
    return {"queries": [{"id": f"q{i}", "query": f"sample {i}", "group": "original" if i < 20 else "rewrite", "relevant_keys": ["note:1", "note:2"]} for i in range(40)]}


def response():
    return {"identity": "stable", "config": {"id": "cfg"}, "read_only": True,
            "lexical": {"Method": "fts", "Hits": [{"Key": "note:1", "Snippet": "private sentinel"}]},
            "hybrid": {"Method": "rrf", "Hits": [{"Key": "note:1"}, {"Key": "note:2"}]}}


class EvaluationTest(unittest.TestCase):
    def test_complete_recall_without_gate_attestation(self):
        calls = []
        def read(cfg, query=None):
            calls.append((cfg, query))
            return response()
        report = evaltool.evaluate(manifest(), "cfg", read)
        self.assertEqual(report["status"], "measured")
        self.assertEqual(len(calls), 42)
        self.assertEqual(report["groups"]["rewrite"]["lexical_recall_at_10"], .5)
        self.assertEqual(report["groups"]["original"]["hybrid_recall_at_10"], 1)
        self.assertFalse(report["quality_gate_passed"])
        self.assertFalse(report["real_embeddings_verified"])
        self.assertNotIn("private sentinel", json.dumps(report))
        self.assertNotIn("sample 0", json.dumps(report))

    def test_degradation_is_not_semantic_recall(self):
        value = response()
        value["hybrid"]["Method"] = "fts"
        report = evaltool.evaluate(manifest(), "cfg", lambda *args: value)
        self.assertEqual(report["status"], "incomplete")
        self.assertIsNone(report["groups"]["original"]["hybrid_recall_at_10"])
        self.assertIsNone(report["queries"][0]["hybrid_recall_at_10"])

    def test_change_during_final_check_invalidates_aggregate(self):
        count = 0
        def read(*args):
            nonlocal count
            count += 1
            value = response()
            if count == 42:
                value["identity"] = "revoked"
            return value
        report = evaltool.evaluate(manifest(), "cfg", read)
        self.assertEqual(report["status"], "blocked")
        self.assertEqual(report["groups"], {})
        self.assertFalse(report["identity_stable"])

    def test_partial_http_failure_is_retained_without_aggregate(self):
        def read(cfg, query=None):
            if query == "sample 2":
                raise ValueError("evaluation HTTP 409")
            return response()
        report = evaltool.evaluate(manifest(), "cfg", read)
        self.assertEqual(len(report["queries"]), 2)
        self.assertEqual(report["status"], "blocked")
        self.assertEqual(report["groups"], {})

    def test_malformed_hits_are_rejected(self):
        value = response()
        value["hybrid"]["Hits"] *= 6
        report = evaltool.evaluate(manifest(), "cfg", lambda *args: value)
        self.assertEqual(report["status"], "blocked")

    def test_manifest_validation(self):
        for mutate in (lambda m: m["queries"].pop(), lambda m: m["queries"][0].update(relevant_keys=[]),
                       lambda m: m["queries"][1].update(query="sample 0"),
                       lambda m: m["queries"][1].update(id="q0")):
            value = copy.deepcopy(manifest())
            mutate(value)
            with self.assertRaises(ValueError):
                evaltool.manifest_queries(value)

    def test_origin_and_cookie_protection(self):
        for url in ("http://remote.example", "https://user:password@host", "https://host/path", "https://host?q=1"):
            with self.assertRaises(ValueError):
                evaltool.endpoint(url)
        self.assertEqual(evaltool.endpoint("http://127.0.0.1:8080"), "http://127.0.0.1:8080/api/knowledge-search-settings")
        with self.assertRaises(ValueError):
            evaltool.http_reader("http://localhost", "cookie\nInjected: x", 1)

    def test_atomic_private_output(self):
        with tempfile.TemporaryDirectory() as folder:
            path = Path(folder) / "result.json"
            evaltool.save_report(path, {"status": "blocked"})
            self.assertEqual(stat.S_IMODE(path.stat().st_mode), 0o600)
            self.assertEqual(json.loads(path.read_text())["status"], "blocked")


if __name__ == "__main__":
    unittest.main()
