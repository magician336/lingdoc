package delivery

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// DeliveryInput is the complete, point-in-time value that a release is
// checked against.  T12 supplies chapter confirmations and T09 supplies the
// persistent source records; this contract deliberately does not read either
// domain's mutable storage while evaluating a frozen value.
type DeliveryInput struct {
	ProjectID      string            `json:"project_id"`
	ProjectName    string            `json:"project_name"`
	ProjectVersion int               `json:"project_version"`
	SpecRevision   int               `json:"spec_revision"`
	Spec           map[string]string `json:"spec"`
	Template       Template          `json:"template"`
	Chapters       []SnapshotChapter `json:"chapters"`
	Sources        []FrozenSource    `json:"sources"`
	AssetVersions  []AssetVersion    `json:"asset_versions"`
	PolicyAssetIDs []string          `json:"policy_asset_ids"`
	DeliveryKind   string            `json:"delivery_kind"`
}

type SnapshotChapter struct {
	ChapterID        string        `json:"chapter_id"`
	ChapterVersionID *string       `json:"chapter_version_id"`
	SectionID        string        `json:"section_id"`
	Title            string        `json:"title"`
	BodyMarkdown     string        `json:"body_markdown"`
	SourceIDs        []string      `json:"source_ids"`
	ReviewItems      []ReviewItem  `json:"review_items"`
	Confirmation     *Confirmation `json:"confirmation,omitempty"`
}

type ReviewItem struct {
	ID                string `json:"id"`
	Statement         string `json:"statement"`
	OriginCandidateID string `json:"origin_candidate_id"`
}

type ReviewDecision struct {
	ReviewItemID string `json:"review_item_id"`
	Disposition  string `json:"disposition"`
	Reason       string `json:"reason"`
}

// Confirmation is already tied to the immutable chapter and spec that was
// confirmed. T12 can map its durable confirmation record to this value.
type Confirmation struct {
	ChapterVersionID string           `json:"chapter_version_id"`
	SpecRevision     int              `json:"spec_revision"`
	TemplateVersion  string           `json:"template_version"`
	Decisions        []ReviewDecision `json:"review_decisions"`
}

type FrozenSource struct {
	ID             string `json:"id"`
	ProjectID      string `json:"project_id"`
	AssetID        string `json:"asset_id"`
	AssetRevision  int    `json:"asset_revision"`
	Locator        string `json:"locator"`
	QuotedText     string `json:"quoted_text"`
	QuotedTextHash string `json:"quoted_text_hash"`
	DisplayTitle   string `json:"display_title"`
}

type AssetVersion struct {
	AssetID  string `json:"asset_id"`
	Revision int    `json:"revision"`
}

type CheckStatus string

const (
	CheckPassed  CheckStatus = "passed"
	CheckBlocked CheckStatus = "blocked"
)

type CheckResult struct {
	ProjectVersion int          `json:"project_version"`
	RulesetHash    string       `json:"ruleset_hash"`
	Status         CheckStatus  `json:"status"`
	Issues         []CheckIssue `json:"issues"`
}

type CheckIssue struct {
	Code      string `json:"code"`
	ChapterID string `json:"chapter_id,omitempty"`
	Message   string `json:"message"`
}

type ReleaseSnapshot struct {
	ID             string        `json:"id"`
	ProjectID      string        `json:"project_id"`
	FrozenInput    DeliveryInput `json:"frozen_input"`
	SnapshotDigest string        `json:"snapshot_digest"`
	Check          CheckResult   `json:"check"`
	IsCurrent      bool          `json:"is_current"`
	CreatedAt      time.Time     `json:"created_at"`
}

// SnapshotStore is the narrow persistence boundary required by T13. A real
// store may be added later without changing check semantics.
type SnapshotStore interface {
	Save(ReleaseSnapshot) error
	Get(projectID, snapshotID string) (ReleaseSnapshot, error)
}

type MemorySnapshotStore struct {
	mu        sync.RWMutex
	snapshots map[string]ReleaseSnapshot
}

func NewMemorySnapshotStore() *MemorySnapshotStore {
	return &MemorySnapshotStore{snapshots: make(map[string]ReleaseSnapshot)}
}

