import pathlib
import tempfile
import types
import unittest
from reconcile import Reconciler, InvalidSnapshot, snapshot, render, install


def response(version=1, members=None, ttl=3000):
    if members is None:
        members = [{"ID": "node-1", "State": "ready", "BackendEndpoint": "http://backend-1:8080"}]
    return {"configuration": {"Version": version, "Members": members}, "valid_for_ms": ttl}


class ReconcileTests(unittest.TestCase):
    def test_zero_and_ready_render(self):
        template = pathlib.Path(__file__).with_name("nginx.conf").read_text()
        self.assertIn("return 503;", render(template, "127.0.0.11", ()))
        self.assertIn("http://backend-1:8080", render(template, "127.0.0.11", snapshot(response())[2]))
        self.assertIn("location ~ ^/internal", template)
        self.assertIn("try_files", template)

    def test_invalid_members(self):
        for endpoint in ("http://good;return:80", "http://user:password@host", "http://host/path", "http://host:99999", "http://host\n", "http://host?x=1"):
            with self.subTest(endpoint=endpoint), self.assertRaises((InvalidSnapshot, ValueError)):
                snapshot(response(members=[{"ID": "a", "State": "ready", "BackendEndpoint": endpoint}]))
        with self.assertRaises(InvalidSnapshot):
            snapshot(response(members=[{"ID": "a", "State": "syncing", "BackendEndpoint": "http://host"}]))

    def test_fallback_expiration_and_same_version_renewal(self):
        now = [0.0]
        mode = ["healthy"]
        calls = []
        def fetch(raw):
            calls.append(raw)
            if raw == "http://first" or mode[0] == "down":
                raise OSError("unreachable")
            return response()
        r = Reconciler(["http://first", "http://second"], "token", 1, 3, fetch, lambda: now[0])
        self.assertTrue(r.poll()); self.assertEqual(calls, ["http://first", "http://second"])
        now[0] = 2; self.assertTrue(r.poll()); self.assertEqual(r.deadline, 5)
        mode[0] = "down"; now[0] = 4; self.assertTrue(r.poll())
        now[0] = 6; self.assertEqual(r.poll(), ())
        mode[0] = "healthy"; self.assertTrue(r.poll())

    def test_old_versions_and_conflicting_same_version_do_not_refresh(self):
        now = [0.0]; data = [response(version=5)]
        r = Reconciler(["http://control"], "token", 1, 2, lambda _: data[0], lambda: now[0])
        r.poll(); data[0] = response(version=4); now[0] = 1
        r.poll(); self.assertEqual(r.deadline, 2)
        data[0] = response(version=5, members=[]); r.poll(); self.assertEqual(r.deadline, 2)
        now[0] = 3; self.assertEqual(r.poll(), ())

    def test_request_duration_consumes_authority(self):
        now = [0.0]
        def fetch(_):
            now[0] += 2
            return response(ttl=1000)
        r = Reconciler(["http://control"], "token", 1, 3, fetch, lambda: now[0])
        self.assertEqual(r.poll(), ())

    def test_validation_and_reload_failure_restore_disk(self):
        with tempfile.TemporaryDirectory() as directory:
            path = pathlib.Path(directory) / "nginx.conf"; path.write_text("old")
            results = iter([1])
            runner = lambda *a, **kw: types.SimpleNamespace(returncode=next(results))
            self.assertFalse(install(path, "new", True, runner)); self.assertEqual(path.read_text(), "old")
            results = iter([0, 1])
            self.assertFalse(install(path, "new", True, runner)); self.assertEqual(path.read_text(), "old")
            results = iter([0, 0])
            self.assertTrue(install(path, "new", True, runner)); self.assertEqual(path.read_text(), "new")


if __name__ == "__main__":
    unittest.main()
