package workspacecore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/google/uuid"
	"maps"
	"regexp"
	"slices"
	"strings"
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
	ReplaceMembers(string, []Member) error
	Chapters(string) ([]Chapter, error)
	Chapter(string, string) (Chapter, error)
	InsertChapter(Chapter) error
	AppendChapter(Chapter, Chapter, int64) error
	Operation(OperationIdentity) (OperationResult, bool, error)
	SaveOperation(OperationIdentity, OperationResult) error
}

// ProjectAuthorizer is the application policy seam for LingDoc project
// access. It operates on the existing Transaction port so authorization and
// idempotent writes share one repository transaction; it does not depend on
// GORM or define a second identity/tenant model.
type ProjectAuthorizer interface {
	AuthorizeTenant(Transaction, Actor, string) error
	AuthorizeProject(Transaction, Actor, string, string) (Project, error)
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
	authorizer ProjectAuthorizer
}

func NewService(repository Repository, readers ...TemplateReader) *Service {
	reader := TemplateReader(ContractDemoTemplate{})
	if len(readers) > 0 && readers[0] != nil {
		reader = readers[0]
	}
	return NewServiceWithAuthorizer(repository, reader, nil, nil)
}

// NewServiceWithSources retains the main-line citation recheck before replay.
func NewServiceWithSources(repository Repository, reader TemplateReader, sources SourcePolicy) *Service {
	return NewServiceWithAuthorizer(repository, reader, sources, nil)
}

// NewServiceWithAuthorizer injects the project policy while keeping the
// repository/transaction architecture as the storage boundary. A nil policy
// uses the compatibility policy implemented by this package.
func NewServiceWithAuthorizer(repository Repository, reader TemplateReader, sources SourcePolicy, authorizer ProjectAuthorizer) *Service {
	if reader == nil {
		reader = ContractDemoTemplate{}
	}
	if authorizer == nil {
		authorizer = transactionProjectAuthorizer{}
	}
	return &Service{repository: repository, templates: reader, sources: sources, authorizer: authorizer}
}
func validActor(actor Actor) bool { return actor.TenantID != 0 && actor.UserID != "" }

type transactionProjectAuthorizer struct{}

func (transactionProjectAuthorizer) AuthorizeTenant(tx Transaction, actor Actor, capability string) error {
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
	if actor.Role != "" {
		if !actor.Role.IsValid() {
			return ErrNotFound
		}
		baseCapability := capability
		if before, _, ok := strings.Cut(capability, ":"); ok {
			baseCapability = before
		}
		required := map[string]types.TenantRole{
			"read": types.TenantRoleViewer, "create": types.TenantRoleContributor,
			"write": types.TenantRoleContributor, "manage": types.TenantRoleAdmin,
		}[baseCapability]
		if required == "" {
			return ErrInvalidRequest
		}
		if !actor.Role.HasPermission(required) {
			return ErrNotFound
		}
	}
	return nil
}

func (a transactionProjectAuthorizer) AuthorizeProject(tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	if err := a.AuthorizeTenant(tx, actor, capability); err != nil {
		return Project{}, err
	}
	return authorizeProject(tx, actor, projectID, capability)
}

