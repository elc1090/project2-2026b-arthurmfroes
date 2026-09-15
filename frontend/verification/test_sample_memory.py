import importlib.util
import os
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("sampler", Path(__file__).with_name("sample-memory.py"))
sampler = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sampler)


class MemorySamplerTest(unittest.TestCase):
    def test_summary_separates_sampled_peak_from_prior_cgroup_peak(self):
        def sample(phase, rss, current, peak):
            return {"phase": phase, "browser": {"rss_bytes": rss, "pss_bytes": rss // 2}, "containers": {"backend": {"memory_current_bytes": current, "memory_peak_bytes": peak}}}
        samples = [sample("baseline", 100, 50, 1000), sample("hashing", 200, 60, 1000), sample("hashing", 180, 80, 1000)]
        summary = sampler.summarize(samples, {})
        self.assertEqual(summary["baseline"]["browser"]["rss_bytes"], 100)
        hashing = summary["phases"]["hashing"]
        self.assertEqual(hashing["browser_rss_sampled_max_bytes"], 200)
        self.assertEqual(hashing["browser_pss_sampled_max_bytes"], 100)
        self.assertEqual(hashing["containers"]["backend"]["memory_current_sampled_max_bytes"], 80)
        self.assertEqual(hashing["containers"]["backend"]["memory_peak_cgroup_lifetime_max_bytes"], 1000)

    def test_pid_identity_and_unavailable_process_are_not_zero_memory(self):
        info = sampler.process_info(os.getpid())
        self.assertEqual(info["pid"], os.getpid())
        self.assertEqual(sampler.descendants(os.getpid(), "wrong-start"), [])
        self.assertIn("unavailable", sampler.process_memory(999999999))
        self.assertEqual(sampler.numeric_max(None, None), None)


if __name__ == "__main__":
    unittest.main()
