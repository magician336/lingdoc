package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/google/uuid"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repository owns atomic commit/rollback and database isolation. Callbacks see
// only domain values. ReadOnly gives a repeatable snapshot; RetryLocks allows
// bounded retries of rolled-back spec transactions, never partial writes.
type Repository interface {
	Transaction(context.Context, TransactionOptions, func(Transaction) error) error
}
type TransactionOptions struct{ ReadOnly, RetryLocks bool }

// Transaction is a storage port, not a second application service. Conditional
// writes return ErrVersionConflict unless the expected state alone changes.
type Transaction interface {
	ActiveMember(Actor) (bool, error)
	Project(uint64, string) (Project, error)
	Projects(Actor, int) ([]Project, error)
	InsertProject(uint64, Project) error
	UpdateProject(uint64, Project, Project) error
	ReplaceCollaborators(string, []string) error
	Chapters(string) ([]Chapter, error)
	Chapter(string, string) (Chapter, error)
	InsertChapter(Chapter) error
	AppendChapter(Chapter, Chapter, int64) error
	Operation(OperationIdentity) (OperationResult, bool, error)
	SaveOperation(OperationIdentity, OperationResult) error
}

type draftCandidateTransaction interface {
	InsertDraftCandidate(DraftCandidate) error
	DraftCandidates(string) ([]DraftCandidate, error)
}

type auditTransaction interface {
	RecordAudit(AuditEvent) error
	AuditEvents(string) ([]AuditEvent, error)
}

type ownerTransferTransaction interface {
	InsertOwnerTransfer(OwnerTransfer) error
	OwnerTransfer(string, string) (OwnerTransfer, error)
	AcceptOwnerTransfer(string, string, int64, time.Time) error
	SetMemberRole(string, string, string) error
	EnsureMember(string, string, string) error
}
type OperationIdentity struct {
	Actor                  Actor
	Operation, Target, Key string
}
type OperationResult struct {
	BodyHash string
	Body     json.RawMessage
	Status   int
}

// Service owns validation, permissions, version expectations and replay order.
// Swapping storage cannot swap or omit these rules.
type Service struct {
	repository Repository
	templates  TemplateReader
	sources    SourcePolicy
}

func NewService(repository Repository, readers ...TemplateReader) *Service {
	reader := TemplateReader(ContractDemoTemplate{})
	if len(readers) > 0 && readers[0] != nil {
		reader = readers[0]
	}
	return &Service{repository: repository, templates: reader}
}

// NewServiceWithSources retains the main-line citation recheck before replay.
func NewServiceWithSources(repository Repository, reader TemplateReader, sources SourcePolicy) *Service {
	s := NewService(repository, reader)
	s.sources = sources
	return s
}
func validActor(actor Actor) bool { return actor.TenantID != 0 && actor.UserID != "" }
func authorize(tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	if capability != "read" && capability != "write" && capability != "manage" {
		return Project{}, ErrInvalidRequest
	}
	if !validActor(actor) {
		return Project{}, ErrNotFound
	}
	active, err := tx.ActiveMember(actor)
	if err != nil {
		return Project{}, err
	}
	if !active {
		return Project{}, ErrNotFound
	}
	p, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		return Project{}, err
	}
	if p.DiscardedAt != nil {
		if capability != "read" {
			return Project{}, ErrInvalidState
		}
		owner := false
		for _, m := range p.Members {
			if m.UserID == actor.UserID && m.Role == "owner" {
				owner = true
				break
			}
		}
		if !owner {
			return Project{}, ErrNotFound
		}
	}
	for _, m := range p.Members {
		if m.UserID == actor.UserID && (capability != "manage" || m.Role == "owner") {
			return p, nil
		}
	}
	return Project{}, ErrNotFound
}
func (s *Service) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	return s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := authorize(tx, actor, projectID, capability)
		if err == nil && p.DiscardedAt != nil {
			return ErrInvalidState
		}
		return err
	})
}
func (s *Service) GetProject(ctx context.Context, actor Actor, id string) (Project, error) {
	var result Project
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) (err error) {
		result, err = authorize(tx, actor, id, "read")
		if err == nil && result.DiscardedAt != nil {
			result.Spec = map[string]string{}
			result.SpecFields = map[string]SpecField{}
		}
		return err
	})
	return result, err
}
func (s *Service) ListProjects(ctx context.Context, actor Actor) ([]Project, bool, error) {
	var result []Project
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if !validActor(actor) {
			return ErrNotFound
		}
		active, err := tx.ActiveMember(actor)
		if err != nil {
			return err
		}
		if !active {
			return ErrNotFound
		}
		result, err = tx.Projects(actor, 51)
		return err
	})
	if err != nil {
		return nil, false, err
	}
	truncated := len(result) > 50
	if truncated {
		result = result[:50]
	}
	return result, truncated, nil
}