func (s *MemorySnapshotStore) Save(snapshot ReleaseSnapshot) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshots[snapshot.ProjectID+"/"+snapshot.ID] = cloneSnapshot(snapshot)
	return nil
}

func (s *MemorySnapshotStore) Get(projectID, snapshotID string) (ReleaseSnapshot, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot, ok := s.snapshots[projectID+"/"+snapshotID]
	if !ok {
		return ReleaseSnapshot{}, fmt.Errorf("release snapshot not found")
	}
	return cloneSnapshot(snapshot), nil
}

type ReleaseService struct {
	store SnapshotStore
	now   func() time.Time
	mu    sync.Mutex
	next  int
}

func NewReleaseService(store SnapshotStore) *ReleaseService {
	return &ReleaseService{store: store, now: time.Now}
}

// Prepare freezes and persists both passing and blocked checks. A blocked
// snapshot is useful evidence, but Export must only accept CheckPassed.
func (s *ReleaseService) Prepare(input DeliveryInput) (ReleaseSnapshot, error) {
	frozen, err := cloneInput(input)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	digest, err := digestFrozenInput(frozen)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	s.mu.Lock()
	s.next++
	id := fmt.Sprintf("snapshot-%06d", s.next)
	s.mu.Unlock()
	snapshot := ReleaseSnapshot{
		ID: id, ProjectID: frozen.ProjectID, FrozenInput: frozen,
		SnapshotDigest: digest, Check: Evaluate(frozen), IsCurrent: true,
		CreatedAt: s.now().UTC(),
	}
	if err := s.store.Save(snapshot); err != nil {
		return ReleaseSnapshot{}, err
	}
	return cloneSnapshot(snapshot), nil
}

// Evaluate applies the invariant checks that can be evaluated from the frozen
// input alone. This intentionally treats absent versions and confirmations as
// blocked rather than inventing values for an incomplete chapter.
func Evaluate(input DeliveryInput) CheckResult {
	result := CheckResult{ProjectVersion: input.ProjectVersion, RulesetHash: input.Template.RulesetHash, Status: CheckPassed}
	issue := func(code, chapterID, message string) {
		result.Issues = append(result.Issues, CheckIssue{Code: code, ChapterID: chapterID, Message: message})
	}
	if input.ProjectID == "" || input.DeliveryKind != "internal_demo" {
		issue("invalid_delivery_input", "", "project_id and internal_demo delivery_kind are required")
	}
	for _, field := range input.Template.RequiredFields {
		if strings.TrimSpace(input.Spec[field]) == "" {
			issue("required_field_missing", "", "required research condition is missing: "+field)
		}
	}
	chapters := make(map[string]SnapshotChapter, len(input.Chapters))
	for _, chapter := range input.Chapters {
		if chapter.SectionID == "" || chapters[chapter.SectionID].SectionID != "" {
			issue("invalid_chapter_section", chapter.ChapterID, "chapter section must be present and unique")
		}
		chapters[chapter.SectionID] = chapter
		checkChapter(chapter, input, issue)
	}
	for _, section := range input.Template.Sections {
		if section.Required && chapters[section.ID].SectionID == "" {
			issue("required_chapter_missing", "", "required template section is missing: "+section.ID)
		}
	}
	if len(result.Issues) > 0 {
		result.Status = CheckBlocked
	}
	return result
}

func checkChapter(chapter SnapshotChapter, input DeliveryInput, issue func(string, string, string)) {
	if chapter.ChapterVersionID == nil {
		issue("chapter_version_missing", chapter.ChapterID, "chapter has no immutable version")
		return
	}
	if strings.TrimSpace(chapter.BodyMarkdown) == "" {
		issue("chapter_empty", chapter.ChapterID, "chapter body is empty")
	}
	if chapter.Confirmation == nil || chapter.Confirmation.ChapterVersionID != *chapter.ChapterVersionID ||
		chapter.Confirmation.SpecRevision != input.SpecRevision || chapter.Confirmation.TemplateVersion != input.Template.Version {
		issue("confirmation_stale", chapter.ChapterID, "confirmation does not match the frozen chapter, specification, and template")
	} else {
		checkDecisions(chapter, issue)
	}
	ids := make(map[string]bool, len(chapter.SourceIDs))
	for _, id := range chapter.SourceIDs {
		if id == "" || ids[id] {
			issue("invalid_source_ids", chapter.ChapterID, "source IDs must be non-empty and unique")
			break
		}
		ids[id] = true
	}
	if !sameStringSet(ids, citationIDs(chapter.BodyMarkdown)) {
		issue("citation_mismatch", chapter.ChapterID, "citation markers must exactly match source_ids")
	}
	sources := sourcesByID(input.Sources)
	assets := assetVersions(input.AssetVersions)
	allowed := stringSet(input.PolicyAssetIDs)
	for id := range ids {
		source, ok := sources[id]
		if !ok || source.ProjectID != input.ProjectID || !allowed[source.AssetID] || assets[source.AssetID] != source.AssetRevision ||
			sha256Text(source.QuotedText) != source.QuotedTextHash {
			issue("source_invalid", chapter.ChapterID, "source is not a complete frozen allowed record: "+id)
		}
	}
}

