"""The gate surface manifest: the single place that says *what belongs to what*.

What this file is
-----------------
LingDoc's CI is a set of *gate surfaces* -- groups of paths that one job is
responsible for watching.  This manifest is the one declaration of that
ownership: for every surface it records

* the surface name (unique),
* the path set that makes up the surface,
* the workflow file, job key and job display name of every job that consumes
  it,
* the gate level (``repository`` or ``targeted``),
* whether that job is skipped on a formatting-only change,
* the kind of the path set (``glob``, ``prefix`` or ``exact``).

The manifest is **declarative data**.  It answers ownership questions only:
which job watches which paths.  It contains no decision logic, and it does not
decide whether a gate runs -- the scope scripts keep doing that.

How to read the surface/job relation
-------------------------------------
``SURFACES`` is a list of records; each record's ``consumers`` lists the jobs
that watch that surface.  The relation is many-to-many and is read like this:

* one surface may be consumed by several jobs (the frontend surface is watched
  by both the test job and the image job of the frontend workflow);
* one job may consume several surfaces (the PR gate job watches the provider
  packages, the frontend, the contracts and the governance surface).

How to add a surface
--------------------
1. Append a record to ``SURFACES`` with the fields listed above.  Keep the
   path set byte-for-byte identical to what the job actually watches.
2. Run ``python scripts/ci/lingdoc_gate_surfaces.py``.  The loader validates
   the record: the surface name must be unique, the path set must not be
   empty, the workflow file must exist, the job key must exist in that
   workflow, and the job display name must match the workflow.  A broken
   record aborts with a non-zero exit status -- it never degrades silently.
3. Only then wire the workflow to the surface (that is a later ticket).

Terms
-----
``gate_level``
    ``repository`` -- the job runs the whole surface; the path set only
    decides whether the job is triggered.  ``targeted`` -- the job runs the
    part of the surface the contribution actually touched.

``path_kind``
    ``glob`` -- gitignore-style patterns, as the workflow path tables spell
    them.  ``prefix`` -- directory prefixes, as the PR gate's provider,
    frontend and contracts triggers spell them.  ``exact`` -- exact file
    paths.

``skipped_on_format_only``
    Whether the consuming job is skipped outright when the change is
    formatting-only.  A lint job is the judge of formatting, so it is never
    skipped; test jobs are skipped.  This is a job-level fact: the PR gate job
    itself is never skipped, while its provider and frontend *steps* honour
    the formatting-only shortcut.

Ownership knowledge (provider roots, contracts directory, governance file
set, frontend root) is registered here exactly once.  ``lingdoc_risk_surface``
and ``lingdoc_pr_scope`` still carry their own copies of it; ticket T2 moves
them over to this manifest, so this file is where those constants come from
after that change.
"""
from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import sys
from typing import Any, Iterable, Mapping, Sequence

def _yaml():
    """PyYAML, imported only when the manifest is checked against the workflows.

    The manifest's *data* -- the surfaces and the ownership knowledge -- is
    plain Python and must stay readable without any dependency: the
    workflow-level gates read it in a job that installs nothing.
    """
    try:
        import yaml
    except ImportError as error:  # pragma: no cover - dependency guard
        raise ImportError(
            "PyYAML is required to validate the gate surface manifest; install it "
            "with: python -m pip install -r scripts/ci/requirements-lingdoc.txt"
        ) from error
    return yaml

REPO_ROOT = Path(__file__).resolve().parents[2]

GATE_LEVEL_REPOSITORY = "repository"
GATE_LEVEL_TARGETED = "targeted"
GATE_LEVELS = (GATE_LEVEL_REPOSITORY, GATE_LEVEL_TARGETED)

PATH_KIND_GLOB = "glob"
PATH_KIND_PREFIX = "prefix"
PATH_KIND_EXACT = "exact"
PATH_KINDS = (PATH_KIND_GLOB, PATH_KIND_PREFIX, PATH_KIND_EXACT)

# --- ownership knowledge: registered here, once -----------------------------
PROVIDER_ROOTS = ("internal/lingdoc", "internal/evidence",
                  "internal/models/acceptancebudget", "cmd/acceptance-budget-reconcile")
CONTRACT_DIR = "docs/08-本轮实施方案/contracts/"
GOVERNANCE_FILES = (".github/workflows/lingdoc-review.yml",
                     ".github/pull_request_template.md")
GOVERNANCE_DIRS = ("scripts/ci/",)
FRONTEND_ROOT = "frontend/"

