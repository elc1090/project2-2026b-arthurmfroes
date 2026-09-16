"""Opt-in Docker verification of the Railway init/helper process topology."""

import os
import subprocess
import time
import unittest
import uuid


@unittest.skipUnless(os.getenv("ACERVO_RAILWAY_RUNTIME_IMAGE"), "set ACERVO_RAILWAY_RUNTIME_IMAGE to a built Railway image")
class FaultRuntimeTest(unittest.TestCase):
    def docker(self, *args, check=True):
        return subprocess.run(["docker", *args], check=check, text=True, capture_output=True)

    def test_stop_restore_and_status_keep_init_available(self):
        image = os.environ["ACERVO_RAILWAY_RUNTIME_IMAGE"]
        name = "acervo-fault-runtime-" + uuid.uuid4().hex[:10]
        self.docker("run", "-d", "--name", name, "--entrypoint", "/usr/local/bin/tini", image,
                    "-g", "--", "/bin/sleep", "300")
        try:
            deadline = time.monotonic() + 5
            while True:
                status = self.docker("exec", name, "/usr/local/bin/fault-signal", "status", check=False)
                if status.returncode == 0:
                    break
                if time.monotonic() >= deadline:
                    self.fail(status.stderr)
                time.sleep(0.05)
            self.assertEqual(status.stdout, "running\n")
            stopped = self.docker("exec", name, "/usr/local/bin/fault-signal", "stop")
            self.assertEqual(stopped.stdout, "stopped\n")
            self.assertEqual(self.docker("exec", name, "/usr/local/bin/fault-signal", "status").stdout, "stopped\n")
            restored = self.docker("exec", name, "/usr/local/bin/fault-signal", "restore")
            self.assertEqual(restored.stdout, "running\n")
            self.assertEqual(self.docker("exec", name, "/usr/local/bin/fault-signal", "status").stdout, "running\n")
            init_stat = self.docker("exec", name, "/bin/cat", "/proc/1/stat").stdout
            init_state = init_stat.rsplit(")", 1)[1].split()[0]
            self.assertNotIn(init_state, {"T", "t"})
        finally:
            self.docker("rm", "-f", name, check=False)

    def test_multiple_init_children_fail_closed(self):
        image = os.environ["ACERVO_RAILWAY_RUNTIME_IMAGE"]
        name = "acervo-fault-topology-" + uuid.uuid4().hex[:10]
        self.docker("run", "-d", "--name", name, "--entrypoint", "/bin/sh", image,
                    "-c", "/bin/sleep 300 & /bin/sleep 300 & wait")
        try:
            deadline = time.monotonic() + 5
            while True:
                result = self.docker("exec", name, "/usr/local/bin/fault-signal", "status", check=False)
                if result.returncode != 126:
                    break
                if time.monotonic() >= deadline:
                    self.fail(result.stderr)
                time.sleep(0.05)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn("init must have exactly one direct child", result.stderr)
        finally:
            self.docker("rm", "-f", name, check=False)


if __name__ == "__main__":
    unittest.main()