func checkDecisions(chapter SnapshotChapter, issue func(string, string, string)) {
	items, decisions := make(map[string]bool), make(map[string]bool)
	for _, item := range chapter.ReviewItems {
		if item.ID == "" || items[item.ID] {
			issue("review_items_invalid", chapter.ChapterID, "review item IDs must be unique")
			return
		}
		items[item.ID] = true
	}
	for _, decision := range chapter.Confirmation.Decisions {
		if decisions[decision.ReviewItemID] || !items[decision.ReviewItemID] || (decision.Disposition != "resolved" && decision.Disposition != "retained_warning") || strings.TrimSpace(decision.Reason) == "" {
			issue("review_decisions_invalid", chapter.ChapterID, "review decisions must exactly cover items with a reason")
			return
		}
		decisions[decision.ReviewItemID] = true
	}
	if len(items) != len(decisions) {
		issue("review_decisions_incomplete", chapter.ChapterID, "all review items need one decision")
	}
}

func digestFrozenInput(input DeliveryInput) (string, error) {
	data, err := json.Marshal(input) // encoding/json emits string-keyed maps in sorted-key order.
	if err != nil {
		return "", fmt.Errorf("canonicalize frozen input: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func cloneInput(input DeliveryInput) (DeliveryInput, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return DeliveryInput{}, err
	}
	var copy DeliveryInput
	if err := json.Unmarshal(data, &copy); err != nil {
		return DeliveryInput{}, err
	}
	return copy, nil
}
func cloneSnapshot(snapshot ReleaseSnapshot) ReleaseSnapshot {
	copy, err := cloneInput(snapshot.FrozenInput)
	if err != nil {
		panic(err)
	}
	snapshot.FrozenInput = copy
	snapshot.Check.Issues = append([]CheckIssue(nil), snapshot.Check.Issues...)
	return snapshot
}
func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func stringSet(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}
func sourcesByID(values []FrozenSource) map[string]FrozenSource {
	out := make(map[string]FrozenSource, len(values))
	for _, value := range values {
		out[value.ID] = value
	}
	return out
}
func assetVersions(values []AssetVersion) map[string]int {
	out := make(map[string]int, len(values))
	for _, value := range values {
		out[value.AssetID] = value.Revision
	}
	return out
}
func sameStringSet(left, right map[string]bool) bool {
	if len(left) != len(right) {
		return false
	}
	for value := range left {
		if !right[value] {
			return false
		}
	}
	return true
}
func citationIDs(body string) map[string]bool {
	out := map[string]bool{}
	for remaining := body; ; {
		start := strings.Index(remaining, "[[source:")
		if start < 0 {
			return out
		}
		remaining = remaining[start+9:]
		end := strings.Index(remaining, "]]")
		if end < 0 {
			return map[string]bool{"__invalid__": true}
		}
		id := remaining[:end]
		if id == "" {
			return map[string]bool{"__invalid__": true}
		}
		out[id] = true
		remaining = remaining[end+2:]
	}
}

// sortedIssueCodes is used by tests and makes failure reports stable for UI
// adapters without changing the original evaluation order.
func sortedIssueCodes(issues []CheckIssue) []string {
	out := make([]string, len(issues))
	for i, issue := range issues {
		out[i] = issue.Code
	}
	sort.Strings(out)
	return out
}

