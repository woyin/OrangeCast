#!/usr/bin/env python3
"""Read-only Recall@10 evaluation against Owner-authorized, already-prepared vectors.

This report is evidence, not a quality-gate import or an attestation of real vectors.
Only standard library modules are used. No embedding requests or jobs are created.
"""
import argparse
import hashlib
import json
import os
import tempfile
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def manifest_queries(data):
    queries = data.get("queries") if isinstance(data, dict) else None
    if not isinstance(queries, list) or len(queries) != 40:
        raise ValueError("manifest requires exactly 40 labeled queries")
    ids, texts, groups = set(), set(), set()
    for item in queries:
        if not isinstance(item, dict):
            raise ValueError("invalid query")
        ident, text, group = item.get("id"), item.get("query"), item.get("group")
        relevant = item.get("relevant_keys")
        if not isinstance(ident, str) or not ident.strip() or len(ident) > 100 or ident in ids:
            raise ValueError("query IDs must be distinct nonempty strings")
        if not isinstance(text, str) or not text.strip() or len(text) > 200 or text.strip() in texts:
            raise ValueError("queries must be distinct, nonempty, at most 200 characters")
        if group not in ("original", "rewrite"):
            raise ValueError("group must be original or rewrite")
        if not isinstance(relevant, list) or not 1 <= len(relevant) <= 200:
            raise ValueError("each query needs 1..200 Owner-labeled relevant keys")
        if any(not isinstance(k, str) or not k.strip() or len(k) > 500 for k in relevant):
            raise ValueError("invalid relevant key")
        if len(set(relevant)) != len(relevant):
            raise ValueError("duplicate relevant key")
        ids.add(ident)
        texts.add(text.strip())
        groups.add(group)
    if groups != {"original", "rewrite"}:
        raise ValueError("both original and rewrite groups are required")
    return queries


def endpoint(base):
    url = urllib.parse.urlsplit(base)
    local = url.hostname in ("localhost", "127.0.0.1", "::1")
    if url.scheme not in ("http", "https") or not url.netloc or url.username or url.password:
        raise ValueError("base URL must be HTTP(S), without credentials")
    if url.scheme == "http" and not local:
        raise ValueError("remote connections require HTTPS")
    if url.query or url.fragment or url.path not in ("", "/"):
        raise ValueError("base URL must be an origin without path, query or fragment")
    return base.rstrip("/") + "/api/knowledge-search-settings"


def http_reader(base, cookie, timeout):
    address = endpoint(base)
    if not cookie or "\n" in cookie or "\r" in cookie:
        raise ValueError("set CWP_SESSION_COOKIE to the Owner session cookie")
    opener = urllib.request.build_opener(NoRedirect())

    def read(config_id, query=None):
        params = {"config_id": config_id}
        if query is not None:
            params.update(action="evaluate", q=query)
        req = urllib.request.Request(address + "?" + urllib.parse.urlencode(params),
                                     headers={"Cookie": cookie, "Accept": "application/json"}, method="GET")
        try:
            with opener.open(req, timeout=timeout) as response:
                if response.headers.get_content_type() != "application/json":
                    raise ValueError("server did not return JSON; check Owner authentication")
                body = response.read(2 * 1024 * 1024 + 1)
                if len(body) > 2 * 1024 * 1024:
                    raise ValueError("evaluation response exceeds limit")
                result = json.loads(body)
                if not isinstance(result, dict):
                    raise ValueError("invalid evaluation response")
                return result
        except urllib.error.HTTPError as err:
            raise ValueError(f"evaluation HTTP {err.code}; check authentication/config/version") from None
        except (urllib.error.URLError, TimeoutError, OSError):
            raise ValueError("evaluation connection failed or timed out") from None
    return read


def hit_keys(result):
    hits = result.get("Hits")
    if hits is None:
        return []
    if not isinstance(hits, list) or len(hits) > 10:
        raise ValueError("expected at most 10 hits")
    keys = []
    for hit in hits:
        if not isinstance(hit, dict) or not isinstance(hit.get("Key"), str) or not hit["Key"]:
            raise ValueError("invalid hit identity")
        keys.append(hit["Key"])
    if len(set(keys)) != len(keys):
        raise ValueError("duplicate returned identity")
    return keys


