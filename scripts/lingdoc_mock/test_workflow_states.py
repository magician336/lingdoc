"""Starting-state loading, building and read-back; no live provider is used.

The provider double below answers the operations a build and a read-back use. It is
deliberately thin: these tests check what the *loader* does — which writes it issues
in which order, where each version precondition comes from, how a read-back is
compared and reported — not what the provider means. Provider semantics are checked
against the real service in docs/08-本轮实施方案/T15-起点状态装载.md.
"""
from __future__ import annotations

from contextlib import redirect_stderr, redirect_stdout
from email.message import Message
import io
import json
from pathlib import Path
import re
import tempfile
import unittest
from unittest.mock import patch
from urllib.parse import urlsplit

from scripts.lingdoc_mock import load_state as module
from scripts.lingdoc_mock.load_state import Bindings, StateLoader, load_state, main, state_from_declaration
from scripts.lingdoc_mock.run_f01 import (
    OPENAPI_PATH,
    SCENARIOS_PATH,
    WORKFLOW_PATH,
    ProviderClient,
    WorkflowError,
    operation_index,
)

OPENAPI = json.loads(OPENAPI_PATH.read_text(encoding="utf-8"))
SECTIONS = {"question": "研究问题", "method": "研究方案"}
OWNER_ID = "user-owner"
MEMBER_ID = "user-member"
PROVIDER_IDS = {"k-demo": "knowledge-1", "k-notready": "knowledge-2"}


class Response:
    def __init__(self, status: int, payload: dict) -> None:
        self.status = status
        self.payload = json.dumps(payload, ensure_ascii=False).encode("utf-8")
        self.headers = Message()
        self.headers["Content-Type"] = "application/json"

    def read(self, limit: int = -1) -> bytes:
        return self.payload[:limit]

    def close(self) -> None:
        pass