func projectOwner(p Project, userID string) bool {
	for _, member := range p.Members {
		if member.UserID == userID && member.Role == "owner" {
			return true
		}
	}
	return false
}

func (s *Service) DiscardProject(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (json.RawMessage, int, bool, error) {
	if expectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	input := map[string]any{"expected_project_version": expectedProjectVersion}
	return s.operation(ctx, actor, "discardProject", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != expectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		if p.DiscardedAt != nil || p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		now := time.Now().UTC()
		next := p
		next.DiscardedAt = &now
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.discard", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion}, CreatedAt: now}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}

func (s *Service) RestoreProject(ctx context.Context, actor Actor, projectID, key string, expectedProjectVersion int64) (json.RawMessage, int, bool, error) {
	if expectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	input := map[string]any{"expected_project_version": expectedProjectVersion}
	return s.operation(ctx, actor, "restoreProject", projectID, key, input, projectID, "read", func(tx Transaction, p Project) (any, int, error) {
		if !projectOwner(p, actor.UserID) {
			return nil, 0, ErrNotFound
		}
		if p.DiscardedAt == nil {
			return nil, 0, ErrInvalidState
		}
		if p.ProjectVersion != expectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		next := p
		next.DiscardedAt = nil
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.restore", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}

// Current authorization precedes replay; only new operations check expected
// versions. Exact response and domain mutation commit together.
func (s *Service) operation(ctx context.Context, actor Actor, op, target, key string, body any, projectID, capability string, write func(Transaction, Project) (any, int, error)) (json.RawMessage, int, bool, error) {
	if !validActor(actor) || len(key) < 8 || len(key) > 128 {
		return nil, 0, false, ErrInvalidRequest
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, 0, false, ErrInvalidRequest
	}
	hash := sha256.Sum256(encoded)
	hashString := hex.EncodeToString(hash[:])
	id := OperationIdentity{Actor: actor, Operation: op, Target: target, Key: key}
	var result OperationResult
	var replayed bool
	err = s.repository.Transaction(ctx, TransactionOptions{RetryLocks: op == "saveSpec"}, func(tx Transaction) error {
		var p Project
		if projectID == "" {
			active, err := tx.ActiveMember(actor)
			if err != nil {
				return err
			}
			if !active {
				return ErrNotFound
			}
		} else {
			var err error
			p, err = authorize(tx, actor, projectID, capability)
			if err != nil {
				return err
			}
		}
		if op == "saveChapter" {
			input := body.(SaveChapterInput)
			if len(input.SourceIDs) > 0 {
				if s.sources == nil {
					return ErrSourceUnavailable
				}
				ids := slices.Clone(input.SourceIDs)
				slices.Sort(ids)
				if err := s.sources.Validate(ctx, projectID, actor.UserID, ids); err != nil {
					return err
				}
			}
		}
		previous, found, err := tx.Operation(id)
		if err != nil {
			return err
		}
		if found {
			if previous.BodyHash != hashString {
				return ErrIdempotencyConflict
			}
			result, replayed = previous, true
			return nil
		}
		value, code, err := write(tx, p)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return err
		}
		result = OperationResult{BodyHash: hashString, Body: raw, Status: code}
		return tx.SaveOperation(id, result)
	})
	if err != nil {
		return nil, 0, false, err
	}
	return result.Body, result.Status, replayed, nil
}

type CreateProjectInput struct {
	Name       string `json:"name"`
	TemplateID string `json:"template_id"`
}
type SaveSpecInput struct {
	ExpectedSpecRevision int64                     `json:"expected_spec_revision"`
	Fields               map[string]string         `json:"fields"`
	FieldMetadata        map[string]SpecFieldInput `json:"field_metadata,omitempty"`
}
type SpecFieldInput struct {
	Origin     string            `json:"origin,omitempty"`
	Status     string            `json:"status,omitempty"`
	Provenance *ProvenanceRecord `json:"provenance,omitempty"`
}
type DraftCandidateInput struct {
	Kind       string            `json:"kind"`
	Title      string            `json:"title"`
	Content    string            `json:"content"`
	Level      string            `json:"level"`
	Provenance *ProvenanceRecord `json:"provenance,omitempty"`
}
type OwnerTransferInput struct {
	ToUserID               string `json:"to_user_id"`
	ExpectedProjectVersion int64  `json:"expected_project_version"`
}
type ActivateProjectInput struct {
	ExpectedSpecRevision   int64 `json:"expected_spec_revision"`
	ExpectedProjectVersion int64 `json:"expected_project_version,omitempty"`
	ReviewedProjectVersion int64 `json:"reviewed_project_version,omitempty"`
}
type TemplateMigrationInput struct {
	ExpectedProjectVersion int64  `json:"expected_project_version"`
	TemplateID             string `json:"template_id"`
	TemplateVersion        string `json:"template_version"`
}

func templateFieldDefinitions(template Template) map[string]TemplateField {
	fields := make(map[string]TemplateField, len(template.RequiredFields))
	for _, field := range template.Fields {
		fields[field.ID] = field
	}
	for _, field := range template.RequiredFields {
		value := fields[field]
		value.ID, value.Required = field, true
		if value.Type == "" {
			value.Type = "string"
		}
		fields[field] = value
	}
	return fields
}

func compatibleTemplateValue(value string, field TemplateField) bool {
	switch field.Type {
	case "", "string", "text":
		return true
	case "number", "integer":
		_, err := strconv.ParseFloat(value, 64)
		return err == nil
	case "boolean":
		_, err := strconv.ParseBool(value)
		return err == nil
	default:
		return false
	}
}

func buildTemplateMigrationPreview(p Project, target Template, expectedVersion int64) TemplateMigrationPreview {
	allowed := templateFieldDefinitions(target)
	keys := make([]string, 0, len(p.Spec))
	for key := range p.Spec {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	preview := TemplateMigrationPreview{
		ProjectID: p.ID, SourceTemplateID: p.TemplateID, SourceTemplateVersion: p.TemplateVersion,
		TargetTemplateID: target.ID, TargetTemplateVersion: target.Version, ExpectedProjectVersion: expectedVersion,
		Fields: make([]TemplateMigrationField, 0, len(keys)+len(target.RequiredFields)), MissingRequired: []string{}, Orphaned: []string{}, Incompatible: []string{},
	}
	seen := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		status := "orphaned"
		if field, ok := allowed[key]; ok {
			status = "preserved"
			if !compatibleTemplateValue(p.Spec[key], field) {
				status = "incompatible"
				preview.Incompatible = append(preview.Incompatible, key)
				if field.Required {
					preview.MissingRequired = append(preview.MissingRequired, key)
				}
			}
		} else {
			preview.Orphaned = append(preview.Orphaned, key)
		}
		preview.Fields = append(preview.Fields, TemplateMigrationField{FieldID: key, Value: p.Spec[key], Status: status})
		seen[key] = struct{}{}
	}
	requiredKeys := make([]string, 0, len(allowed))
	for key, field := range allowed {
		if !field.Required {
			continue
		}
		requiredKeys = append(requiredKeys, key)
	}
	slices.Sort(requiredKeys)
	for _, key := range requiredKeys {
		if _, ok := seen[key]; ok {
			continue
		}
		preview.Fields = append(preview.Fields, TemplateMigrationField{FieldID: key, Status: "missing"})
		preview.MissingRequired = append(preview.MissingRequired, key)
	}
	slices.Sort(preview.MissingRequired)
	slices.Sort(preview.Orphaned)
	slices.Sort(preview.Incompatible)
	return preview
}

func (s *Service) PreviewTemplateMigration(ctx context.Context, actor Actor, projectID string, input TemplateMigrationInput) (TemplateMigrationPreview, error) {
	if input.ExpectedProjectVersion < 0 || strings.TrimSpace(input.TemplateID) == "" {
		return TemplateMigrationPreview{}, ErrInvalidRequest
	}
	target, err := s.templates.Get(strings.TrimSpace(input.TemplateID), strings.TrimSpace(input.TemplateVersion))
	if err != nil {
		return TemplateMigrationPreview{}, err
	}
	var result TemplateMigrationPreview
	err = s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := authorize(tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		if p.Status != "draft" || p.DiscardedAt != nil {
			return ErrInvalidState
		}
		if input.ExpectedProjectVersion > 0 && p.ProjectVersion != input.ExpectedProjectVersion {
			return ErrVersionConflict
		}
		result = buildTemplateMigrationPreview(p, target, p.ProjectVersion)
		return nil
	})
	return result, err
}

func (s *Service) ChangeTemplate(ctx context.Context, actor Actor, projectID, key string, input TemplateMigrationInput) (json.RawMessage, int, bool, error) {
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.TemplateVersion = strings.TrimSpace(input.TemplateVersion)
	if input.ExpectedProjectVersion < 1 || input.TemplateID == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	target, err := s.templates.Get(input.TemplateID, input.TemplateVersion)
	if err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "changeTemplate", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		preview := buildTemplateMigrationPreview(p, target, input.ExpectedProjectVersion)
		next := p
		next.TemplateID, next.TemplateVersion = target.ID, target.Version
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_template.change", Target: projectID, Details: map[string]any{"from": p.TemplateID + "/" + p.TemplateVersion, "to": target.ID + "/" + target.Version, "missing_required": preview.MissingRequired, "orphaned": preview.Orphaned}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}

type SaveMembersInput struct {
	ExpectedProjectVersion int64    `json:"expected_project_version"`
	CollaboratorUserIDs    []string `json:"collaborator_user_ids"`
}
type SaveChapterInput struct {
	ExpectedChapterVersionID *string  `json:"expected_chapter_version_id"`
	ExpectedSpecRevision     int64    `json:"expected_spec_revision"`
	BodyMarkdown             string   `json:"body_markdown"`
	SourceIDs                []string `json:"source_ids"`
}

func (s *Service) CreateProject(ctx context.Context, actor Actor, key string, input CreateProjectInput) (json.RawMessage, int, bool, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 {
		return nil, 0, false, ErrInvalidRequest
	}
	template, err := s.templates.Get(input.TemplateID, "")
	if err != nil {
		return nil, 0, false, err
	}
	return s.operation(ctx, actor, "createProject", "projects", key, input, "", "", func(tx Transaction, _ Project) (any, int, error) {
		p := Project{ID: uuid.NewString(), Name: input.Name, Status: "draft", ProjectVersion: 1, CurrentContextRevision: 0, DeliveryStatus: "NOT_READY", Spec: map[string]string{}, TemplateID: template.ID, TemplateVersion: template.Version, Members: []Member{{UserID: actor.UserID, Role: "owner"}}}
		return p, 201, tx.InsertProject(actor.TenantID, p)
	})
}
func (s *Service) SaveSpec(ctx context.Context, actor Actor, projectID, key string, input SaveSpecInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.Fields == nil {
		return nil, 0, false, ErrInvalidRequest
	}
	for k, v := range input.Fields {
		if strings.TrimSpace(k) == "" || len(k) > 100 || len(v) > 10000 {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	for key, metadata := range input.FieldMetadata {
		if _, ok := input.Fields[key]; !ok || !validSpecFieldMetadata(metadata) {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	return s.operation(ctx, actor, "saveSpec", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		nextFields := maps.Clone(p.SpecFields)
		if nextFields == nil {
			nextFields = map[string]SpecField{}
		}
		changedFields := make(map[string]struct{})
		for key, value := range input.Fields {
			field := nextFields[key]
			origin, status := field.Origin, field.Status
			provenance := field.Provenance
			if metadata, ok := input.FieldMetadata[key]; ok {
				origin, status, provenance = metadata.Origin, metadata.Status, metadata.Provenance
				if origin == "" {
					origin = "human"
				}
				if status == "" {
					status = "draft"
				}
			} else {
				if origin == "" {
					origin = "human"
				}
				if status == "" {
					status = "draft"
				}
				if field.Value != value {
					provenance = nil
				}
			}
			if field.Value != value || field.Origin != origin || field.Status != status || !reflect.DeepEqual(field.Provenance, provenance) {
				field.Value = value
				field.ModifiedBy = actor.UserID
				field.ModifiedAt = time.Now().UTC()
				field.Origin, field.Status, field.Provenance = origin, status, provenance
				changedFields[key] = struct{}{}
			}
			nextFields[key] = field
		}
		if !maps.Equal(p.Spec, input.Fields) || !maps.Equal(p.SpecFields, nextFields) {
			next := p
			next.Spec = maps.Clone(input.Fields)
			next.SpecFields = nextFields
			next.SpecRevision++
			next.ProjectVersion++
			for key := range changedFields {
				field := next.SpecFields[key]
				field.ModifiedProjectVersion = next.ProjectVersion
				next.SpecFields[key] = field
			}
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return nil, 0, err
			}
			if audit, ok := tx.(auditTransaction); ok {
				if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project_spec.save", Target: projectID, Details: map[string]any{"spec_revision": next.SpecRevision, "field_metadata": next.SpecFields}, CreatedAt: time.Now().UTC()}); err != nil {
					return nil, 0, err
				}
			}
			p = next
		}
		return p, 200, nil
	})
}

func validSpecFieldMetadata(input SpecFieldInput) bool {
	origin := input.Origin
	if origin == "" {
		origin = "human"
	}
	status := input.Status
	if status == "" {
		status = "draft"
	}
	validOrigin := origin == "human" || origin == "ai_generated" || origin == "ai_assisted_human"
	validStatus := status == "draft" || status == "pending_confirmation" || status == "confirmed" || status == "superseded"
	if !validOrigin || !validStatus {
		return false
	}
	if (origin == "ai_generated" || origin == "ai_assisted_human") && status == "confirmed" {
		return false
	}
	return input.Provenance == nil || input.Provenance.SourceType != "" && input.Provenance.CreatedBy != ""
}

func (s *Service) CreateDraftCandidate(ctx context.Context, actor Actor, projectID, key string, input DraftCandidateInput) (json.RawMessage, int, bool, error) {
	input.Kind = strings.TrimSpace(input.Kind)
	input.Title = strings.TrimSpace(input.Title)
	if input.Kind == "" || input.Title == "" || input.Content == "" || len(input.Content) > 200000 {
		return nil, 0, false, ErrInvalidRequest
	}
	if input.Level == "" {
		input.Level = "background"
	}
	if input.Level != "candidate_evidence" && input.Level != "background" && input.Level != "discovery" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "createDraftCandidate", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		candidateStore, ok := tx.(draftCandidateTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		candidate := DraftCandidate{ID: uuid.NewString(), ProjectID: projectID, Kind: input.Kind, Title: input.Title, Content: input.Content, Level: input.Level, BasedOnContextRevision: p.CurrentContextRevision, Provenance: input.Provenance, CreatedBy: actor.UserID, CreatedAt: time.Now().UTC()}
		if err := candidateStore.InsertDraftCandidate(candidate); err != nil {
			return nil, 0, err
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "draft_candidate.create", Target: candidate.ID, Details: map[string]any{"level": candidate.Level, "kind": candidate.Kind}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return candidate, 201, nil
	})
}

func (s *Service) ListAuditEvents(ctx context.Context, actor Actor, projectID string) ([]AuditEvent, error) {
	var result []AuditEvent
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := authorize(tx, actor, projectID, "read"); err != nil {
			return err
		}
		audit, ok := tx.(auditTransaction)
		if !ok {
			return ErrInvalidState
		}
		var err error
		result, err = audit.AuditEvents(projectID)
		return err
	})
	return result, err
}

func (s *Service) RequestOwnerTransfer(ctx context.Context, actor Actor, projectID, key string, input OwnerTransferInput) (json.RawMessage, int, bool, error) {
	input.ToUserID = strings.TrimSpace(input.ToUserID)
	if input.ToUserID == "" || input.ToUserID == actor.UserID || input.ExpectedProjectVersion < 1 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "requestOwnerTransfer", projectID, key, input, "", "", func(tx Transaction, _ Project) (any, int, error) {
		p, err := tx.Project(actor.TenantID, projectID)
		if err != nil {
			return nil, 0, err
		}
		currentOwner := ""
		for _, member := range p.Members {
			if member.Role == "owner" {
				currentOwner = member.UserID
				break
			}
		}
		if currentOwner == "" {
			return nil, 0, ErrInvalidState
		}
		ownerActive, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: currentOwner})
		if err != nil {
			return nil, 0, err
		}
		isRecoveryAdmin := actor.SystemAdmin || actor.TenantRole == "admin" || actor.TenantRole == "owner"
		if actor.UserID != currentOwner && !(isRecoveryAdmin && !ownerActive) {
			return nil, 0, ErrNotFound
		}
		if p.ProjectVersion != input.ExpectedProjectVersion || p.Status == "archived" {
			return nil, 0, ErrVersionConflict
		}
		if p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: input.ToUserID})
		if err != nil || !active {
			return nil, 0, ErrInvalidState
		}
		transferStore, ok := tx.(ownerTransferTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		transfer := OwnerTransfer{ID: uuid.NewString(), ProjectID: projectID, FromUserID: currentOwner, ToUserID: input.ToUserID, Status: "pending", ExpectedProjectVersion: p.ProjectVersion, CreatedAt: time.Now().UTC()}
		if err := transferStore.InsertOwnerTransfer(transfer); err != nil {
			return nil, 0, err
		}
		return transfer, 201, nil
	})
}