func authorizeProject(tx Transaction, actor Actor, projectID, capability string) (Project, error) {
	baseCapability, resource := capability, ""
	if before, after, ok := strings.Cut(capability, ":"); ok {
		baseCapability, resource = before, after
	}
	if baseCapability != "read" && baseCapability != "write" && baseCapability != "manage" {
		return Project{}, ErrInvalidRequest
	}
	p, err := tx.Project(actor.TenantID, projectID)
	if err != nil {
		return Project{}, err
	}
	for _, m := range p.Members {
		if m.UserID != actor.UserID {
			continue
		}
		if m.Status != "" && m.Status != "active" {
			return Project{}, ErrNotFound
		}
		governance := m.GovernanceRole
		if governance == "" && m.Role == "owner" {
			governance = "owner"
		}
		if baseCapability == "manage" {
			if governance == "owner" || governance == "admin" || m.Role == "owner" {
				return p, nil
			}
			return Project{}, ErrNotFound
		}
		if baseCapability == "read" || m.Role == "owner" || governance == "owner" {
			return p, nil
		}
		if baseCapability == "write" {
			for _, role := range m.FunctionRoles {
				if role != "author" {
					continue
				}
				if resource == "" {
					return p, nil
				}
				for _, allowed := range m.FunctionScopes[role] {
					if allowed == resource {
						return p, nil
					}
				}
			}
		}
		return Project{}, ErrNotFound
	}
	return Project{}, ErrNotFound
}
func (s *Service) Authorize(ctx context.Context, actor Actor, projectID, capability string) error {
	return s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		_, err := s.authorizer.AuthorizeProject(tx, actor, projectID, capability)
		return err
	})
}
func (s *Service) GetProject(ctx context.Context, actor Actor, id string) (Project, error) {
	var result Project
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) (err error) {
		result, err = s.authorizer.AuthorizeProject(tx, actor, id, "read")
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
		if err := s.authorizer.AuthorizeTenant(tx, actor, "read"); err != nil {
			return err
		}
		projects, err := tx.Projects(actor, 51)
		result = projects
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
			if err := s.authorizer.AuthorizeTenant(tx, actor, capability); err != nil {
				return err
			}
		} else {
			var err error
			p, err = s.authorizer.AuthorizeProject(tx, actor, projectID, capability)
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
	ExpectedSpecRevision int64             `json:"expected_spec_revision"`
	Fields               map[string]string `json:"fields"`
}
type ActivateProjectInput struct {
	ExpectedSpecRevision int64 `json:"expected_spec_revision"`
}
type SaveMembersInput struct {
	ExpectedProjectVersion int64    `json:"expected_project_version"`
	CollaboratorUserIDs    []string `json:"collaborator_user_ids"`
	Members                []Member `json:"members,omitempty"`
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
	return s.operation(ctx, actor, "createProject", "projects", key, input, "", "create", func(tx Transaction, _ Project) (any, int, error) {
		p := Project{ID: uuid.NewString(), Name: input.Name, Status: "draft", ProjectVersion: 1, Spec: map[string]string{}, TemplateID: template.ID, TemplateVersion: template.Version, Members: []Member{{UserID: actor.UserID, Role: "owner"}}}
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
	return s.operation(ctx, actor, "saveSpec", projectID, key, input, projectID, "write", func(tx Transaction, p Project) (any, int, error) {
		if p.SpecRevision != input.ExpectedSpecRevision {
			return nil, 0, ErrVersionConflict
		}
		if !maps.Equal(p.Spec, input.Fields) {
			next := p
			next.Spec = maps.Clone(input.Fields)
			next.SpecRevision++
			next.ProjectVersion++
			if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
				return nil, 0, err
			}
			p = next
		}
		return p, 200, nil
	})
}
func (s *Service) SaveMembers(ctx context.Context, actor Actor, projectID, key string, input SaveMembersInput) (json.RawMessage, int, bool, error) {
	if input.Members != nil {
		return s.saveMemberPermissions(ctx, actor, projectID, key, input)
	}
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
		next := p
		next.Status = "active"
		next.ProjectVersion++
		if err := tx.UpdateProject(actor.TenantID, p, next); err != nil {
			return nil, 0, err
		}
		for _, section := range template.Sections {
			if err := tx.InsertChapter(Chapter{ID: uuid.NewString(), ProjectID: projectID, SectionID: section.ID, Title: section.Title}); err != nil {
				return nil, 0, err
			}
		}
		return next, 200, nil
	})
}
func (s *Service) ListChapters(ctx context.Context, actor Actor, projectID string) ([]Chapter, error) {
	var result []Chapter
	err := s.repository.Transaction(ctx, TransactionOptions{ReadOnly: true}, func(tx Transaction) error {
		if _, err := s.authorizer.AuthorizeProject(tx, actor, projectID, "read"); err != nil {
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
	return s.operation(ctx, actor, "saveChapter", projectID+"/"+chapterID, key, input, projectID, "write:"+chapterID, func(tx Transaction, p Project) (any, int, error) {
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
		p, err := s.authorizer.AuthorizeProject(tx, actor, projectID, "read")
		if err != nil {
			return err
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