class SyntheticProvider:
    """A tiny provider holding projects, chapters and bound assets."""

    def __init__(self, project_name: str | None = None, chapter_body: str | None = None,
                 asset_states: dict[str, str] | None = None, deny_reasons: dict[str, str] | None = None,
                 failures: dict[str, int] | None = None) -> None:
        self.project_name_override = project_name
        self.chapter_body_override = chapter_body
        self.asset_states = dict(asset_states or {})
        self.deny_reasons = dict(deny_reasons or {})
        self.failures = dict(failures or {})
        self.projects: dict[str, dict] = {}
        self.chapters: dict[str, list[dict]] = {}
        self.assets: dict[str, list[dict]] = {}
        self.calls: list[dict] = []
        self.counter = 0
        self.idempotency: dict[tuple[str, str], tuple[str, Response]] = {}

    # ---- the opener seam --------------------------------------------------

    def open(self, request, timeout):
        method = request.get_method()
        path = urlsplit(request.full_url).path
        body = json.loads(request.data.decode("utf-8")) if request.data else None
        headers = {name.lower(): value for name, value in request.headers.items()}
        self.calls.append({"method": method, "url": request.full_url, "path": path,
                           "headers": headers, "body": body})
        idempotency_key = headers.get("idempotency-key")
        identity = request.get_header("Authorization") or request.get_header("authorization") or ""
        replay_key = (identity, idempotency_key) if idempotency_key else None
        fingerprint = json.dumps([method, path, body], ensure_ascii=False, sort_keys=True)
        if replay_key in self.idempotency:
            previous_fingerprint, previous = self.idempotency[replay_key]
            if previous_fingerprint != fingerprint:
                return Response(409, {"error": {"code": "idempotency_conflict", "message": "synthetic",
                                                 "retryable": False}, "request_id": "req-synthetic"})
            payload = json.loads(previous.payload.decode("utf-8"))
            if isinstance(payload, dict) and isinstance(payload.get("meta"), dict):
                payload["meta"].update(replayed=True, refresh_required=True)
            return Response(previous.status, payload)
        for pattern, status in self.failures.items():
            if re.fullmatch(pattern, path):
                return Response(status, {"error": {"code": "version_conflict", "message": "synthetic",
                                                   "retryable": False}, "request_id": "req-synthetic"})
        route = self._route(method, path, body)
        assert route is not None, f"unrouted {method} {path}"
        if replay_key and route.status in {200, 201, 202}:
            self.idempotency[replay_key] = (fingerprint, route)
        return route

    def _route(self, method: str, path: str, body):
        if method == "POST" and path.endswith("/api/v1/lingdoc/projects"):
            return self._create_project(body)
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)", path)
        if match and method == "GET":
            return Response(200, self._envelope(self._view(match.group(1))))
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/spec", path)
        if match and method == "PUT":
            return self._save_spec(match.group(1), body)
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/members", path)
        if match and method == "PUT":
            return self._save_members(match.group(1), body)
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/activate", path)
        if match and method == "POST":
            return self._activate(match.group(1), body)
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/chapters", path)
        if match and method == "GET":
            return Response(200, self._envelope(list(self.chapters.get(match.group(1), []))))
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/chapters/([^/]+)/versions", path)
        if match and method == "POST":
            return self._save_chapter(match.group(1), match.group(2), body)
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/assets", path)
        if match and method == "GET":
            listed = [asset for asset in self.assets.get(match.group(1), [])
                      if self.asset_states.get(asset["knowledge_id"], "ready") == "ready"]
            return Response(200, self._envelope(listed))
        if match and method == "POST":
            return self._bind_asset(match.group(1), body)
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/retrieval", path)
        if match and method == "POST":
            return self._retrieve(match.group(1), body)
        match = re.fullmatch(r".*/lingdoc/projects/([^/]+)/generations", path)
        if match and method == "POST":
            return self._start_generation(match.group(1), body)
        return None

    # ---- operations -------------------------------------------------------

    @staticmethod
    def _envelope(data, replayed: bool = False):
        return {"data": data, "request_id": "req-synthetic",
                "meta": {"replayed": replayed, "refresh_required": replayed}}

    def _next_id(self, prefix: str) -> str:
        self.counter += 1
        return f"{prefix}-{self.counter}"

    def _view(self, project_id):
        view = dict(self.projects[project_id])
        if self.project_name_override is not None:
            view["name"] = self.project_name_override
        return view

    def _create_project(self, body):
        project_id = self._next_id("p")
        self.projects[project_id] = {"id": project_id, "name": body["name"], "status": "draft",
                                     "project_version": 1, "spec_revision": 0, "spec": {},
                                     "template_id": body["template_id"], "template_version": "1",
                                     "members": [{"user_id": OWNER_ID, "role": "owner"}]}
        self.chapters[project_id] = []
        self.assets[project_id] = []
        return Response(201, self._envelope(self._view(project_id)))

    def _save_spec(self, project_id, body):
        project = self.projects[project_id]
        if body["expected_spec_revision"] != project["spec_revision"]:
            return self._version_conflict()
        project["spec_revision"] += 1
        project["project_version"] += 1
        project["spec"] = dict(body["fields"])
        return Response(200, self._envelope(self._view(project_id)))

    def _save_members(self, project_id, body):
        project = self.projects[project_id]
        assert body["expected_project_version"] == project["project_version"], "project version precondition"
        project["members"] = [{"user_id": OWNER_ID, "role": "owner"}]
        project["members"].extend({"user_id": user_id, "role": "collaborator"}
                                  for user_id in sorted(body["collaborator_user_ids"]))
        project["project_version"] += 1
        return Response(200, self._envelope(self._view(project_id)))

    def _activate(self, project_id, body):
        project = self.projects[project_id]
        if body["expected_spec_revision"] != project["spec_revision"]:
            return self._version_conflict()
        project["status"] = "active"
        project["project_version"] += 1
        self.chapters[project_id] = [{"id": self._next_id("ch"), "project_id": project_id,
                                      "section_id": section, "title": title, "current_version_id": None,
                                      "body_markdown": "", "source_ids": [], "confirmation_valid": False,
                                      "review_items": []} for section, title in SECTIONS.items()]
        return Response(200, self._envelope(self._view(project_id)))

    def _save_chapter(self, project_id, chapter_id, body):
        project = self.projects[project_id]
        chapter = next(ch for ch in self.chapters[project_id] if ch["id"] == chapter_id)
        if (body["expected_chapter_version_id"] != chapter["current_version_id"]
                or body["expected_spec_revision"] != project["spec_revision"]):
            return self._version_conflict()
        project["project_version"] += 1  # the real service serialises chapter writes through the project row
        chapter["current_version_id"] = self._next_id("cv")
        chapter["body_markdown"] = (self.chapter_body_override if self.chapter_body_override is not None
                                    else body["body_markdown"])
        chapter["source_ids"] = list(body["source_ids"])
        return Response(201, self._envelope(dict(chapter)))

    def _start_generation(self, project_id, body):
        run_id = self._next_id("run")
        return Response(202, self._envelope({"id": run_id, "project_id": project_id,
                                             "chapter_id": body["chapter_id"], "status": "queued",
                                             "candidate_id": None}))

    @staticmethod
    def _version_conflict():
        return Response(409, {"error": {"code": "version_conflict", "message": "synthetic",
                                        "retryable": False}, "request_id": "req-synthetic"})

    def _bind_asset(self, project_id, body):
        asset = {"id": self._next_id("a"), "project_id": project_id, "knowledge_id": body["knowledge_id"],
                 "title": "合成测试材料（不是真实研究证据）", "asset_revision": 1,
                 "processing_state": self.asset_states.get(body["knowledge_id"], "ready")}
        self.assets[project_id].append(asset)
        return Response(201, self._envelope(dict(asset)))

    def _retrieve(self, project_id, body):
        # Mirrors the real gateway: asset_ids names bindings of this project, and an id
        # with no binding here is `not_found` however real the knowledge behind it is.
        # Reasons are keyed by knowledge so a test states them the way the declaration does.
        bound = {asset["id"]: asset for asset in self.assets.get(project_id, [])}
        denied = []
        for asset_id in body["asset_ids"]:
            asset = bound.get(asset_id)
            if asset is None:
                denied.append({"asset_id": asset_id, "reason": "not_found"})
                continue
            knowledge_id = asset["knowledge_id"]
            if self.asset_states.get(knowledge_id, "ready") != "ready":
                denied.append({"asset_id": asset_id,
                               "reason": self.deny_reasons.get(knowledge_id, "not_ready")})
        if denied:
            return Response(422, {"error": {"code": "asset_not_authorized", "message": "synthetic",
                                            "retryable": False, "details": {"denied": denied}},
                                  "request_id": "req-synthetic"})
        return Response(200, self._envelope([]))

    def paths(self) -> list[str]:
        return [urlsplit(call["path"]).path.replace("/api/v1/lingdoc", "") for call in self.calls]


