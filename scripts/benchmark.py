#!/usr/bin/env python3
"""Run identical Go benchmark workloads on this checkout and a base revision."""

import argparse
import csv
import io
import json
import math
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import tempfile

ROOT = Path(__file__).resolve().parents[1]
UNITS = {"ns/op", "B/op", "allocs/op"}
CSV_UNITS = {"sec/op", "B/op", "allocs/op"}


def command(args, cwd=ROOT, env=None):
    return subprocess.run(args, cwd=cwd, env=env, check=True, text=True, capture_output=True).stdout


def benchmark_files(root):
    # Respect Go module boundaries, excluding worktrees and the examples module.
    dirs = command(["go", "list", "-f", "{{.Dir}}", "./..."], cwd=root).splitlines()
    files = []
    for directory in dirs:
        for path in Path(directory).glob("*_test.go"):
            if re.search(r"^func Benchmark\w*\(", path.read_text(), re.MULTILINE):
                if not path.name.endswith("_bench_test.go"):
                    raise ValueError(f"move benchmarks to *_bench_test.go for baseline copying: {path}")
                files.append(path.relative_to(root))
    return sorted(files)


def unpack_snapshot(archive, base):
    # Git sources need only directories and regular files here. Extract them
    # explicitly so the runner also works on Python 3.9, without tar filters.
    with tarfile.open(fileobj=io.BytesIO(archive)) as tar:
        for member in tar:
            dest = base / member.name
            dest.resolve().relative_to(base.resolve())
            if member.isdir():
                dest.mkdir(parents=True, exist_ok=True)
            elif member.isfile():
                dest.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(member) as src, dest.open("wb") as out:
                    shutil.copyfileobj(src, out)
                dest.chmod(member.mode & 0o777)
            else:
                raise ValueError("unsupported Git archive entry: " + member.name)


def install_workloads(head, base, files):
    # Removed or renamed benchmarks must not leave stale base-only workloads.
    for p in base.rglob("*_bench_test.go"):
        p.unlink()
    for rel in files:
        dest = base / rel
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(head / rel, dest)
    helpers = base / "internal/benchtest"
    if helpers.exists():
        shutil.rmtree(helpers)
    shutil.copytree(head / "internal/benchtest", helpers)


def samples(text):
    result = {}
    pkg = None
    for line in text.splitlines():
        if line.startswith("pkg: "):
            pkg = line[5:]
        if not line.startswith("Benchmark"):
            continue
        parts = line.split()
        if not pkg or len(parts) < 4 or not parts[1].isdigit() or int(parts[1]) <= 0:
            raise ValueError("malformed benchmark result: " + line)
        metrics = set()
        for i in range(2, len(parts) - 1, 2):
            value, unit = float(parts[i]), parts[i + 1]
            if not math.isfinite(value) or value < 0:
                raise ValueError("invalid measurement: " + line)
            if unit in UNITS:
                metrics.add(unit)
                result.setdefault((pkg, parts[0], unit), []).append(value)
        if metrics != UNITS:
            raise ValueError("benchmark must report time, bytes and allocations: " + line)
    if not result:
        raise ValueError("no benchmark measurements found")
    return result


def validate_samples(before, after, count):
    old, new = samples(before), samples(after)
    if old.keys() != new.keys():
        raise ValueError("benchmark names or metrics differ between base and head")
    for side, data in (("base", old), ("head", new)):
        for key, values in data.items():
            if len(values) != count:
                raise ValueError(f"{side}: {key} has {len(values)} samples, expected {count}")


def regressions(csv_text, threshold=0.10):
    """Read the pinned benchstat CSV; '~' already applies its significance test."""
    findings = []
    package = None
    unit = None
    compared = 0
    for row in csv.reader(io.StringIO(csv_text)):
        if len(row) == 1 and row[0].startswith("pkg: "):
            package = row[0][5:]
        if len(row) == 7 and row[0] == "" and row[1] in CSV_UNITS:
            if row[2:] != ["CI", row[1], "CI", "vs base", "P"]:
                raise ValueError("unexpected benchstat CSV header")
            unit = row[1]
            continue
        if not row or row[0] in ("", "geomean") or len(row) == 1:
            continue
        if unit is None or package is None or len(row) != 7:
            raise ValueError("unexpected benchstat CSV row: " + repr(row))
        old, new = float(row[1]), float(row[3])
        if not all(math.isfinite(v) and v >= 0 for v in (old, new)):
            raise ValueError("non-finite or negative benchstat median")
        compared += 1
        # Compute from unrounded medians, not benchstat's displayed percentage.
        if row[5] != "~" and row[6].startswith("p=") and new > old * (1 + threshold):
            delta = f"+{(new / old - 1) * 100:.2f}%" if old else "0 to nonzero"
            findings.append(f"{package}: {row[0]} {unit} {delta} ({row[6]})")
    if not compared:
        raise ValueError("benchstat produced no comparisons")
    return findings


