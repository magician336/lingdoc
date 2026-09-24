package delivery

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrSnapshotStaleInput = errors.New("delivery input is no longer current")

// DeliveryInput is the immutable value captured for checking and export.
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
	SectionID        string        `json:"-"`
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

// Confirmation freezes the T12 durable record, including the exact source asset versions.
type Confirmation struct {
	ID               string           `json:"id"`
	ChapterID        string           `json:"chapter_id"`
	ChapterVersionID string           `json:"chapter_version_id"`
	SpecRevision     int              `json:"spec_revision"`
	AssetVersions    []AssetVersion   `json:"asset_versions"`
	TemplateVersion  string           `json:"template_version"`
	ActorUserID      string           `json:"actor_user_id"`
	CreatedAt        time.Time        `json:"created_at"`
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
	Revision int    `json:"asset_revision"`
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

// CheckIssue is the shared ValidationIssue shape. Code and ChapterID stay internal for tests.
type CheckIssue struct {
	ID            string  `json:"id"`
	RuleID        string  `json:"rule_id"`
	RulesetHash   string  `json:"ruleset_hash"`
	Severity      string  `json:"severity"`
	TargetID      string  `json:"target_id"`
	TargetVersion *string `json:"target_version"`
	Message       string  `json:"message"`
	Code          string  `json:"-"`
	ChapterID     string  `json:"-"`
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

// CurrentnessChecker compares frozen versions with current workspace and source state.
type CurrentnessChecker interface {
	IsCurrent(DeliveryInput) (bool, error)
}
type CurrentnessFunc func(DeliveryInput) (bool, error)

func (f CurrentnessFunc) IsCurrent(input DeliveryInput) (bool, error) { return f(input) }

type alwaysCurrent struct{}

func (alwaysCurrent) IsCurrent(DeliveryInput) (bool, error) { return true, nil }

type ReleaseService struct {
	store       SnapshotStore
	currentness CurrentnessChecker
	now         func() time.Time
}

func NewReleaseService(store SnapshotStore, checkers ...CurrentnessChecker) *ReleaseService {
	checker := CurrentnessChecker(alwaysCurrent{})
	if len(checkers) > 0 && checkers[0] != nil {
		checker = checkers[0]
	}
	return &ReleaseService{store: store, currentness: checker, now: time.Now}
}

func (s *ReleaseService) Prepare(input DeliveryInput) (ReleaseSnapshot, error) {
	frozen := cloneInput(input)
	current, err := s.currentness.IsCurrent(frozen)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	if !current {
		return ReleaseSnapshot{}, ErrSnapshotStaleInput
	}
	digest, err := digestFrozenInput(frozen)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	id, err := opaqueID("snapshot")
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	snapshot := ReleaseSnapshot{ID: id, ProjectID: frozen.ProjectID, FrozenInput: frozen, SnapshotDigest: digest, Check: Evaluate(frozen), IsCurrent: true, CreatedAt: s.now().UTC()}
	if err := s.store.Save(snapshot); err != nil {
		return ReleaseSnapshot{}, err
	}
	return cloneSnapshot(snapshot), nil
}

// Get recalculates a dynamic flag without mutating frozen history or digest.
func (s *ReleaseService) Get(projectID, snapshotID string) (ReleaseSnapshot, error) {
	snapshot, err := s.store.Get(projectID, snapshotID)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	current, err := s.currentness.IsCurrent(snapshot.FrozenInput)
	if err != nil {
		return ReleaseSnapshot{}, err
	}
	snapshot.IsCurrent = current
	return snapshot, nil
}

func Evaluate(input DeliveryInput) CheckResult {
	result := CheckResult{ProjectVersion: input.ProjectVersion, RulesetHash: input.Template.RulesetHash, Status: CheckPassed}
	issue := func(code, chapterID, message string) {
		target := input.ProjectID
		if chapterID != "" {
			target = chapterID
		}
		result.Issues = append(result.Issues, CheckIssue{ID: fmt.Sprintf("%s-%s-%d", code, target, len(result.Issues)+1), RuleID: code, RulesetHash: input.Template.RulesetHash, Severity: "blocking", TargetID: target, TargetVersion: nil, Message: message, Code: code, ChapterID: chapterID})
	}
	if input.ProjectID == "" || input.DeliveryKind != "internal_demo" {
		issue("invalid_delivery_input", "", "project_id and internal_demo delivery_kind are required")
	}
	for _, field := range input.Template.RequiredFields {
		if strings.TrimSpace(input.Spec[field]) == "" {
			issue("required_field_missing", "", "required research condition is missing: "+field)
		}
	}
	validateFrozenCollections(input, issue)
	chapters := map[string]SnapshotChapter{}
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
func validateFrozenCollections(input DeliveryInput, issue func(string, string, string)) {
	sources := map[string]bool{}
	for _, source := range input.Sources {
		if source.ID == "" || sources[source.ID] || source.ProjectID == "" || source.AssetID == "" || source.AssetRevision < 1 || source.Locator == "" || source.DisplayTitle == "" {
			issue("invalid_frozen_source", "", "frozen sources require unique IDs and complete provenance")
		}
		sources[source.ID] = true
	}
	assets := map[string]bool{}
	for _, asset := range input.AssetVersions {
		if asset.AssetID == "" || asset.Revision < 1 || assets[asset.AssetID] {
			issue("invalid_asset_versions", "", "asset versions require unique asset IDs and positive revisions")
		}
		assets[asset.AssetID] = true
	}
	policy := map[string]bool{}
	for _, id := range input.PolicyAssetIDs {
		if id == "" || policy[id] {
			issue("invalid_policy_assets", "", "policy asset IDs must be non-empty and unique")
		}
		policy[id] = true
	}
}
func checkChapter(chapter SnapshotChapter, input DeliveryInput, issue func(string, string, string)) {
	if chapter.ChapterVersionID == nil {
		issue("chapter_version_missing", chapter.ChapterID, "chapter has no immutable version")
		return
	}
	if strings.TrimSpace(chapter.BodyMarkdown) == "" {
		issue("chapter_empty", chapter.ChapterID, "chapter body is empty")
	}
	ids := map[string]bool{}
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
	sources, assets, allowed := sourcesByID(input.Sources), assetVersions(input.AssetVersions), stringSet(input.PolicyAssetIDs)
	expectedAssets := map[string]int{}
	for id := range ids {
		source, ok := sources[id]
		if !ok || source.ProjectID != input.ProjectID || !allowed[source.AssetID] || assets[source.AssetID] != source.AssetRevision || sha256Text(source.QuotedText) != source.QuotedTextHash {
			issue("source_invalid", chapter.ChapterID, "source is not a complete frozen allowed record: "+id)
			continue
		}
		expectedAssets[source.AssetID] = source.AssetRevision
	}
	confirmation := chapter.Confirmation
	if confirmation == nil || confirmation.ID == "" || confirmation.ChapterID != chapter.ChapterID || confirmation.ChapterVersionID != *chapter.ChapterVersionID || confirmation.SpecRevision != input.SpecRevision || confirmation.TemplateVersion != input.Template.Version || confirmation.ActorUserID == "" || confirmation.CreatedAt.IsZero() {
		issue("confirmation_stale", chapter.ChapterID, "confirmation does not match the frozen chapter, specification, and template")
		return
	}
	checkDecisions(chapter, issue)
	if !sameAssetVersions(confirmation.AssetVersions, expectedAssets) {
		issue("confirmation_asset_versions_mismatch", chapter.ChapterID, "confirmation assets must exactly match the chapter's frozen sources")
	}
}
func checkDecisions(chapter SnapshotChapter, issue func(string, string, string)) {
	items, decisions := map[string]bool{}, map[string]bool{}
	for _, item := range chapter.ReviewItems {
		if item.ID == "" || item.Statement == "" || item.OriginCandidateID == "" || items[item.ID] {
			issue("review_items_invalid", chapter.ChapterID, "review items must have unique complete IDs")
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

type canonicalDeliveryInput struct {
	ProjectID      string                     `json:"project_id"`
	ProjectName    string                     `json:"project_name"`
	ProjectVersion int                        `json:"project_version"`
	SpecRevision   int                        `json:"spec_revision"`
	Spec           map[string]string          `json:"spec"`
	Template       Template                   `json:"template"`
	Chapters       []canonicalSnapshotChapter `json:"chapters"`
	Sources        []FrozenSource             `json:"sources"`
	AssetVersions  []AssetVersion             `json:"asset_versions"`
	PolicyAssetIDs []string                   `json:"policy_asset_ids"`
	DeliveryKind   string                     `json:"delivery_kind"`
}
type canonicalSnapshotChapter struct {
	ChapterID        string        `json:"chapter_id"`
	ChapterVersionID *string       `json:"chapter_version_id"`
	Title            string        `json:"title"`
	BodyMarkdown     string        `json:"body_markdown"`
	SourceIDs        []string      `json:"source_ids"`
	ReviewItems      []ReviewItem  `json:"review_items"`
	Confirmation     *Confirmation `json:"confirmation,omitempty"`
}

func digestFrozenInput(input DeliveryInput) (string, error) {
	chapters := make([]canonicalSnapshotChapter, len(input.Chapters))
	for i, chapter := range input.Chapters {
		chapters[i] = canonicalSnapshotChapter{ChapterID: chapter.ChapterID, ChapterVersionID: chapter.ChapterVersionID, Title: chapter.Title, BodyMarkdown: chapter.BodyMarkdown, SourceIDs: chapter.SourceIDs, ReviewItems: chapter.ReviewItems, Confirmation: chapter.Confirmation}
	}
	data, err := json.Marshal(canonicalDeliveryInput{ProjectID: input.ProjectID, ProjectName: input.ProjectName, ProjectVersion: input.ProjectVersion, SpecRevision: input.SpecRevision, Spec: input.Spec, Template: input.Template, Chapters: chapters, Sources: input.Sources, AssetVersions: input.AssetVersions, PolicyAssetIDs: input.PolicyAssetIDs, DeliveryKind: input.DeliveryKind})
	if err != nil {
		return "", fmt.Errorf("canonicalize frozen input: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func cloneInput(input DeliveryInput) DeliveryInput {
	copy := input
	copy.Spec = map[string]string{}
	for key, value := range input.Spec {
		copy.Spec[key] = value
	}
	copy.Template = cloneTemplate(input.Template)
	copy.Chapters = make([]SnapshotChapter, len(input.Chapters))
	for i, chapter := range input.Chapters {
		copy.Chapters[i] = cloneChapter(chapter)
	}
	copy.Sources = append([]FrozenSource(nil), input.Sources...)
	copy.AssetVersions = append([]AssetVersion(nil), input.AssetVersions...)
	copy.PolicyAssetIDs = append([]string(nil), input.PolicyAssetIDs...)
	return copy
}
func cloneChapter(chapter SnapshotChapter) SnapshotChapter {
	copy := chapter
	if chapter.ChapterVersionID != nil {
		value := *chapter.ChapterVersionID
		copy.ChapterVersionID = &value
	}
	copy.SourceIDs = append([]string(nil), chapter.SourceIDs...)
	copy.ReviewItems = append([]ReviewItem(nil), chapter.ReviewItems...)
	if chapter.Confirmation != nil {
		confirmation := *chapter.Confirmation
		confirmation.AssetVersions = append([]AssetVersion(nil), chapter.Confirmation.AssetVersions...)
		confirmation.Decisions = append([]ReviewDecision(nil), chapter.Confirmation.Decisions...)
		copy.Confirmation = &confirmation
	}
	return copy
}
func cloneSnapshot(snapshot ReleaseSnapshot) ReleaseSnapshot {
	snapshot.FrozenInput = cloneInput(snapshot.FrozenInput)
	snapshot.Check.Issues = append([]CheckIssue(nil), snapshot.Check.Issues...)
	return snapshot
}
func opaqueID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate %s id: %w", prefix, err)
	}
	return prefix + "-" + hex.EncodeToString(value[:]), nil
}
func sha256Text(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}
func sourcesByID(values []FrozenSource) map[string]FrozenSource {
	out := map[string]FrozenSource{}
	for _, value := range values {
		out[value.ID] = value
	}
	return out
}
func assetVersions(values []AssetVersion) map[string]int {
	out := map[string]int{}
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
func sameAssetVersions(actual []AssetVersion, expected map[string]int) bool {
	if len(actual) != len(expected) {
		return false
	}
	seen := map[string]bool{}
	for _, value := range actual {
		if value.AssetID == "" || value.Revision < 1 || seen[value.AssetID] || expected[value.AssetID] != value.Revision {
			return false
		}
		seen[value.AssetID] = true
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
func sortedIssueCodes(issues []CheckIssue) []string {
	out := make([]string, len(issues))
	for i, issue := range issues {
		out[i] = issue.Code
	}
	sort.Strings(out)
	return out
}