# --- the surfaces -----------------------------------------------------------
# The path sets of the seven workflow-level gates are byte-for-byte the path
# tables those workflows feed to the scope script.  Do not reformat them: the
# wiring test compares them with the workflow files.
SURFACES: tuple[Mapping[str, Any], ...] = (
    {
        "name": "frontend",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_GLOB,
        "paths": (
            "frontend/**",
            "scripts/build_frontend_dist.sh",
            ".github/workflows/frontend.yml",
        ),
        "consumers": (
            {"workflow": ".github/workflows/frontend.yml",
             "job": "verify",
             "job_name": "Test, type-check, and build"},
            {"workflow": ".github/workflows/frontend.yml",
             "job": "docker-image",
             "job_name": "Docker multi-stage image"},
        ),
        "skipped_on_format_only": True,
    },
    {
        "name": "go-lint",
        "gate_level": GATE_LEVEL_TARGETED,
        "path_kind": PATH_KIND_GLOB,
        "paths": (
            "**/*.go",
            "**/go.mod",
            "**/go.sum",
            ".golangci.yml",
            ".github/workflows/go-lint.yml",
        ),
        "consumers": (
            {"workflow": ".github/workflows/go-lint.yml",
             "job": "changes",
             "job_name": "detect lint modules"},
            {"workflow": ".github/workflows/go-lint.yml",
             "job": "golangci",
             "job_name": "golangci-lint (${{ matrix.module }})"},
        ),
        "skipped_on_format_only": False,
    },
    {
        "name": "app",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_GLOB,
        "paths": (
            "**/*.go",
            "!cli/**",
            "!client/**",
            "!docreader/**",
            "!third_party/anydoc-go/**",
            "go.mod",
            "go.sum",
            "Makefile",
            "config/**",
            "migrations/**",
            "skills/preloaded/**",
            "testdata/**",
            "docker/Dockerfile.app",
            "LICENSE",
            "THIRD_PARTY_NOTICES.md",
            "licenses/**",
            "internal/textconv/data/**",
            "scripts/*license*.sh",
            "cmd/desktop/build/windows/installer/project.nsi",
            ".github/workflows/app.yml",
        ),
        "consumers": (
            {"workflow": ".github/workflows/app.yml",
             "job": "verify",
             "job_name": "Format, vet, test, and build"},
        ),
        "skipped_on_format_only": True,
    },
    {
        "name": "lingdoc-mock",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_GLOB,
        "paths": (
            "scripts/lingdoc_mock/**",
            "scripts/ci/requirements-lingdoc.txt",
            "scripts/ci/check_lingdoc_routes.py",
            "docs/08-本轮实施方案/contracts/**",
            "frontend/src/dev/lingdoc-mock/**",
            "frontend/lingdoc-mock.html",
            "frontend/src/utils/request.ts",
            "frontend/package*.json",
            "frontend/vite.config.ts",
            ".github/workflows/lingdoc-mock.yml",
        ),
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-mock.yml",
             "job": "mock",
             "job_name": "T06 fixed mock and request consumer"},
        ),
        "skipped_on_format_only": True,
    },
    {
        "name": "lingdoc-f01-runner",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_GLOB,
        "paths": (
            "scripts/lingdoc_mock/run_f01.py",
            "scripts/lingdoc_mock/load_state.py",
            "scripts/lingdoc_mock/run_scenario.py",
            "scripts/lingdoc_mock/build_t15_matrix.py",
            "scripts/lingdoc_mock/test_workflow*.py",
            "docs/08-本轮实施方案/contracts/**",
            "docs/08-本轮实施方案/README.md",
            "docs/08-本轮实施方案/02-接口与Mock约定.md",
            "docs/08-本轮实施方案/T15-11-验证报告.json",
            "docs/08-本轮实施方案/T15-11-证据.json",
            ".github/workflows/lingdoc-f01-runner.yml",
        ),
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-f01-runner.yml",
             "job": "runner-tests",
             "job_name": "F01 runner regression"},
        ),
        "skipped_on_format_only": True,
    },
    {
        "name": "lingdoc-workspace-concurrency",
        "gate_level": GATE_LEVEL_TARGETED,
        "path_kind": PATH_KIND_GLOB,
        "paths": (
            "internal/lingdoc/workspacecore/**",
            "migrations/sqlite/000018_lingdoc_workspace.*.sql",
            "go.mod",
            "go.sum",
            ".github/workflows/lingdoc-workspace-concurrency.yml",
        ),
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-workspace-concurrency.yml",
             "job": "workspace-concurrency",
             "job_name": "workspace-concurrency"},
        ),
        "skipped_on_format_only": True,
    },
    {
        "name": "lingdoc-interface-integration",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_GLOB,
        "paths": (
            "internal/lingdoc/**",
            "internal/evidence/**",
            "scripts/acceptance/**",
            "internal/container/lingdoc*",
            "internal/router/lingdoc*",
            ".github/workflows/lingdoc-interface-integration.yml",
        ),
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-interface-integration.yml",
             "job": "integration",
             "job_name": "integration"},
            {"workflow": ".github/workflows/lingdoc-interface-integration.yml",
             "job": "frontend",
             "job_name": "frontend"},
        ),
        "skipped_on_format_only": True,
    },
    {
        "name": "pr-gate-provider",
        "gate_level": GATE_LEVEL_TARGETED,
        "path_kind": PATH_KIND_PREFIX,
        "paths": PROVIDER_ROOTS,
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-review.yml",
             "job": "gate",
             "job_name": "LingDoc PR gate"},
        ),
        "skipped_on_format_only": False,
    },
    {
        "name": "pr-gate-frontend",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_PREFIX,
        "paths": (FRONTEND_ROOT,),
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-review.yml",
             "job": "gate",
             "job_name": "LingDoc PR gate"},
        ),
        "skipped_on_format_only": False,
    },
    {
        "name": "contracts",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_PREFIX,
        "paths": (CONTRACT_DIR,),
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-review.yml",
             "job": "gate",
             "job_name": "LingDoc PR gate"},
        ),
        "skipped_on_format_only": False,
    },
    {
        "name": "governance-files",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_EXACT,
        "paths": GOVERNANCE_FILES,
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-review.yml",
             "job": "gate",
             "job_name": "LingDoc PR gate"},
        ),
        "skipped_on_format_only": False,
    },
    {
        "name": "governance-scripts",
        "gate_level": GATE_LEVEL_REPOSITORY,
        "path_kind": PATH_KIND_PREFIX,
        "paths": GOVERNANCE_DIRS,
        "consumers": (
            {"workflow": ".github/workflows/lingdoc-review.yml",
             "job": "gate",
             "job_name": "LingDoc PR gate"},
        ),
        "skipped_on_format_only": False,
    },
)

