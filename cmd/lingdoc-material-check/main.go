// Command lingdoc-material-check is the material qualification gate.
//
// It answers one question before a material enters an acceptance run: is
// every chunk of that material the verbatim slice of the origin text at its
// own coordinates?
//
// The question is not academic. The source re-check compares the text the
// origin yields at [start_at, end_at) with the quoted text of the source; a
// chunk that carries anything the document does not have at that position is
// judged "the quoted text no longer comes from the original", every source
// of that material goes stale, and every operation that re-checks them is
// refused. That failure looks like a licence-chain problem and is not one
// (#485 / D1).
//
// The gate reads only: the server's read-only endpoints and the origin file.
// It never calls a provider.
//
// Exit codes:
//
//	0 — the material is qualified
//	1 — the material is not qualified (the report says what is missing)
//	2 — the gate could not run (missing dependency, unreachable server, ...)
//	    — it fails closed: an unrunnable gate must not be read as a pass
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/evidence"
)

type chunkPayload struct {
	ID              string `json:"id"`
	ChunkIndex      int    `json:"chunk_index"`
	StartAt         int    `json:"start_at"`
	EndAt           int    `json:"end_at"`
	Content         string `json:"content"`
	IndexStatus     string `json:"index_status"`
	ContentRevision int    `json:"content_revision"`
}

type sourcePayload struct {
	ID             string `json:"id"`
	QuotedText     string `json:"quoted_text"`
	QuotedTextHash string `json:"quoted_text_hash"`
	Status         string `json:"status"`
	Locator        string `json:"locator"`
	AssetID        string `json:"asset_id"`
	AssetRevision  int    `json:"asset_revision"`
}

type envelope struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Total   int             `json:"total"`
	Message string          `json:"message"`
	Error   string          `json:"error"`
}

