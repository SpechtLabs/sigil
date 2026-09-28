"""Exercise regression detection and baseline workload preparation."""

import contextlib
import io
from pathlib import Path
import shutil
import tempfile
import unittest

import benchmark


def raw(ns=100, size=64, allocs=2, count=10, name="Eval"):
    return "pkg: example/policy\n" + "".join(
        f"Benchmark{name}-2 1000 {ns} ns/op {size} B/op {allocs} allocs/op\n"
        for _ in range(count)
    )


def table(old, new, delta="+20.00%", unit="sec/op", p="p=0.000 n=10"):
    return (
        "pkg: example/policy\n,base.txt,,head.txt,,,\n"
        f",{unit},CI,{unit},CI,vs base,P\n"
        f"Eval-2,{old},0%,{new},0%,{delta},{p}\n"
        f"geomean,{old},,{new},,{delta},\n"
    )


class RegressionTest(unittest.TestCase):
    def test_significant_slowdown_fails(self):
        self.assertEqual(len(benchmark.regressions(table(100, 120))), 1)

    def test_small_noisy_or_improving_changes_pass(self):
        for data in (table(100, 110, "+10.00%"), table(100, 105, "+5.00%"),
                     table(100, 150, "~", p="p=0.512 n=10"),
                     table(100, 80, "-20.00%")):
            with self.subTest(data=data):
                self.assertEqual(benchmark.regressions(data), [])

    def test_uses_unrounded_values_at_threshold(self):
        self.assertEqual(len(benchmark.regressions(table(100, 110.004, "+10.00%"))), 1)

    def test_memory_and_zero_to_nonzero_regressions_fail(self):
        for unit in ("B/op", "allocs/op"):
            with self.subTest(unit=unit):
                self.assertEqual(len(benchmark.regressions(table(0, 1, "+∞%", unit))), 1)
                self.assertEqual(len(benchmark.regressions(table(10, 12, unit=unit))), 1)

    def test_empty_missing_renamed_or_incomplete_samples_are_errors(self):
        for before, after in (("", ""), (raw(), raw(name="Renamed")),
                              (raw(), raw(count=9)), (raw(count=11), raw()),
                              (raw(), raw().replace(" 2 allocs/op", ""))):
            with self.subTest(after=after):
                with self.assertRaises(ValueError):
                    benchmark.validate_samples(before, after, 10)

    def test_valid_samples_and_throughput_are_accepted(self):
        benchmark.validate_samples(raw(), raw(110), 10)
        benchmark.samples(raw().replace(" ns/op", " ns/op 123.45 MB/s"))

    def test_invalid_numbers_are_errors(self):
        for value in ("nan", "inf", "-1"):
            with self.subTest(value=value):
                with self.assertRaises(ValueError):
                    benchmark.samples(raw(ns=value))
                with self.assertRaises(ValueError):
                    benchmark.regressions(table(100, value))

    def test_empty_or_unexpected_csv_does_not_pass(self):
        for data in ("", "pkg: example/policy\n", table(100, 120).replace(",CI,vs base,P", ",CI,changed,P")):
            with self.subTest(data=data):
                with self.assertRaises(ValueError):
                    benchmark.regressions(data)

    def test_requires_ten_samples(self):
        with self.assertRaisesRegex(ValueError, "at least 10"):
            benchmark.compare(Path("unused"), "unused", 9)

    def test_replaces_stale_workloads_without_touching_library_code(self):
        with tempfile.TemporaryDirectory() as tmp:
            head, base = Path(tmp) / "head", Path(tmp) / "base"
            for root in (head, base):
                (root / "internal/benchtest").mkdir(parents=True)
            (base / "stale_bench_test.go").write_text("stale")
            (base / "library.go").write_text("original library")
            (base / "internal/benchtest/old.go").write_text("old fixture")
            (head / "current_bench_test.go").write_text("new workload")
            (head / "internal/benchtest/fixture.go").write_text("new fixture")
            benchmark.install_workloads(head, base, [Path("current_bench_test.go")])
            self.assertFalse((base / "stale_bench_test.go").exists())
            self.assertFalse((base / "internal/benchtest/old.go").exists())
            self.assertEqual((base / "library.go").read_text(), "original library")
            self.assertEqual((base / "current_bench_test.go").read_text(), "new workload")
            self.assertEqual((base / "internal/benchtest/fixture.go").read_text(), "new fixture")

    @unittest.skipUnless(shutil.which("benchstat"), "benchstat integration runs in benchmark CI")
    def test_real_benchstat_detects_cpu_and_memory_regressions(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            (output / "base.txt").write_text(raw())
            for measured, failed in ((raw(), False), (raw(120), True),
                                     (raw(size=128), True), (raw(allocs=3), True),
                                     (raw(105), False), (raw(80), False)):
                with self.subTest(measured=measured):
                    (output / "head.txt").write_text(measured)
                    with contextlib.redirect_stdout(io.StringIO()):
                        self.assertEqual(benchmark.compare(output, "benchstat", 10), failed)
                    self.assertTrue((output / "summary.md").is_file())


if __name__ == "__main__":
    unittest.main()