func (s *Service) AcceptOwnerTransfer(ctx context.Context, actor Actor, projectID, transferID, key string) (json.RawMessage, int, bool, error) {
	return s.operation(ctx, actor, "acceptOwnerTransfer", projectID+"/"+transferID, key, map[string]string{"transfer_id": transferID}, "", "", func(tx Transaction, _ Project) (any, int, error) {
		if !validActor(actor) {
			return nil, 0, ErrNotFound
		}
		p, err := tx.Project(actor.TenantID, projectID)
		if err != nil {
			return nil, 0, err
		}
		if p.DiscardedAt != nil {
			return nil, 0, ErrInvalidState
		}
		store, ok := tx.(ownerTransferTransaction)
		if !ok {
			return nil, 0, ErrInvalidState
		}
		transfer, err := store.OwnerTransfer(projectID, transferID)
		if err != nil || transfer.Status != "pending" || transfer.ToUserID != actor.UserID {
			return nil, 0, ErrInvalidState
		}
		if transfer.ExpectedProjectVersion != p.ProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		active, err := tx.ActiveMember(actor)
		if err != nil || !active {
			return nil, 0, ErrNotFound
		}
		if err := store.EnsureMember(projectID, actor.UserID, "collaborator"); err != nil {
			return nil, 0, err
		}
		next := p
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		if err := store.SetMemberRole(projectID, transfer.FromUserID, "collaborator"); err != nil {
			return nil, 0, err
		}
		if err := store.SetMemberRole(projectID, actor.UserID, "owner"); err != nil {
			return nil, 0, err
		}
		acceptedAt := time.Now().UTC()
		if err := store.AcceptOwnerTransfer(projectID, transferID, transfer.ExpectedProjectVersion, acceptedAt); err != nil {
			return nil, 0, err
		}
		transfer.Status = "accepted"
		transfer.AcceptedAt = &acceptedAt
		return transfer, 200, nil
	})
}

