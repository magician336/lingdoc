package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// The gate's contract with its caller is its EXIT CODE: the F01 runner reads
// nothing else. So the exit codes are the thing these cases pin.
//
//	0 — the material is qualified (the stage that was asked for ran and passed)
//	1 — the material is not qualified (the report says what is missing)
//	2 — the gate could not run (a gate that did not run is not a pass)
//
// The cases that matter most are the fail-closed ones: a half-checked gate
// must never answer 0, because "half-checked" is exactly what #485's broken
// material looked like.
// ---------------------------------------------------------------------------

// fakeServer is a server that answers the two read-only endpoints the gate
// uses: the chunk list and the source of one chunk.
type fakeServer struct {
	chunks  []chunkPayload
	sources map[string]sourcePayload
	// missingSource makes the source endpoint answer 404, which is what a
	// material that is NOT bound to the project looks like.
	missingSource bool
}

func (f *fakeServer) handler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/chunks/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope{
			Success: true,
			Total:   len(f.chunks),
			Data:    mustJSON(t, f.chunks),
		})
	})
	mux.HandleFunc("/api/v1/lingdoc/projects/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if f.missingSource {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(envelope{Success: false, Error: "not found"})
			return
		}
		chunkID := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		source, ok := f.sources[chunkID]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(envelope{Success: false, Error: "not found"})
			return
		}
		_ = json.NewEncoder(w).Encode(envelope{Success: true, Data: mustJSON(t, source)})
	})
	return mux
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// qualifiedFixture is a material whose two chunks are the verbatim slices of
// a 12-character origin, overlapping over [4,8).
func qualifiedFixture(t *testing.T) (origin string, chunks []chunkPayload, sources map[string]sourcePayload) {
	t.Helper()
	origin = "0123456789ab"
	chunks = []chunkPayload{
		{ID: "c1", StartAt: 0, EndAt: 8, Content: "01234567"},
		{ID: "c2", StartAt: 4, EndAt: 12, Content: "456789ab"},
	}
	sources = map[string]sourcePayload{}
	for _, chunk := range chunks {
		sources[chunk.ID] = sourcePayload{
			ID:         chunk.ID,
			QuotedText: chunk.Content,
			Status:     "available",
		}
	}
	return origin, chunks, sources
}

func writeOrigin(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "origin.md")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write origin: %v", err)
	}
	return path
}

func writeToken(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token.txt")
	if err := os.WriteFile(path, []byte("token-value"), 0o600); err != nil {
		t.Fatalf("write token: %v", err)
	}
	return path
}