def evaluate(manifest, config_id, read):
    queries = manifest_queries(manifest)
    fingerprint = hashlib.sha256(json.dumps(manifest, sort_keys=True, ensure_ascii=False,
                                           separators=(",", ":")).encode()).hexdigest()
    report = {"schema": 1, "manifest_sha256": fingerprint, "config_id": config_id,
              "read_only": True, "real_embeddings_verified": False, "cost": None,
              "status": "blocked", "quality_gate_passed": False, "queries": [], "groups": {}}
    identity = None

    def check(value):
        current = value.get("identity")
        cfg = value.get("config")
        if not isinstance(current, str) or not current or not isinstance(cfg, dict) or cfg.get("id") != config_id:
            raise ValueError("invalid configuration/quality identity")
        if identity is not None and current != identity:
            raise ValueError("index/configuration/quality identity changed during evaluation")
        return current

    try:
        identity = check(read(config_id))
        report["identity"] = identity
        for item in queries:
            response = read(config_id, item["query"])
            check(response)
            if response.get("read_only") is not True:
                raise ValueError("server did not confirm read-only evaluation")
            lexical, hybrid = response.get("lexical"), response.get("hybrid")
            if not isinstance(lexical, dict) or not isinstance(hybrid, dict) or lexical.get("Method") != "fts":
                raise ValueError("invalid retrieval adapters")
            lkeys, hkeys = hit_keys(lexical), hit_keys(hybrid)
            relevant = set(item["relevant_keys"])
            semantic = hybrid.get("Method") == "rrf" and not hybrid.get("Degradation")
            report["queries"].append({"id": item["id"], "group": item["group"],
                                      "lexical_keys": lkeys, "hybrid_keys": hkeys,
                                      "lexical_recall_at_10": len(relevant.intersection(lkeys)) / len(relevant),
                                      "hybrid_recall_at_10": len(relevant.intersection(hkeys)) / len(relevant) if semantic else None,
                                      "semantic_available": semantic})
        check(read(config_id))
        for group in ("original", "rewrite"):
            rows = [r for r in report["queries"] if r["group"] == group]
            report["groups"][group] = {
                "count": len(rows), "lexical_recall_at_10": sum(r["lexical_recall_at_10"] for r in rows) / len(rows),
                "hybrid_recall_at_10": sum(r["hybrid_recall_at_10"] for r in rows) / len(rows) if all(r["semantic_available"] for r in rows) else None}
        report["identity_stable"] = True
        report["status"] = "measured" if all(r["semantic_available"] for r in report["queries"]) else "incomplete"
    except (ValueError, KeyError, TypeError, json.JSONDecodeError) as err:
        report["identity_stable"] = False
        report["error"] = str(err)
        # A partial set under changing permissions is never aggregated as a result.
        report["groups"] = {}
    return report


def save_report(path, report):
    path = Path(path)
    fd, name = tempfile.mkstemp(prefix=".semantic-eval-", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as out:
            json.dump(report, out, ensure_ascii=False, indent=2)
            out.write("\n")
        os.replace(name, path)
    finally:
        if os.path.exists(name):
            os.unlink(name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--base-url", default="http://127.0.0.1:8080")
    parser.add_argument("--config-id", required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--timeout", type=float, default=30)
    args = parser.parse_args()
    try:
        if not args.config_id.strip() or not 0 < args.timeout <= 120:
            raise ValueError("invalid config ID or timeout")
        manifest = json.loads(args.manifest.read_text())
        manifest_queries(manifest)
        report = evaluate(manifest, args.config_id, http_reader(args.base_url, os.environ.get("CWP_SESSION_COOKIE"), args.timeout))
        save_report(args.output, report)
        print(f"status={report['status']}; report saved; no quality gate was changed")
        return 0 if report["status"] == "measured" else 2
    except (ValueError, OSError) as err:
        parser.exit(2, f"evaluation failed: {err}\n")


if __name__ == "__main__":
    raise SystemExit(main())