def client_for(provider: SyntheticProvider) -> ProviderClient:
    # The base URL comes from the contract's server entry, the way main() resolves it.
    return ProviderClient(operation_index(OPENAPI)[0], opener=provider.open)


def bindings_for(state) -> tuple[Bindings, Bindings]:
    """Bind the names one declaration uses, the way an operator binds them for that state."""
    knowledge = Bindings("knowledge", "knowledge")
    member = Bindings("member", "user")
    for asset in state.assets:
        knowledge.bind(asset.knowledge_id, PROVIDER_IDS[asset.knowledge_id])
    for name in state.members:
        member.bind(name, MEMBER_ID)
    return knowledge, member


def loader_for(state_id: str, provider: SyntheticProvider) -> StateLoader:
    state = load_state(SCENARIOS_PATH, state_id)
    knowledge, member = bindings_for(state)
    return StateLoader(OPENAPI, state, client_for(provider), knowledge, member)


def built(state_id: str = "S2", provider: SyntheticProvider | None = None) -> tuple[SyntheticProvider, StateLoader]:
    provider = provider or SyntheticProvider()
    loader = loader_for(state_id, provider)
    loader.preflight()
    loader.build()
    return provider, loader


def run_cli(argv: list[str], provider: SyntheticProvider) -> tuple[int, str, str]:
    stdout, stderr = io.StringIO(), io.StringIO()
    with patch.object(module, "ProviderClient", return_value=client_for(provider)), \
            redirect_stdout(stdout), redirect_stderr(stderr):
        status = main(argv)
    return status, stdout.getvalue(), stderr.getvalue()