func runGate(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestTheFullStagePassesWhenBothHalvesAreQualified(t *testing.T) {
	origin, chunks, sources := qualifiedFixture(t)
	server := httptest.NewServer((&fakeServer{chunks: chunks, sources: sources}).handler(t))
	defer server.Close()

	code, stdout, stderr := runGate(t, []string{
		"--base-url", server.URL,
		"--knowledge-id", "k-1",
		"--project-id", "p-1",
		"--origin-file", writeOrigin(t, origin),
		"--token-file", writeToken(t),
		"--stage", "full",
	})
	if code != 0 {
		t.Fatalf("a qualified material must pass: code=%d stderr=%s stdout=%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "阶段=full") {
		t.Fatalf("the report must name the stage it judged: %s", stdout)
	}
	if !strings.Contains(stdout, "逐位一致=true") {
		t.Fatalf("the report must state the overlap verdict: %s", stdout)
	}
}

func TestTheFullStageRefusesToRunWithoutTheProject(t *testing.T) {
	// The quoted-text half reads the sources of a BOUND material. Without
	// the project the stage cannot run, and a stage that could not run is
	// not a pass.
	origin, chunks, _ := qualifiedFixture(t)
	server := httptest.NewServer((&fakeServer{chunks: chunks}).handler(t))
	defer server.Close()

	code, _, stderr := runGate(t, []string{
		"--base-url", server.URL,
		"--knowledge-id", "k-1",
		"--origin-file", writeOrigin(t, origin),
		"--token-file", writeToken(t),
		"--stage", "full",
	})
	if code != 2 {
		t.Fatalf("the full stage without a project must fail closed, got %d", code)
	}
	if !strings.Contains(stderr, "--project-id") {
		t.Fatalf("the refusal must say what is missing: %s", stderr)
	}
}

func TestTheContentStageRunsWithoutTheProjectAndSaysWhichHalfItJudged(t *testing.T) {
	origin, chunks, _ := qualifiedFixture(t)
	server := httptest.NewServer((&fakeServer{chunks: chunks}).handler(t))
	defer server.Close()

	code, stdout, stderr := runGate(t, []string{
		"--base-url", server.URL,
		"--knowledge-id", "k-1",
		"--origin-file", writeOrigin(t, origin),
		"--token-file", writeToken(t),
		"--stage", "content",
	})
	if code != 0 {
		t.Fatalf("the content stage must pass a qualified material: code=%d stderr=%s", code, stderr)
	}
	if !strings.Contains(stdout, "阶段=content") {
		t.Fatalf("the report must name the stage: %s", stdout)
	}
	if !strings.Contains(stdout, "本阶段不核对") {
		t.Fatalf("the report must say the quoted half was not judged: %s", stdout)
	}
}

func TestAnUnqualifiedMaterialIsRefusedWithAReason(t *testing.T) {
	// The origin file says something else than the chunk does.
	_, chunks, sources := qualifiedFixture(t)
	server := httptest.NewServer((&fakeServer{chunks: chunks, sources: sources}).handler(t))
	defer server.Close()

	code, stdout, stderr := runGate(t, []string{
		"--base-url", server.URL,
		"--knowledge-id", "k-1",
		"--project-id", "p-1",
		"--origin-file", writeOrigin(t, "XXXXXXXXXXXX"),
		"--token-file", writeToken(t),
		"--stage", "full",
	})
	if code != 1 {
		t.Fatalf("an unqualified material must be refused with 1, got %d (stderr=%s)", code, stderr)
	}
	if !strings.Contains(stdout, "content_not_verbatim") {
		t.Fatalf("the refusal must carry the machine-readable reason: %s", stdout)
	}
}

func TestAnOriginFileThatIsNotTheOriginalIsRefusedBeforeAnythingIsJudged(t *testing.T) {
	// The product side refuses an origin text with rewritten line endings,
	// a rewritten image reference, a NUL byte or invalid UTF-8. The gate
	// uses the SAME judge, so a copy that is not the original cannot be
	// judged against the chunks: it would be compared against the wrong
	// bytes and could report a false pass.
	_, chunks, sources := qualifiedFixture(t)
	server := httptest.NewServer((&fakeServer{chunks: chunks, sources: sources}).handler(t))
	defer server.Close()

	for name, content := range map[string]string{
		"rewritten line endings": "01234567\r\n89ab",
		"a rewritten image":      "01234567 ![x](resource://y) 89ab",
		"a NUL byte":             "01234567\x0089ab",
	} {
		t.Run(name, func(t *testing.T) {
			code, _, stderr := runGate(t, []string{
				"--base-url", server.URL,
				"--knowledge-id", "k-1",
				"--project-id", "p-1",
				"--origin-file", writeOrigin(t, content),
				"--token-file", writeToken(t),
				"--stage", "full",
			})
			if code != 2 {
				t.Fatalf("an origin file that is not the original must fail closed, got %d", code)
			}
			if !strings.Contains(stderr, "ORIGINAL") {
				t.Fatalf("the refusal must say the file is not the original: %s", stderr)
			}
		})
	}
}

func TestAnUnknownStageIsRefused(t *testing.T) {
	origin, chunks, _ := qualifiedFixture(t)
	server := httptest.NewServer((&fakeServer{chunks: chunks}).handler(t))
	defer server.Close()

	code, _, stderr := runGate(t, []string{
		"--base-url", server.URL,
		"--knowledge-id", "k-1",
		"--origin-file", writeOrigin(t, origin),
		"--token-file", writeToken(t),
		"--stage", "half",
	})
	if code != 2 {
		t.Fatalf("an unknown stage must fail closed, got %d", code)
	}
	if !strings.Contains(stderr, "unknown stage") {
		t.Fatalf("the refusal must name the unknown stage: %s", stderr)
	}
}

func TestTheGateRefusesWhenTheServerCannotBeReached(t *testing.T) {
	origin, _, _ := qualifiedFixture(t)
	code, _, stderr := runGate(t, []string{
		"--base-url", "http://127.0.0.1:1",
		"--knowledge-id", "k-1",
		"--project-id", "p-1",
		"--origin-file", writeOrigin(t, origin),
		"--token-file", writeToken(t),
		"--stage", "full",
	})
	if code != 2 {
		t.Fatalf("an unreachable server must fail closed, got %d", code)
	}
	if !strings.Contains(stderr, "cannot run") {
		t.Fatalf("the refusal must say the gate could not run: %s", stderr)
	}
}

func TestTheJSONReportCarriesTheStageAndTheOverlapVerdict(t *testing.T) {
	origin, chunks, sources := qualifiedFixture(t)
	server := httptest.NewServer((&fakeServer{chunks: chunks, sources: sources}).handler(t))
	defer server.Close()

	out := filepath.Join(t.TempDir(), "report.json")
	code, _, stderr := runGate(t, []string{
		"--base-url", server.URL,
		"--knowledge-id", "k-1",
		"--project-id", "p-1",
		"--origin-file", writeOrigin(t, origin),
		"--token-file", writeToken(t),
		"--stage", "full",
		"--json-out", out,
	})
	if code != 0 {
		t.Fatalf("a qualified material must pass: code=%d stderr=%s", code, stderr)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var decoded struct {
		Stage        string `json:"stage"`
		Qualified    bool   `json:"qualified"`
		ContentCheck struct {
			Overlaps []struct {
				FirstChunkID string `json:"FirstChunkID"`
				Span         int    `json:"Span"`
				Identical    bool   `json:"Identical"`
			} `json:"Overlaps"`
		} `json:"content_check"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decode report: %v", err)
	}
	if decoded.Stage != "full" || !decoded.Qualified {
		t.Fatalf("the report must record the stage and the verdict: %+v", decoded)
	}
	if len(decoded.ContentCheck.Overlaps) != 1 {
		t.Fatalf("the report must carry the overlap verdict: %+v", decoded.ContentCheck.Overlaps)
	}
	if !decoded.ContentCheck.Overlaps[0].Identical || decoded.ContentCheck.Overlaps[0].Span != 4 {
		t.Fatalf("the overlap must be reported as identical over 4 runes: %+v", decoded.ContentCheck.Overlaps[0])
	}
}