func (s *Service) ActivationDiff(ctx context.Context, actor Actor, projectID string, sinceProjectVersion int64) (ActivationDiff, error) {
	var result ActivationDiff
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := authorize(tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		if sinceProjectVersion < 0 {
			return ErrInvalidRequest
		}
		result = ActivationDiff{ProjectID: projectID, CurrentProjectVersion: p.ProjectVersion, CurrentSpecRevision: p.SpecRevision, ChangedSinceVersion: sinceProjectVersion, ChangedFields: []SpecFieldChange{}, PendingAIFields: []SpecFieldChange{}}
		for key, field := range p.SpecFields {
			change := SpecFieldChange{Key: key, Value: field.Value, Origin: field.Origin, Status: field.Status, ModifiedBy: field.ModifiedBy, ModifiedAt: field.ModifiedAt, ModifiedProjectVersion: field.ModifiedProjectVersion}
			if (field.Origin == "ai_generated" || field.Origin == "ai_assisted_human") && field.Status == "pending_confirmation" {
				result.PendingAIFields = append(result.PendingAIFields, change)
			}
			if field.ModifiedProjectVersion > sinceProjectVersion || (field.ModifiedProjectVersion == 0 && sinceProjectVersion < p.ProjectVersion) {
				result.ChangedFields = append(result.ChangedFields, change)
			}
		}
		slices.SortFunc(result.ChangedFields, func(a, b SpecFieldChange) int { return strings.Compare(a.Key, b.Key) })
		slices.SortFunc(result.PendingAIFields, func(a, b SpecFieldChange) int { return strings.Compare(a.Key, b.Key) })
		return nil
	})
	return result, err
}