class DeclarationTest(unittest.TestCase):
    def test_repository_states_load_with_the_parts_the_builder_supports(self):
        states = {state_id: load_state(SCENARIOS_PATH, state_id) for state_id in ("S1", "S2", "S3", "S4", "S5")}
        self.assertEqual(states["S1"].status, "active")
        self.assertEqual(states["S1"].assets, ())
        self.assertEqual(states["S2"].members, ("u-member",))
        self.assertEqual([chapter.section_id for chapter in states["S2"].chapters], ["question", "method"])
        self.assertTrue(states["S2"].chapters[0].body_markdown)
        self.assertEqual(states["S2"].chapters[1].body_markdown, "")  # 这一节存在但还没有正文
        self.assertEqual(states["S3"].assets[0].knowledge_id, "k-notready")
        self.assertEqual(states["S3"].assets[0].processing_state, "pending")
        self.assertEqual(states["S4"].status, "draft")
        self.assertEqual(states["S4"].spec["research_goal"], "验证资料到章节的工作流程")
        # S3 与 S5 是同一种声明形状，只差绑定资料可不可用：读回结论必须跟着翻转。
        self.assertEqual(states["S5"].assets[0].processing_state, "ready")
        self.assertEqual(states["S5"].status, states["S3"].status)
        self.assertEqual(states["S5"].spec, states["S3"].spec)
        self.assertEqual(states["S5"].chapters, states["S3"].chapters)
        self.assertEqual(states["S5"].members, states["S3"].members)

    def test_unknown_state_is_refused_by_name(self):
        with self.assertRaisesRegex(WorkflowError, "no starting state S99"):
            load_state(SCENARIOS_PATH, "S99")

    def test_document_without_declared_states_is_refused(self):
        with self.assertRaisesRegex(WorkflowError, "declares no starting_states"):
            load_state(WORKFLOW_PATH, "S1")

    def test_unbuildable_parts_are_refused_with_the_reason_they_cannot_be_built(self):
        cases = [
            ({"candidate": {"id": "candidate-demo"}}, "declares candidate, which cannot be built: .*generation run"),
            ({"release": {"id": "snapshot-demo"}}, "declares release, which cannot be built: .*confirmed chapters"),
            ({"invented": 1}, "declares unknown part invented"),
        ]
        for extra, expected in cases:
            with self.subTest(expected=expected):
                declaration = {"id": "S9", "project": {"name": "n", "template_id": "template-demo",
                                                       "status": "draft", "spec": {"research_goal": "g"}}, **extra}
                with self.assertRaisesRegex(WorkflowError, expected):
                    state_from_declaration(declaration, "starting state S9")

    def test_broken_declarations_are_refused_with_their_own_reason(self):
        project = {"name": "n", "template_id": "template-demo", "status": "draft", "spec": {"research_goal": "g"}}
        cases = [
            ({"project": {**project, "status": "paused"}}, "status must be draft or active"),
            ({"project": {**project, "spec": {}}}, "must declare the project spec"),
            ({"chapters": [{"section_id": "question", "body_markdown": 5}]}, "body_markdown must be a string"),
            ({"chapters": [{"section_id": "", "body_markdown": "x"}]}, "section_id must be a non-empty string"),
            ({"assets": [{"knowledge_id": "k-demo", "processing_state": "unknown"}]}, "processing_state must be one of"),
            ({"assets": [{"knowledge_id": ""}]}, "knowledge_id must be a non-empty string"),
        ]
        for override, expected in cases:
            with self.subTest(expected=expected):
                declaration = {"id": "S9", "project": project, **override}
                with self.assertRaisesRegex(WorkflowError, expected):
                    state_from_declaration(declaration, "starting state S9")

    def test_chapter_body_markers_must_match_the_declared_references(self):
        declaration = {"id": "S9", "project": {"name": "n", "template_id": "template-demo", "status": "active",
                                               "spec": {"research_goal": "g"}},
                       "chapters": [{"section_id": "question", "body_markdown": "带引用 [[source:s-demo]]",
                                     "source_ids": []}]}
        with self.assertRaisesRegex(WorkflowError, r"body markers \['s-demo'\] do not match source_ids \[\]"):
            state_from_declaration(declaration, "starting state S9")