SURFACE_FIELDS = ("name", "gate_level", "path_kind", "paths", "consumers",
                  "skipped_on_format_only")
CONSUMER_FIELDS = ("workflow", "job", "job_name")


class SurfaceManifestError(Exception):
    """The manifest is invalid; it must not be used as it is."""


@dataclass(frozen=True)
class Consumer:
    """A job that watches a surface."""

    workflow: str
    job: str
    job_name: str


@dataclass(frozen=True)
class Surface:
    """One gate surface and the jobs that consume it."""

    name: str
    paths: tuple[str, ...]
    consumers: tuple[Consumer, ...]
    gate_level: str
    path_kind: str
    skipped_on_format_only: bool


@dataclass(frozen=True)
class Manifest:
    """The validated manifest."""

    surfaces: tuple[Surface, ...]
    provider_roots: tuple[str, ...]
    contract_dir: str
    governance_files: tuple[str, ...]
    governance_dirs: tuple[str, ...]
    frontend_root: str


def _require_mapping(value: Any, what: str) -> Mapping[str, Any]:
    if not isinstance(value, Mapping):
        raise SurfaceManifestError(f"{what} must be a mapping, got {value!r}")
    return value


def _require_fields(entry: Mapping[str, Any], fields: Iterable[str], what: str) -> None:
    for field in fields:
        if field not in entry:
            raise SurfaceManifestError(f"{what} is missing field {field!r}")