func (s *Service) ListDraftCandidates(ctx context.Context, actor Actor, projectID string) ([]DraftCandidate, error) {
	var result []DraftCandidate
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := authorize(tx, actor, projectID, "read"); err != nil {
			return err
		}
		p, err := tx.Project(actor.TenantID, projectID)
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return ErrInvalidState
		}
		store, ok := tx.(draftCandidateTransaction)
		if !ok {
			return ErrInvalidState
		}
		result, err = store.DraftCandidates(projectID)
		return err
	})
	return result, err
}
func (s *Service) SaveMembers(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.CollaboratorUserIDs == nil || len(input.CollaboratorUserIDs) > 20 {
		return nil, 0, false, ErrInvalidRequest
	}
	ids := slices.Clone(input.CollaboratorUserIDs)
	slices.Sort(ids)
	if len(slices.Compact(slices.Clone(ids))) != len(ids) {
		return nil, 0, false, ErrInvalidRequest
	}
	for _, id := range ids {
		if strings.TrimSpace(id) != id || id == "" || len(id) > 64 || id == actor.UserID {
			return nil, 0, false, ErrInvalidRequest
		}
	}
	return s.operation(ctx, actor, "saveMembers", projectID, key, input, projectID, "manage", func(tx Transaction, p Project) (any, int, error) {
		if p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		for _, id := range ids {
			active, err := tx.ActiveMember(Actor{TenantID: actor.TenantID, UserID: id})
			if err != nil {
				return nil, 0, err
			}
			if !active {
				return nil, 0, ErrInvalidState
			}
		}
		existing := []string{}
		members := []Member{}
		for _, m := range p.Members {
			if m.Role == "collaborator" {
				existing = append(existing, m.UserID)
			} else {
				members = append(members, m)
			}
		}
		slices.Sort(existing)
		if !slices.Equal(existing, ids) {
			next := p
			next.ProjectVersion++
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return nil, 0, err
			}
			if err := tx.ReplaceCollaborators(projectID, ids); err != nil {
				return nil, 0, err
			}
			for _, id := range ids {
				members = append(members, Member{UserID: id, Role: "collaborator"})
			}
			slices.SortFunc(members, func(a, b Member) int { return strings.Compare(a.UserID, b.UserID) })
			next.Members = members
			p = next
		}
		return p, 200, nil
	})
}
func (s *Service) ActivateProject(ctx context.Context, actor Actor, projectID, key string, input ActivateProjectInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "activateProject", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		owner := false
		for _, member := range p.Members {
			if member.UserID == actor.UserID && member.Role == "owner" {
				owner = true
				break
			}
		}
		if !owner {
			return nil, 0, ErrNotFound
		}
		if input.ExpectedProjectVersion < 1 || input.ReviewedProjectVersion < 1 {
			return nil, 0, ErrInvalidRequest
		}
		if input.ExpectedProjectVersion > 0 && p.ProjectVersion != input.ExpectedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		if input.ReviewedProjectVersion > 0 && p.ProjectVersion != input.ReviewedProjectVersion {
			return nil, 0, ErrVersionConflict
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		if p.Status != "draft" {
			return nil, 0, ErrInvalidState
		}
		template, err := s.templates.Get(p.TemplateID, p.TemplateVersion)
		if err != nil || template.Version != p.TemplateVersion {
			return nil, 0, ErrInvalidState
		}
		for _, field := range template.RequiredFields {
			if strings.TrimSpace(p.Spec[field]) == "" {
				return nil, 0, ErrInvalidState
			}
		}
		for fieldID, field := range templateFieldDefinitions(template) {
			if field.Required && !compatibleTemplateValue(p.Spec[fieldID], field) {
				return nil, 0, ErrInvalidState
			}
		}
		next := p
		next.Status = "active"
		next.CurrentContextRevision = 1
		next.DeliveryStatus = "NOT_READY"
		next.BaselineConfirmationID = uuid.NewString()
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		for _, section := range template.Sections {
			if err := tx.InsertChapter(Chapter{ID: uuid.NewString(), ProjectID: projectID, SectionID: section.ID, Title: section.Title}); err != nil {
				return nil, 0, err
			}
		}
		if audit, ok := tx.(auditTransaction); ok {
			if err := audit.RecordAudit(AuditEvent{ID: uuid.NewString(), ProjectID: projectID, ActorID: actor.UserID, Action: "project.activate", Target: projectID, Details: map[string]any{"project_version": next.ProjectVersion, "context_revision": next.CurrentContextRevision, "chapter_count": len(template.Sections), "delivery_status": next.DeliveryStatus, "baseline_confirmation_id": next.BaselineConfirmationID}, CreatedAt: time.Now().UTC()}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}
func (s *Service) ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error) {
	var result []Chapter
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := authorize(tx, actor, projectID, "read"); err != nil {
			return err
		}
		var err error
		result, err = tx.Chapters(projectID)
		return err
	})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range result {
		ids = append(ids, c.SourceIDs...)
	}
	if len(ids) > 0 {
		if s.sources == nil {
			return nil, ErrSourceUnavailable
		}
		if err := s.sources.Validate(ctx, projectID, actor.UserID, ids); err != nil {
			return nil, err
		}
	}
	return result, nil
}