class BuildTest(unittest.TestCase):
    def test_writes_run_in_dependency_order_with_versions_taken_from_responses(self):
        provider, loader = built("S2")
        project_id, chapter_id = loader.identifiers["project_id"], loader.identifiers["chapter.question"]
        self.assertEqual(provider.paths(), ["/projects", f"/projects/{project_id}/spec",
                                            f"/projects/{project_id}/members", f"/projects/{project_id}/activate",
                                            f"/projects/{project_id}/chapters",
                                            f"/projects/{project_id}/chapters/{chapter_id}/versions"])
        self.assertEqual([call["method"] for call in provider.calls], ["POST", "PUT", "PUT", "POST", "GET", "POST"])
        bodies = [call["body"] for call in provider.calls]
        self.assertEqual(bodies[1]["expected_spec_revision"], 0)    # from the create response
        self.assertEqual(bodies[2]["expected_project_version"], 2)   # after the spec save
        self.assertEqual(bodies[2]["collaborator_user_ids"], [MEMBER_ID])
        self.assertEqual(bodies[3]["expected_spec_revision"], 1)
        self.assertEqual(bodies[5]["expected_spec_revision"], 1)
        self.assertIsNone(bodies[5]["expected_chapter_version_id"])  # the chapter has no version yet
        self.assertEqual(bodies[5]["body_markdown"], "人工编辑：这是合成测试材料，不是真实研究结果。")
        for call in provider.calls:
            if call["method"] != "GET":
                self.assertGreaterEqual(len(call["headers"]["idempotency-key"]), 8)

    def test_draft_state_stops_before_activation(self):
        provider, loader = built("S4")
        self.assertEqual(provider.paths(), ["/projects", f"/projects/{loader.identifiers['project_id']}/spec"])
        self.assertEqual(list(provider.chapters.values()), [[]])
        self.assertEqual(provider.projects["p-1"]["status"], "draft")

    def test_bound_assets_are_named_through_the_environment(self):
        provider, loader = built("S3", SyntheticProvider(asset_states={"knowledge-2": "pending"}))
        self.assertEqual(provider.calls[-1]["body"], {"knowledge_id": "knowledge-2"})
        self.assertEqual(loader.identifiers["asset.k-notready"], "knowledge-2")

    def test_a_draft_state_binds_its_assets_like_any_other(self):
        # 绑定不依赖立项：草稿声明了资料就得真绑上，读回也照常比对——而不是等读回时
        # 才因为没有绑定 id 炸掉。
        provider, loader = built("S6")
        self.assertEqual(provider.projects["p-1"]["status"], "draft")
        self.assertEqual(provider.paths(), ["/projects", "/projects/p-1/spec", "/projects/p-1/assets"])
        self.assertEqual(provider.assets["p-1"][0]["knowledge_id"], "knowledge-1")
        settled = loader.read_back()
        self.assertEqual(settled["mismatches"], [])
        self.assertEqual(settled["observed"]["assets"],
                         [{"knowledge_id": "k-demo", "listed_state": "ready", "deny_reason": ""}])

    def test_a_provider_refusal_names_the_step_and_its_code(self):
        provider = SyntheticProvider(failures={r".*/projects/[^/]+/activate": 409})
        loader = loader_for("S1", provider)
        with self.assertRaisesRegex(WorkflowError, r"S1-activate activateProject: HTTP 409 \(version_conflict\)"):
            loader.build()
        self.assertEqual([step["id"] for step in loader.build_steps], ["S1-create", "S1-spec"])

    def test_the_run_target_is_taken_from_the_client_not_read_a_second_time(self):
        # --base-url 只解析一次：客户端解析后装载器跟着走。各解析一次的话，明明是对真实服务
        # 跑的运行会静默打到契约里的 mock 地址，报告还照样说 completed。
        state = load_state(SCENARIOS_PATH, "S1")
        provider = SyntheticProvider()
        client = ProviderClient("http://127.0.0.1:9999/api/v1/lingdoc", opener=provider.open)
        knowledge, member = bindings_for(state)
        loader = StateLoader(OPENAPI, state, client, knowledge, member)
        loader.build()
        self.assertEqual(loader.base_url, "http://127.0.0.1:9999/api/v1/lingdoc")
        self.assertTrue(all(call["url"].startswith("http://127.0.0.1:9999/api/v1/lingdoc/")
                            for call in provider.calls))

    def test_a_body_that_does_not_match_the_contract_is_refused(self):
        openapi = json.loads(json.dumps(OPENAPI))
        openapi["components"]["schemas"]["ActivateProject"]["required"].append("expected_project_version")
        state = load_state(SCENARIOS_PATH, "S1")
        knowledge, member = bindings_for(state)
        loader = StateLoader(openapi, state, client_for(SyntheticProvider()), knowledge, member)
        with self.assertRaisesRegex(WorkflowError, r"S1-activate activateProject: this build sends body fields "
                                                    r"\['expected_spec_revision'\]"):
            loader.build()