type report struct {
	KnowledgeID string `json:"knowledge_id"`
	ProjectID   string `json:"project_id"`
	OriginFile  string `json:"origin_file"`
	OriginRunes int    `json:"origin_runes"`
	ChunkCount  int    `json:"chunk_count"`
	// Stage is the half of the gate this report judges: "content" (the
	// chunk content against the origin slice) or "full" (both halves).
	//
	// The gate has two halves and they cannot run at the same time: the
	// quoted-text half reads the sources of a **bound** material, so it needs
	// the project the material is bound to — and in an acceptance run the
	// project does not exist yet before the first node. A stage that says
	// which half it judged is what keeps "half-checked" from being read as
	// "checked".
	Stage        string                         `json:"stage"`
	Qualified    bool                           `json:"qualified"`
	ContentCheck evidence.MaterialQualification `json:"content_check"`
	// QuotedCheck is the end-to-end check: the quoted text the server hands
	// out for each source must be the origin slice at the source's own
	// coordinates. It needs the project the material is bound to, so in the
	// "full" stage it is **required**: without the project the stage cannot
	// run, and a stage that could not run is not a pass.
	QuotedCheck        evidence.MaterialQualification `json:"quoted_check,omitempty"`
	QuotedCheckSkipped bool                           `json:"quoted_check_skipped"`
	SourceStatus       map[string]string              `json:"source_status"`
	Notes              []string                       `json:"notes,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the whole gate, and it ANSWERS with an exit code instead of
// leaving the process.
//
// The exit code is the gate's contract with its caller (the F01 runner reads
// nothing else), so it has to be testable without starting a process: every
// path returns a code, and the tests drive the same paths.
func run(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("lingdoc-material-check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	var (
		baseURL     = flags.String("base-url", "http://127.0.0.1:18085", "server base URL")
		apiPath     = flags.String("api-path", "/api/v1", "API path prefix")
		lingdocPath = flags.String("lingdoc-path", "/api/v1/lingdoc", "LingDoc API path prefix")
		tokenFile   = flags.String("token-file", "", "file holding the bearer token")
		token       = flags.String("token", "", "bearer token (alternative to --token-file). "+
			"WARNING: a token on the command line is visible in the process list to every "+
			"other account on the machine — prefer --token-file")
		knowledgeID = flags.String("knowledge-id", "", "knowledge id of the material")
		projectID   = flags.String("project-id", "", "project the material is bound to")
		originFile  = flags.String("origin-file", "", "the ORIGINAL uploaded file of the material; "+
			"its bytes are what the ingest chain split, so any copy with rewritten line endings "+
			"or a rewritten image reference is refused")
		stage = flags.String("stage", "full", "which half of the gate to judge: "+
			"\"content\" (the chunk content against the origin slice; runs before the project exists) "+
			"or \"full\" (both halves; needs --project-id and refuses to run without it)")
		jsonOut = flags.String("json-out", "", "optional path for the JSON report")
		timeout = flags.Int("timeout", 30, "HTTP timeout in seconds")
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}

	if *knowledgeID == "" || *originFile == "" {
		_, _ = fmt.Fprintln(stderr, "usage: lingdoc-material-check --knowledge-id ID --origin-file FILE "+
			"[--stage content|full] [--project-id ID] [--token-file FILE]")
		return 2
	}
	if *stage != "content" && *stage != "full" {
		_, _ = fmt.Fprintf(stderr, "material check cannot run: unknown stage %q (want \"content\" or \"full\")\n", *stage)
		return 2
	}
	bearer, err := readToken(*tokenFile, *token)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "material check cannot run: %v\n", err)
		return 2
	}
	originBytes, err := os.ReadFile(*originFile)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "material check cannot run: cannot read the origin file: %v\n", err)
		return 2
	}
	// The origin text must be the bytes the ingest chain actually split.
	// The product side judges that with evidence.VerbatimOrigin (the same
	// judge the source re-check uses); a file that fails it is not the
	// original — rewritten line endings, a rewritten image reference or a
	// truncated copy would make every coordinate compare against the wrong
	// bytes, and the verdict would be a false pass.
	origin, verbatim := evidence.VerbatimOrigin(originBytes)
	if !verbatim {
		_, _ = fmt.Fprintf(stderr,
			"material check cannot run: the origin file %s is not the text the ingest chain split "+
				"(it contains rewritten line endings, a rewritten image reference, a NUL byte or invalid UTF-8); "+
				"pass the ORIGINAL uploaded file\n", *originFile)
		return 2
	}

	client := &http.Client{Timeout: time.Duration(*timeout) * time.Second}
	chunks, err := listChunks(client, *baseURL+*apiPath, bearer, *knowledgeID)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "material check cannot run: cannot list the chunks: %v\n", err)
		return 2
	}
	if len(chunks) == 0 {
		_, _ = fmt.Fprintf(stderr, "material check cannot run: the material %s has no chunks\n", *knowledgeID)
		return 2
	}

	result := report{
		KnowledgeID:  *knowledgeID,
		ProjectID:    *projectID,
		OriginFile:   *originFile,
		OriginRunes:  utf8.RuneCountInString(origin),
		ChunkCount:   len(chunks),
		Stage:        *stage,
		SourceStatus: map[string]string{},
	}

	// The "full" stage judges both halves, so it needs the project the
	// material is bound to. Without it the stage cannot run, and a stage
	// that could not run is not a pass: refuse before doing anything.
	if *stage == "full" && *projectID == "" {
		_, _ = fmt.Fprintln(stderr,
			"material check cannot run: the full stage needs --project-id "+
				"(the quoted-text half reads the sources of a bound material)")
		return 2
	}

	contentChunks := make([]evidence.MaterialChunk, 0, len(chunks))
	quotedChunks := make([]evidence.MaterialChunk, 0, len(chunks))
	for _, chunk := range chunks {
		contentChunks = append(contentChunks, evidence.MaterialChunk{
			ID:      chunk.ID,
			StartAt: chunk.StartAt,
			EndAt:   chunk.EndAt,
			Content: chunk.Content,
		})
		quoted := ""
		status := "not_checked"
		if *stage == "full" {
			source, err := getSource(client, *baseURL+*lingdocPath, bearer, *projectID, chunk.ID)
			switch {
			case err != nil:
				status = "unreadable: " + err.Error()
				result.Notes = append(result.Notes,
					fmt.Sprintf("chunk %s: the source could not be read: %v", chunk.ID, err))
			default:
				quoted = source.QuotedText
				status = source.Status
			}
		}
		result.SourceStatus[chunk.ID] = status
		quotedChunks = append(quotedChunks, evidence.MaterialChunk{
			ID:      chunk.ID,
			StartAt: chunk.StartAt,
			EndAt:   chunk.EndAt,
			Content: quoted,
		})
	}

	result.ContentCheck = evidence.QualifyMaterial(origin, contentChunks)
	result.Qualified = result.ContentCheck.Qualified
	if *stage == "full" {
		result.QuotedCheck = evidence.QualifyMaterial(origin, quotedChunks)
		result.QuotedCheckSkipped = false
		result.Qualified = result.Qualified && result.QuotedCheck.Qualified
	} else {
		// The content stage judges the content half only, and says so. The
		// quoted-text half needs a bound material, so it cannot run here.
		result.QuotedCheckSkipped = true
		result.Notes = append(result.Notes,
			"本阶段只核对分块内容与原文切片；来源引文那一半由「full」阶段在材料绑定之后核对")
	}

	printReport(stdout, result)

	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, result); err != nil {
			_, _ = fmt.Fprintf(stderr, "material check cannot write the report: %v\n", err)
			return 2
		}
	}
	if !result.Qualified {
		return 1
	}
	if result.QuotedCheckSkipped && *stage == "full" {
		// The quoted-text half did not run. A gate that did not run is not a
		// pass: that half is the one #485 actually broke (the source was
		// judged stale), so a half-checked gate that reports success is
		// exactly the failure this gate exists to prevent. Fail closed.
		//
		// (In the "content" stage the half is out of scope on purpose, and
		// the report says which half it judged.)
		_, _ = fmt.Fprintln(stderr,
			"material check did not run completely: the quoted-text half was skipped "+
				"(no --project-id given). Pass --project-id so the sources are checked too.")
		return 2
	}
	return 0
}

func printReport(stdout io.Writer, result report) {
	_, _ = fmt.Fprintf(stdout, "材料合格性前置比对（阶段=%s）\n", result.Stage)
	_, _ = fmt.Fprintf(stdout, "  知识=%s 项目=%s 原文=%s（%d 字符）分块=%d\n",
		result.KnowledgeID, result.ProjectID, result.OriginFile, result.OriginRunes, result.ChunkCount)
	_, _ = fmt.Fprintf(stdout, "  分块内容 vs 原文切片：\n%s", indent(result.ContentCheck.QualificationSummary()))
	if result.QuotedCheckSkipped {
		_, _ = fmt.Fprintln(stdout, "  来源引文 vs 原文切片：（本阶段不核对——它需要材料已绑定到项目）")
	} else {
		_, _ = fmt.Fprintf(stdout, "  来源引文 vs 原文切片：\n%s", indent(result.QuotedCheck.QualificationSummary()))
	}
	for id, status := range result.SourceStatus {
		_, _ = fmt.Fprintf(stdout, "  来源状态 chunk=%s status=%s\n", id, status)
	}
	for _, note := range result.Notes {
		_, _ = fmt.Fprintf(stdout, "  ! %s\n", note)
	}
	if result.Qualified {
		_, _ = fmt.Fprintln(stdout, "  结论：合格")
		return
	}
	_, _ = fmt.Fprintln(stdout, "  结论：不合格 —— 这份材料不能进入验收：上面的分块不是原文在它自己坐标上的切片，来源复核会判 stale。")
}

func indent(text string) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	for i, line := range lines {
		lines[i] = "    " + line
	}
	return strings.Join(lines, "\n") + "\n"
}

func readToken(tokenFile, token string) (string, error) {
	if token != "" {
		return strings.TrimSpace(token), nil
	}
	if tokenFile == "" {
		return "", fmt.Errorf("no bearer token: pass --token-file or --token")
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("cannot read the token file: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", fmt.Errorf("the token file %s is empty", tokenFile)
	}
	return value, nil
}

func listChunks(client *http.Client, base, bearer, knowledgeID string) ([]chunkPayload, error) {
	chunks := make([]chunkPayload, 0)
	page := 1
	for {
		url := fmt.Sprintf("%s/chunks/%s?page=%d&page_size=100", base, knowledgeID, page)
		body, err := getJSON(client, url, bearer)
		if err != nil {
			return nil, err
		}
		var env envelope
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("cannot parse the chunk list: %w", err)
		}
		var pageChunks []chunkPayload
		if len(env.Data) > 0 {
			if err := json.Unmarshal(env.Data, &pageChunks); err != nil {
				return nil, fmt.Errorf("cannot parse the chunk list: %w", err)
			}
		}
		chunks = append(chunks, pageChunks...)
		if len(pageChunks) == 0 || len(chunks) >= env.Total {
			return chunks, nil
		}
		page++
	}
}

func getSource(client *http.Client, base, bearer, projectID, chunkID string) (sourcePayload, error) {
	url := fmt.Sprintf("%s/projects/%s/sources/%s", base, projectID, chunkID)
	body, err := getJSON(client, url, bearer)
	if err != nil {
		return sourcePayload{}, err
	}
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return sourcePayload{}, fmt.Errorf("cannot parse the source: %w", err)
	}
	var source sourcePayload
	if len(env.Data) > 0 {
		if err := json.Unmarshal(env.Data, &source); err != nil {
			return sourcePayload{}, fmt.Errorf("cannot parse the source: %w", err)
		}
	}
	return source, nil
}

func getJSON(client *http.Client, url, bearer string) ([]byte, error) {
	request, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+bearer)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s -> %d: %s", url, response.StatusCode, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func writeJSON(path string, value any) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}