def _require_text(value: Any, what: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise SurfaceManifestError(f"{what} must be a non-empty string, got {value!r}")
    return value


def _consumer_from_record(entry: Any, surface_name: str) -> Consumer:
    what = f"surface {surface_name!r} consumer"
    mapping = _require_mapping(entry, what)
    _require_fields(mapping, CONSUMER_FIELDS, what)
    return Consumer(
        workflow=_require_text(mapping["workflow"], f"{what} workflow"),
        job=_require_text(mapping["job"], f"{what} job"),
        job_name=_require_text(mapping["job_name"], f"{what} job_name"),
    )


def _surface_from_record(entry: Any, index: int) -> Surface:
    what = f"surface record #{index}"
    mapping = _require_mapping(entry, what)
    _require_fields(mapping, SURFACE_FIELDS, what)
    name = _require_text(mapping["name"], f"{what} name")
    gate_level = _require_text(mapping["gate_level"], f"surface {name!r} gate level")
    if gate_level not in GATE_LEVELS:
        raise SurfaceManifestError(
            f"surface {name!r} has an unknown gate level {gate_level!r}; "
            f"expected one of {', '.join(GATE_LEVELS)}")
    path_kind = _require_text(mapping["path_kind"], f"surface {name!r} path kind")
    if path_kind not in PATH_KINDS:
        raise SurfaceManifestError(
            f"surface {name!r} has an unknown path kind {path_kind!r}; "
            f"expected one of {', '.join(PATH_KINDS)}")
    paths = mapping["paths"]
    if not isinstance(paths, (list, tuple)) or not paths:
        raise SurfaceManifestError(f"surface {name!r} has an empty path set")
    for path in paths:
        _require_text(path, f"surface {name!r} path")
    consumers = mapping["consumers"]
    if not isinstance(consumers, (list, tuple)) or not consumers:
        raise SurfaceManifestError(f"surface {name!r} declares no consuming job")
    if not isinstance(mapping["skipped_on_format_only"], bool):
        raise SurfaceManifestError(
            f"surface {name!r} must state skipped_on_format_only as true or false")
    return Surface(
        name=name,
        paths=tuple(paths),
        consumers=tuple(_consumer_from_record(consumer, name) for consumer in consumers),
        gate_level=gate_level,
        path_kind=path_kind,
        skipped_on_format_only=mapping["skipped_on_format_only"],
    )


def _workflow_jobs(root: Path, workflow: str) -> Mapping[str, Any]:
    yaml = _yaml()
    path = root / workflow
    if not path.is_file():
        raise SurfaceManifestError(f"workflow file not found: {workflow}")
    try:
        document = yaml.safe_load(path.read_text(encoding="utf-8"))
    except yaml.YAMLError as error:
        raise SurfaceManifestError(
            f"workflow file {workflow} is not valid YAML: {error}") from error
    jobs = (document or {}).get("jobs")
    if not isinstance(jobs, Mapping):
        raise SurfaceManifestError(f"workflow file {workflow} declares no jobs")
    return jobs


def _check_consumers(surfaces: Sequence[Surface], root: Path) -> None:
    cache: dict[str, Mapping[str, Any]] = {}
    for surface in surfaces:
        for consumer in surface.consumers:
            jobs = cache.get(consumer.workflow)
            if jobs is None:
                jobs = _workflow_jobs(root, consumer.workflow)
                cache[consumer.workflow] = jobs
            if consumer.job not in jobs:
                raise SurfaceManifestError(
                    f"job not found: {consumer.job!r} is not a job of "
                    f"{consumer.workflow}, which surface {surface.name!r} points at")
            declared = jobs[consumer.job].get("name") or consumer.job
            if declared != consumer.job_name:
                raise SurfaceManifestError(
                    f"surface {surface.name!r} registers the display name "
                    f"{consumer.job_name!r} for job {consumer.job!r}, but "
                    f"{consumer.workflow} calls it {declared!r}")


def validate(records: Sequence[Mapping[str, Any]], root: Path) -> Manifest:
    """Validate manifest records and return the manifest they describe.

    Every failure raises :class:`SurfaceManifestError`: an invalid manifest is
    never used, and never silently degraded.
    """
    if not isinstance(records, (list, tuple)) or not records:
        raise SurfaceManifestError("the manifest declares no gate surface")
    surfaces: list[Surface] = []
    seen: dict[str, int] = {}
    for index, entry in enumerate(records):
        surface = _surface_from_record(entry, index)
        if surface.name in seen:
            raise SurfaceManifestError(
                f"duplicate surface name {surface.name!r}: records #{seen[surface.name]} "
                f"and #{index} declare the same surface")
        seen[surface.name] = index
        surfaces.append(surface)
    _check_consumers(surfaces, root)
    return Manifest(
        surfaces=tuple(surfaces),
        provider_roots=tuple(PROVIDER_ROOTS),
        contract_dir=CONTRACT_DIR,
        governance_files=tuple(GOVERNANCE_FILES),
        governance_dirs=tuple(GOVERNANCE_DIRS),
        frontend_root=FRONTEND_ROOT,
    )


def load_and_validate(root: Path | None = None,
                      records: Sequence[Mapping[str, Any]] | None = None) -> Manifest:
    """Load the committed manifest and validate it against the repository."""
    return validate(SURFACES if records is None else records,
                    REPO_ROOT if root is None else root)


def main() -> int:
    """Validate the committed manifest; exit non-zero when it is invalid."""
    try:
        loaded = load_and_validate()
    except SurfaceManifestError as error:
        print(f"invalid gate surface manifest: {error}", file=sys.stderr)
        return 1
    for surface in loaded.surfaces:
        consumers = ", ".join(f"{consumer.job} ({consumer.workflow})"
                              for consumer in surface.consumers)
        print(f"{surface.name}: {surface.gate_level}/{surface.path_kind} -> {consumers}")
    print(f"{len(loaded.surfaces)} gate surfaces validated")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