class ReadBackTest(unittest.TestCase):
    def test_read_back_matches_the_declaration_and_records_a_version_free_snapshot(self):
        provider, loader = built()
        settled = loader.read_back()
        self.assertEqual(settled["mismatches"], [])
        self.assertEqual(provider.paths()[-3:], ["/projects/p-1", "/projects/p-1/chapters", "/projects/p-1/assets"])
        self.assertEqual(settled["snapshot"]["project"]["name"], "合成起点：协作者与人工正文")
        self.assertEqual(settled["snapshot"]["project"]["members"],
                         [{"role": "owner"}, {"role": "collaborator", "user_id": "u-member"}])
        self.assertEqual(settled["snapshot"]["chapters"],
                         [{"section_id": "method", "body_markdown": "", "source_ids": []},
                          {"section_id": "question", "body_markdown": "人工编辑：这是合成测试材料，不是真实研究结果。",
                           "source_ids": []}])
        self.assertEqual(settled["observed"]["project_version"], 5)
        self.assertEqual(settled["observed"]["spec_revision"], 1)
        self.assertEqual(len(settled["snapshot_digest"]), 64)

    def test_two_runs_of_one_declaration_produce_the_same_read_back(self):
        first = built()[1].read_back()
        second = built()[1].read_back()
        self.assertEqual(first["snapshot"], second["snapshot"])
        self.assertEqual(first["snapshot_digest"], second["snapshot_digest"])
        # 「读回结果相同」说的是提供方答回来的事实，而 snapshot 只是声明本身：真正承载
        # 这条验收的是 observed（及其摘要）与两次都为空的不一致清单。
        self.assertEqual(first["observed"], second["observed"])
        self.assertEqual(first["observed_digest"], second["observed_digest"])
        self.assertEqual(first["mismatches"], second["mismatches"])
        self.assertNotEqual(first["snapshot_digest"], first["observed_digest"])

    def test_an_unreadable_sub_state_is_recorded_instead_of_asserted(self):
        # 声明写的是 pending，通道只能证到「不可用」：这一格必须当成「未验证」报出来，
        # 否则声明里写 failed 也一样能过，读回就成了自说自话。
        settled = built("S3", SyntheticProvider(asset_states={"knowledge-2": "pending"}))[1].read_back()
        self.assertEqual(settled["unverified"],
                         [{"pointer": "/assets/k-notready", "declared": "处理状态 pending",
                           "verified": "不可用（not_ready）",
                           "why": "提供方只答「不可用」；pending / processing / failed / replaced "
                                  "之间没有任何通道能区分"}])
        self.assertEqual(settled["mismatches"], [])

    def test_a_usable_asset_is_fully_verified_and_leaves_no_gap(self):
        settled = built("S5")[1].read_back()
        self.assertEqual(settled["unverified"], [])

    def test_a_difference_is_reported_with_what_was_declared_and_what_came_back(self):
        provider = SyntheticProvider(project_name="被改名的项目", chapter_body="别的正文")
        settled = built(provider=provider)[1].read_back()
        self.assertEqual(settled["mismatches"], [
            {"pointer": "/project/name", "declared": "合成起点：协作者与人工正文", "actual": "被改名的项目"},
            {"pointer": "/chapters/question/body_markdown", "declared": "人工编辑：这是合成测试材料，不是真实研究结果。",
             "actual": "别的正文"},
        ])

    def test_unusable_asset_is_verified_through_the_retrieval_preflight(self):
        provider, loader = built("S3", SyntheticProvider(asset_states={"knowledge-2": "pending"}))
        settled = loader.read_back()
        self.assertEqual(settled["mismatches"], [])
        self.assertEqual(loader.readback_steps[-1]["operation_id"], "retrieveSources")
        self.assertEqual(settled["observed"]["assets"],
                         [{"knowledge_id": "k-notready", "listed_state": None, "deny_reason": "not_ready"}])

    def test_usable_asset_is_confirmed_through_the_asset_list_without_a_probe(self):
        provider, loader = built("S5")
        settled = loader.read_back()
        self.assertEqual(settled["mismatches"], [])
        self.assertEqual(settled["observed"]["assets"],
                         [{"knowledge_id": "k-demo", "listed_state": "ready", "deny_reason": ""}])
        # 可用性由 listAssets 自己回答，读回不需要再发检索预检（不可用那条才需要）。
        self.assertNotIn("retrieveSources", [step["operation_id"] for step in loader.readback_steps])
        self.assertEqual([step["operation_id"] for step in loader.readback_steps][-3:],
                         ["getProject", "listChapters", "listAssets"])

    def test_asset_denied_for_authorization_is_not_accepted_as_not_ready(self):
        provider = SyntheticProvider(asset_states={"knowledge-2": "pending"},
                                     deny_reasons={"knowledge-2": "not_authorized"})
        settled = built("S3", provider)[1].read_back()
        self.assertEqual(settled["mismatches"], [{"pointer": "/assets/k-notready", "declared": "已绑定但不可用",
                                                  "actual": "调用者无权读取（not_authorized）"}])

    def test_asset_the_provider_lists_as_usable_contradicts_a_not_ready_declaration(self):
        settled = built("S3", SyntheticProvider(asset_states={"knowledge-2": "ready"}))[1].read_back()
        self.assertEqual(settled["mismatches"], [{"pointer": "/assets/k-notready", "declared": "不可用（pending）",
                                                  "actual": "listAssets 把它列为可用资料"}])

    def test_a_usable_declaration_the_provider_buries_reports_the_denial_reason(self):
        settled = built("S5", SyntheticProvider(asset_states={"knowledge-1": "pending"}))[1].read_back()
        self.assertEqual(settled["mismatches"],
                         [{"pointer": "/assets/k-demo", "declared": "已绑定且可用",
                           "actual": "listAssets 未列出：资料未就绪（not_ready）"}])

    def test_template_sections_missing_from_the_read_back_are_reported(self):
        provider, loader = built("S1")
        provider.chapters["p-1"] = []
        settled = loader.read_back()
        self.assertEqual(settled["mismatches"],
                         [{"pointer": "/chapters/method", "declared": "由模板生成", "actual": "读回时不存在"},
                          {"pointer": "/chapters/question", "declared": "由模板生成", "actual": "读回时不存在"}])

    def test_a_member_absent_from_the_read_back_is_reported(self):
        provider, loader = built()
        provider.projects["p-1"]["members"] = [{"user_id": OWNER_ID, "role": "owner"}]
        settled = loader.read_back()
        self.assertEqual(settled["mismatches"],
                         [{"pointer": "/project/members/roles", "declared": ["collaborator", "owner"],
                           "actual": ["owner"]},
                          {"pointer": "/project/members/u-member", "declared": "协作者", "actual": "读回时不在成员里"}])

    def test_an_undeclared_usable_asset_is_reported(self):
        provider, loader = built("S1")
        provider.assets["p-1"].append({"id": "a-9", "project_id": "p-1", "knowledge_id": "knowledge-9",
                                       "title": "t", "asset_revision": 1, "processing_state": "ready"})
        provider.calls.clear()
        settled = loader.read_back()
        self.assertEqual(settled["mismatches"], [{"pointer": "/assets", "declared": "没有其他资料",
                                                  "actual": "多出一条可用资料 knowledge-9"}])
        self.assertEqual(provider.paths(), ["/projects/p-1", "/projects/p-1/chapters", "/projects/p-1/assets"])


