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
	KnowledgeID  string                         `json:"knowledge_id"`
	ProjectID    string                         `json:"project_id"`
	OriginFile   string                         `json:"origin_file"`
	OriginRunes  int                            `json:"origin_runes"`
	ChunkCount   int                            `json:"chunk_count"`
	Qualified    bool                           `json:"qualified"`
	ContentCheck evidence.MaterialQualification `json:"content_check"`
	// QuotedCheck is the end-to-end check: the quoted text the server hands
	// out for each source must be the origin slice at the source's own
	// coordinates. It needs the project the material is bound to, so it is
	// skipped (not failed) when no project is given.
	QuotedCheck        evidence.MaterialQualification `json:"quoted_check,omitempty"`
	QuotedCheckSkipped bool                           `json:"quoted_check_skipped"`
	SourceStatus       map[string]string              `json:"source_status"`
	Notes              []string                       `json:"notes,omitempty"`
}

func main() {
	var (
		baseURL     = flag.String("base-url", "http://127.0.0.1:18085", "server base URL")
		apiPath     = flag.String("api-path", "/api/v1", "API path prefix")
		lingdocPath = flag.String("lingdoc-path", "/api/v1/lingdoc", "LingDoc API path prefix")
		tokenFile   = flag.String("token-file", "", "file holding the bearer token")
		token       = flag.String("token", "", "bearer token (alternative to --token-file)")
		knowledgeID = flag.String("knowledge-id", "", "knowledge id of the material")
		projectID   = flag.String("project-id", "", "project the material is bound to")
		originFile  = flag.String("origin-file", "", "file holding the material's origin text")
		jsonOut     = flag.String("json-out", "", "optional path for the JSON report")
		timeout     = flag.Int("timeout", 30, "HTTP timeout in seconds")
	)
	flag.Parse()

	if *knowledgeID == "" || *originFile == "" {
		fmt.Fprintln(os.Stderr, "usage: lingdoc-material-check --knowledge-id ID --origin-file FILE [--project-id ID] [--token-file FILE]")
		os.Exit(2)
	}
	bearer, err := readToken(*tokenFile, *token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "material check cannot run: %v\n", err)
		os.Exit(2)
	}
	originBytes, err := os.ReadFile(*originFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "material check cannot run: cannot read the origin file: %v\n", err)
		os.Exit(2)
	}
	origin := string(originBytes)

	client := &http.Client{Timeout: time.Duration(*timeout) * time.Second}
	chunks, err := listChunks(client, *baseURL+*apiPath, bearer, *knowledgeID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "material check cannot run: cannot list the chunks: %v\n", err)
		os.Exit(2)
	}
	if len(chunks) == 0 {
		fmt.Fprintf(os.Stderr, "material check cannot run: the material %s has no chunks\n", *knowledgeID)
		os.Exit(2)
	}

	result := report{
		KnowledgeID:  *knowledgeID,
		ProjectID:    *projectID,
		OriginFile:   *originFile,
		OriginRunes:  utf8.RuneCountInString(origin),
		ChunkCount:   len(chunks),
		SourceStatus: map[string]string{},
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
		if *projectID != "" {
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
	if *projectID != "" {
		result.QuotedCheck = evidence.QualifyMaterial(origin, quotedChunks)
		result.QuotedCheckSkipped = false
		result.Qualified = result.Qualified && result.QuotedCheck.Qualified
	} else {
		// No project: the quoted-text half of the gate cannot run. It is
		// skipped, not failed — but the report says so, so nobody reads a
		// half-checked gate as a full one.
		result.QuotedCheckSkipped = true
		result.Notes = append(result.Notes,
			"没有指定项目：来源引文这一半没有跑（只比对了分块内容与原文切片）")
	}

	printReport(result)

	if *jsonOut != "" {
		if err := writeJSON(*jsonOut, result); err != nil {
			fmt.Fprintf(os.Stderr, "material check cannot write the report: %v\n", err)
			os.Exit(2)
		}
	}
	if !result.Qualified {
		os.Exit(1)
	}
}

func printReport(result report) {
	fmt.Printf("材料合格性前置比对\n")
	fmt.Printf("  知识=%s 项目=%s 原文=%s（%d 字符）分块=%d\n",
		result.KnowledgeID, result.ProjectID, result.OriginFile, result.OriginRunes, result.ChunkCount)
	fmt.Printf("  分块内容 vs 原文切片：\n%s", indent(result.ContentCheck.QualificationSummary()))
	if result.QuotedCheckSkipped {
		fmt.Println("  来源引文 vs 原文切片：（跳过——没有指定项目）")
	} else {
		fmt.Printf("  来源引文 vs 原文切片：\n%s", indent(result.QuotedCheck.QualificationSummary()))
	}
	for id, status := range result.SourceStatus {
		fmt.Printf("  来源状态 chunk=%s status=%s\n", id, status)
	}
	for _, note := range result.Notes {
		fmt.Printf("  ! %s\n", note)
	}
	if result.Qualified {
		fmt.Println("  结论：合格")
		return
	}
	fmt.Println("  结论：不合格 —— 这份材料不能进入验收：上面的分块不是原文在它自己坐标上的切片，来源复核会判 stale。")
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