var sourceMarker = regexp.MustCompile(`\[\[source:([A-Za-z0-9_-]+)\]\]`)

func (s *Service) SaveChapter(ctx context.Context, actor Actor, projectID, chapterID, key string, input SaveChapterInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedSpecRevision < 0 || input.SourceIDs == nil || len(input.BodyMarkdown) > 200000 {
		return nil, 0, false, ErrInvalidRequest
	}
	markers := sourceMarker.FindAllStringSubmatch(input.BodyMarkdown, -1)
	if strings.Count(input.BodyMarkdown, "[[source:") != len(markers) {
		return nil, 0, false, ErrInvalidRequest
	}
	refs := make([]string, 0, len(markers))
	for _, m := range markers {
		refs = append(refs, m[1])
	}
	slices.Sort(refs)
	refs = slices.Compact(refs)
	declared := slices.Clone(input.SourceIDs)
	slices.Sort(declared)
	if len(slices.Compact(slices.Clone(declared))) != len(declared) || !slices.Equal(refs, declared) {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.operation(ctx, actor, "saveChapter", projectID+"/"+chapterID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.Status != "active" {
			return nil, 0, ErrInvalidState
		}
		if p.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		old, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return nil, 0, err
		}
		if !sameVersion(old.CurrentVersionID, input.ExpectedChapterVersionID) {
			return nil, 0, ErrVersionConflict
		}
		nextProject := p
		nextProject.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, nextProject); err != nil {
			return nil, 0, err
		}
		next := old
		id := uuid.NewString()
		next.CurrentVersionID = &id
		next.BodyMarkdown = input.BodyMarkdown
		next.SourceIDs = declared
		next.ConfirmationValid = false
		if err := tx.AppendChapter(old, next, p.SpecRevision); err != nil {
			return nil, 0, err
		}
		return next, 201, nil
	})
}
func sameVersion(a, b *string) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func (s *Service) GenerationContext(ctx context.Context, actor Actor, projectID, chapterID string) (GenerationContext, error) {
	var result GenerationContext
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		p, err := authorize(tx, actor, projectID, "read")
		if err != nil {
			return err
		}
		if p.DiscardedAt != nil {
			return ErrInvalidState
		}
		chapter, err := tx.Chapter(projectID, chapterID)
		if err != nil {
			return err
		}
		result = GenerationContext{ProjectID: p.ID, ProjectVersion: p.ProjectVersion, SpecRevision: p.SpecRevision, Spec: p.Spec, TemplateID: p.TemplateID, TemplateVersion: p.TemplateVersion, ChapterID: chapter.ID, ChapterVersionID: chapter.CurrentVersionID, ChapterBody: chapter.BodyMarkdown, Chapter: chapter}
		return nil
	})
	return result, err
}