class CommandLineTest(unittest.TestCase):
    def test_environment_names_are_bound_before_the_first_write(self):
        provider = SyntheticProvider()
        status, _, stderr = run_cli(["--state", "S2"], provider)
        self.assertEqual(status, 1)
        self.assertEqual(provider.calls, [])
        self.assertIn("the environment supplies no user for u-member", stderr)
        self.assertIn("bind the declared name with --member u-member=<id>", stderr)

    def test_binding_syntax_is_checked(self):
        status, _, stderr = run_cli(["--state", "S1", "--knowledge", "k-demo"], SyntheticProvider())
        self.assertEqual(status, 1)
        self.assertIn("--knowledge takes name=id", stderr)

    def test_a_supplied_name_the_state_never_uses_is_refused(self):
        # 多给的绑定和拼错的绑定长得一样，静默忽略就是把「这票最恨的半截状态」放进来。
        provider = SyntheticProvider()
        status, _, stderr = run_cli(["--state", "S1", "--knowledge", "k-dmeo=knowledge-1"], provider)
        self.assertEqual(status, 1)
        self.assertEqual(provider.calls, [])
        self.assertIn("--knowledge supplies k-dmeo, which knowledge names this state never declares", stderr)

    def test_completed_run_writes_a_sanitized_report_and_prints_identifiers(self):
        with tempfile.TemporaryDirectory() as directory:
            report_path = Path(directory) / "artifacts" / "S1-run.json"
            status, stdout, _ = run_cli(["--state", "S1", "--report", str(report_path)], SyntheticProvider())
            self.assertEqual(status, 0)
            report_text = report_path.read_text(encoding="utf-8")
            report = json.loads(report_text)
            self.assertFalse(list(report_path.parent.glob(".f01-*.tmp")))
        self.assertEqual(report["runner_status"], "completed")
        self.assertEqual(report["state"], "S1")
        self.assertEqual(report["mismatches"], [])
        self.assertEqual(report["verification_scope"], "declared_state_read_back_only")
        self.assertEqual(report["provider_semantics_status"], "not_verified")
        # 报告要能自己说明是对谁跑的：契约里的 mock 地址与真实服务地址不是一回事。
        self.assertEqual(report["provider_host"], "localhost:4010")
        self.assertEqual([entry["part"] for entry in report["cannot_build"]],
                         ["template", "source", "candidate", "confirmation", "release", "export"])
        self.assertTrue(all(entry["reason"] for entry in report["cannot_build"]))
        for private in ("p-1", "ch-", "a-1", "req-synthetic", "knowledge-1", "user-owner"):
            self.assertNotIn(private, report_text)
        identifiers = json.loads(stdout.splitlines()[-1])
        self.assertEqual(identifiers["identifiers"]["project_id"], "p-1")
        self.assertEqual(identifiers["snapshot_digest"], report["snapshot_digest"])

    def test_report_destination_is_verified_before_any_provider_write(self):
        with tempfile.TemporaryDirectory() as directory:
            occupied = Path(directory) / "not-a-directory"
            occupied.write_text("occupied", encoding="utf-8")
            provider = SyntheticProvider()
            status, _, stderr = run_cli(["--state", "S1", "--report", str(occupied / "run.json")], provider)
        self.assertEqual(status, 1)
        self.assertEqual(provider.calls, [])
        self.assertIn("cannot write workflow report", stderr)
        self.assertNotIn("Traceback", stderr)

    def test_a_mismatch_fails_the_run_and_replaces_an_old_success(self):
        with tempfile.TemporaryDirectory() as directory:
            report_path = Path(directory) / "S1-run.json"
            report_path.write_text('{"runner_status":"completed"}', encoding="utf-8")
            status, stdout, stderr = run_cli(["--state", "S1", "--report", str(report_path)],
                                             SyntheticProvider(project_name="被改名的项目"))
            report = json.loads(report_path.read_text(encoding="utf-8"))
        self.assertEqual(status, 1)
        self.assertEqual(report["runner_status"], "failed")
        self.assertEqual(report["mismatches"][0]["pointer"], "/project/name")
        self.assertIn("MISMATCH /project/name", stderr)
        self.assertIn("S1 FAILED: the built state does not match the declaration", stderr)
        self.assertEqual(stdout, "")
        # The mismatch names the value that came back; the snapshot keeps the declaration,
        # so a failed run never leaves behind a record of a state it did not build.
        self.assertEqual(report["snapshot"]["project"]["name"], "合成起点：空资料项目")

    def test_failure_after_a_write_records_the_step_that_died(self):
        with tempfile.TemporaryDirectory() as directory:
            report_path = Path(directory) / "S1-run.json"
            status, _, stderr = run_cli(["--state", "S1", "--report", str(report_path)],
                                        SyntheticProvider(failures={r".*/projects/[^/]+/activate": 409}))
            report = json.loads(report_path.read_text(encoding="utf-8"))
        self.assertEqual(status, 1)
        self.assertEqual(report["failed_step"], {"id": "S1-activate", "operation_id": "activateProject"})
        self.assertEqual(report["build_steps"], [{"id": "S1-create", "operation_id": "createProject",
                                                  "http_status": 201, "content_type": "application/json"},
                                                 {"id": "S1-spec", "operation_id": "saveSpec",
                                                  "http_status": 200, "content_type": "application/json"}])
        self.assertIn("S1 FAILED: S1-activate activateProject: HTTP 409", stderr)


class RunnerSeamTest(unittest.TestCase):
    def test_loader_drives_the_runners_own_request_path(self):
        provider, loader = built("S1")
        loader.read_back()
        self.assertIsInstance(loader.client, ProviderClient)
        self.assertTrue(provider.calls)
        self.assertEqual({call["headers"]["accept"] for call in provider.calls},
                         {"application/json, application/octet-stream"})


if __name__ == "__main__":
    unittest.main()