def compare(output, benchstat, count):
    if count < 10:
        raise ValueError("a regression check requires at least 10 samples per revision")
    before, after = output / "base.txt", output / "head.txt"
    validate_samples(before.read_text(), after.read_text(), count)
    flags = [benchstat, "-alpha", "0.01", "-filter", ".unit:(ns/op OR B/op OR allocs/op)"]
    report = command(flags + [before.name, after.name], cwd=output)
    table = command(flags + ["-format", "csv", before.name, after.name], cwd=output)
    (output / "benchstat.txt").write_text(report)
    (output / "benchstat.csv").write_text(table)
    failures = regressions(table)
    summary = "# Go benchmark comparison\n\n"
    summary += "Gate: increase above 10% and benchstat significance at alpha 0.01, for time, bytes or allocations per operation.\n\n"
    summary += "\n".join("- " + line for line in failures) if failures else "No confirmed regressions above 10%."
    summary += "\n\n```text\n" + report + "```\n"
    (output / "summary.md").write_text(summary)
    print(summary, flush=True)
    return bool(failures)


def run(args):
    if args.count < 1 or args.cpu < 1:
        raise ValueError("count and cpu must be positive")
    if args.baseline and args.count < 10:
        raise ValueError("a baseline comparison requires at least 10 samples")
    output = Path(args.output).resolve()
    output.mkdir(parents=True, exist_ok=True)
    # Never append a new run to samples left by an earlier invocation.
    for name in ("base.txt", "head.txt", "benchstat.txt", "benchstat.csv", "summary.md"):
        (output / name).unlink(missing_ok=True)
    env = dict(os.environ, GOTOOLCHAIN="local", GOWORK="off", GOMAXPROCS=str(args.cpu))
    files = benchmark_files(ROOT)
    if not files:
        raise ValueError("no *_bench_test.go workloads found")
    packages = sorted({"./" + str(p.parent) for p in files})
    metadata = {"head": command(["git", "rev-parse", "HEAD"]).strip(),
                "dirty": bool(command(["git", "status", "--porcelain"])),
                "go": command(["go", "version"]).strip(), "cpu": args.cpu,
                "benchtime": args.benchtime, "samples": args.count,
                "packages": packages, "workloads": [str(p) for p in files]}
    with tempfile.TemporaryDirectory(prefix="sigil-bench-") as tmp:
        temp = Path(tmp)
        trees = {"head": ROOT}
        if args.baseline:
            sha = command(["git", "rev-parse", "--verify", args.baseline + "^{commit}"]).strip()
            metadata["base"] = sha
            base = temp / "base"
            base.mkdir()
            archive = subprocess.run(["git", "archive", "--format=tar", sha], cwd=ROOT, check=True, capture_output=True).stdout
            unpack_snapshot(archive, base)
            install_workloads(ROOT, base, files)
            trees = {"base": base, "head": ROOT}
        (output / "metadata.json").write_text(json.dumps(metadata, indent=2) + "\n")
        binaries = {}
        # Build everything first. Compiler processes never compete with samples.
        for side, tree in trees.items():
            for i, package in enumerate(packages):
                binary = temp / f"{side}-{i}.test"
                print(f"Building {side} {package}", flush=True)
                command(["go", "test", "-c", "-buildvcs=false", "-o", str(binary), package], cwd=tree, env=env)
                binaries[side, package] = binary
        for sample in range(args.count):
            sides = list(trees) if sample % 2 == 0 else list(reversed(trees))
            print(f"Sample {sample + 1}/{args.count}: {', '.join(sides)}", flush=True)
            for package in packages:
                for side in sides:
                    binary = binaries[side, package]
                    data = command([str(binary), "-test.run=^$", "-test.bench=" + args.bench,
                                    "-test.benchmem", "-test.count=1", "-test.cpu=" + str(args.cpu),
                                    "-test.benchtime=" + args.benchtime, "-test.timeout=3m"],
                                   cwd=trees[side] / package, env=env)
                    with (output / f"{side}.txt").open("a") as f:
                        f.write(data)
        # A mistyped filter must not turn the check green with no work done.
        samples((output / "head.txt").read_text())
        if args.baseline:
            return compare(output, args.benchstat, args.count)
    (output / "summary.md").write_text("# Go benchmarks\n\nMeasurements saved to head.txt; no base revision was selected.\n")
    return False


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline", default=os.environ.get("BENCH_BASE_REF"))
    parser.add_argument("--no-baseline", action="store_true", help="only measure the current checkout")
    parser.add_argument("--count", type=int, default=int(os.environ.get("BENCH_COUNT", "10")))
    parser.add_argument("--cpu", type=int, default=2)
    parser.add_argument("--benchtime", default=os.environ.get("BENCH_TIME", "200ms"))
    parser.add_argument("--bench", default=".")
    parser.add_argument("--output", default="benchmark-results")
    parser.add_argument("--benchstat", default="benchstat")
    args = parser.parse_args()
    if args.no_baseline:
        args.baseline = None
    try:
        failed = run(args)
    except (ValueError, OSError, subprocess.CalledProcessError) as err:
        print(str(err), file=sys.stderr)
        if isinstance(err, subprocess.CalledProcessError):
            print(err.stdout or "", file=sys.stderr)
            print(err.stderr or "", file=sys.stderr)
        return 2
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
