package workspacecore

import (
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	academiccatalog "github.com/Tencent/WeKnora/internal/lingdoc/academic/catalog"
	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
	"github.com/google/uuid"
)

const (
	AcademicSourceCatalogKind  = "academic_source_catalog"
	AcademicCrossrefProviderID = "academic:crossref"
	// AcademicCrossrefResultProvider is the distinct, persisted result-source
	// value. It must not be confused with the task routing provider ID.
	AcademicCrossrefResultProvider   = academiccatalog.MetadataProviderCrossref
	AcademicGlobalCatalogVersion     = "crossref-metadata-v1"
	academicSourceCatalogObjectID    = "project"
	maximumAcademicSourceCatalogRule = 100
)

// AcademicSourceRule is a project-maintained extension to the global
// Crossref metadata-provider allowlist. Venue IDs are ISSNs for journals or
// stable, project-defined identifiers for conferences. A rule never grants
// rights to the publisher's full text.
type AcademicSourceRule struct {
	ID                 string    `json:"id"`
	Version            int64     `json:"version"`
	SourceType         string    `json:"source_type"`
	VenueName          string    `json:"venue_name"`
	VenueIDs           []string  `json:"venue_ids"`
	AllowedLicenseURLs []string  `json:"allowed_license_urls"`
	Maintainer         string    `json:"maintainer"`
	EffectiveAt        time.Time `json:"effective_at"`
}

// AcademicSourceCatalog stores append-only project policy revisions. The
// global provider policy is embedded in code and has its own stable version.
type AcademicSourceCatalog struct {
	ProjectID            string               `json:"project_id"`
	Version              int64                `json:"version"`
	GlobalVersion        string               `json:"global_version"`
	SourceCatalogVersion string               `json:"source_catalog_version"`
	Rules                []AcademicSourceRule `json:"rules"`
	UpdatedBy            string               `json:"updated_by,omitempty"`
	UpdatedAt            time.Time            `json:"updated_at,omitempty"`
}

type UpdateAcademicSourceCatalogInput struct {
	VersionExpectation
	Rules []AcademicSourceRule `json:"rules"`
}

func AcademicSourceCatalogVersion(rules []AcademicSourceRule) string {
	rules = normalizeAcademicSourceRuleOrder(rules)
	projectRules := make([]academiccatalog.Rule, 0, len(rules))
	for _, rule := range rules {
		projectRules = append(projectRules, academiccatalog.Rule{
			ID: rule.ID, Version: strconv.FormatInt(rule.Version, 10), SourceType: rule.SourceType,
			VenueName: rule.VenueName, VenueIDs: slices.Clone(rule.VenueIDs),
			AllowedLicenseURLs: slices.Clone(rule.AllowedLicenseURLs), Maintainer: rule.Maintainer, EffectiveAt: rule.EffectiveAt,
		})
	}
	return academiccatalog.Combine(academiccatalog.Snapshot{Version: AcademicGlobalCatalogVersion}, projectRules).Version
}

func normalizeAcademicSourceRuleOrder(rules []AcademicSourceRule) []AcademicSourceRule {
	out := slices.Clone(rules)
	slices.SortFunc(out, func(a, b AcademicSourceRule) int {
		return strings.Compare(a.ID, b.ID)
	})
	for index := range out {
		out[index].VenueIDs = sortedUniqueIDs(out[index].VenueIDs)
		out[index].AllowedLicenseURLs = sortedUniqueIDs(out[index].AllowedLicenseURLs)
	}
	return out
}

func normalizeAcademicSourceRules(rules []AcademicSourceRule, nextVersion int64, maintainer string, now time.Time) ([]AcademicSourceRule, error) {
	if len(rules) > maximumAcademicSourceCatalogRule {
		return nil, ErrInvalidRequest
	}
	seen := make(map[string]struct{}, len(rules))
	out := make([]AcademicSourceRule, 0, len(rules))
	for _, rule := range rules {
		rule.ID = strings.TrimSpace(rule.ID)
		rule.SourceType = strings.ToLower(strings.TrimSpace(rule.SourceType))
		rule.VenueName = strings.TrimSpace(rule.VenueName)
		// The recorded maintainer is provenance, so never trust a client-supplied
		// value. It must identify the authenticated actor making this revision.
		rule.Maintainer = strings.TrimSpace(maintainer)
		if rule.ID == "" {
			rule.ID = uuid.NewString()
		}
		if rule.ID == "project" || rule.VenueName == "" || rule.Maintainer == "" ||
			(rule.SourceType != "journal" && rule.SourceType != "conference" && rule.SourceType != "proceedings") {
			return nil, ErrInvalidRequest
		}
		if _, exists := seen[rule.ID]; exists {
			return nil, ErrInvalidRequest
		}
		seen[rule.ID] = struct{}{}
		rule.VenueIDs = sortedUniqueIDs(rule.VenueIDs)
		rule.AllowedLicenseURLs = sortedUniqueIDs(rule.AllowedLicenseURLs)
		if len(rule.VenueIDs) == 0 {
			return nil, ErrInvalidRequest
		}
		for _, value := range rule.AllowedLicenseURLs {
			parsed, err := url.Parse(value)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
				return nil, ErrInvalidRequest
			}
		}
		if rule.EffectiveAt.IsZero() {
			rule.EffectiveAt = now
		}
		rule.EffectiveAt = rule.EffectiveAt.UTC()
		rule.Version = nextVersion
		out = append(out, rule)
	}
	return normalizeAcademicSourceRuleOrder(out), nil
}

func emptyAcademicSourceCatalog(projectID string) AcademicSourceCatalog {
	catalog := AcademicSourceCatalog{ProjectID: projectID, GlobalVersion: AcademicGlobalCatalogVersion, Rules: []AcademicSourceRule{}}
	catalog.SourceCatalogVersion = AcademicSourceCatalogVersion(catalog.Rules)
	return catalog
}

func academicResearchCatalogVersion(sourceCatalogVersion string, assets []ProjectAssetVersion) string {
	if assets == nil {
		assets = []ProjectAssetVersion{}
	}
	raw, _ := json.Marshal(struct {
		Assets   []ProjectAssetVersion
		Provider string
	}{Assets: assets, Provider: AcademicCrossrefProviderID + "|rest-works-v1|" + sourceCatalogVersion})
	return sha256Hex(string(raw))
}

// currentAcademicSourceCatalogVersion reads the policy snapshot from the
// caller's transaction so candidate creation/confirmation cannot rely on a
// catalog value cached before the transaction began.
func currentAcademicSourceCatalog(tx Transaction, projectID string) (AcademicSourceCatalog, error) {
	store, err := agentObjectStore(tx)
	if err != nil {
		return AcademicSourceCatalog{}, err
	}
	catalog := emptyAcademicSourceCatalog(projectID)
	rows, err := store.Objects(projectID, AcademicSourceCatalogKind)
	if err != nil {
		return AcademicSourceCatalog{}, err
	}
	for _, row := range rows {
		if row.ID != academicSourceCatalogObjectID {
			continue
		}
		if row.Version < 1 || json.Unmarshal(row.Payload, &catalog) != nil {
			return AcademicSourceCatalog{}, ErrInvalidState
		}
		catalog.ProjectID = projectID
		catalog.Version = row.Version
		catalog.GlobalVersion = AcademicGlobalCatalogVersion
		catalog.Rules = normalizeAcademicSourceRuleOrder(catalog.Rules)
		catalog.SourceCatalogVersion = AcademicSourceCatalogVersion(catalog.Rules)
		break
	}
	return catalog, nil
}

func (s *Service) GetAcademicSourceCatalog(ctx context.Context, actor Actor, projectID string) (AcademicSourceCatalog, error) {
	out := emptyAcademicSourceCatalog(projectID)
	err := s.readWithCapability(ctx, actor, projectID, "read", func(tx Transaction, _ Project) error {
		var err error
		out, err = currentAcademicSourceCatalog(tx, projectID)
		return err
	})
	return out, err
}

func (s *Service) UpdateAcademicSourceCatalog(ctx context.Context, actor Actor, projectID, key string, input UpdateAcademicSourceCatalogInput) (json.RawMessage, int, bool, error) {
	if input.ExpectedProjectVersion < 1 || input.ExpectedContextRevision < 0 || input.ExpectedVersion < 0 {
		return nil, 0, false, ErrInvalidRequest
	}
	now := time.Now().UTC()
	rules, err := normalizeAcademicSourceRules(input.Rules, input.ExpectedVersion+1, actor.UserID, now)
	if err != nil {
		return nil, 0, false, err
	}
	return s.runAgentOperation(ctx, actor, projectID, "manage", "updateAcademicSourceCatalog", projectID, key, input, nil, operationcore.HumanGateNotRequired, func(tx Transaction, _ Project) (any, int, error) {
		if err := lockAcademicEvidenceProject(tx, actor, projectID); err != nil {
			return nil, 0, err
		}
		p, err := s.authorizeProject(ctx, tx, authorizationRequest(actor, projectID, AuthorizationManage, ""))
		if err != nil {
			return nil, 0, err
		}
		if !expectationMatches(p, input.VersionExpectation) {
			return nil, 0, ErrVersionConflict
		}
		var old StoredObject
		currentVersion := int64(0)
		rows, err := func() ([]StoredObject, error) {
			store, storeErr := agentObjectStore(tx)
			if storeErr != nil {
				return nil, storeErr
			}
			return store.Objects(projectID, AcademicSourceCatalogKind)
		}()
		if err != nil {
			return nil, 0, err
		}
		for _, row := range rows {
			if row.ID == academicSourceCatalogObjectID {
				old = row
				currentVersion = row.Version
				break
			}
		}
		if currentVersion != input.ExpectedVersion {
			return nil, 0, ErrVersionConflict
		}
		catalog := AcademicSourceCatalog{ProjectID: projectID, Version: currentVersion + 1, GlobalVersion: AcademicGlobalCatalogVersion, Rules: rules, UpdatedBy: actor.UserID, UpdatedAt: now}
		catalog.SourceCatalogVersion = AcademicSourceCatalogVersion(catalog.Rules)
		if err = storeStoredObject(tx, old, projectID, AcademicSourceCatalogKind, academicSourceCatalogObjectID, catalog.Version, catalog); err != nil {
			return nil, 0, err
		}
		if err = invalidateAcademicEvidenceForCatalog(tx, actor, projectID, catalog.SourceCatalogVersion); err != nil {
			return nil, 0, err
		}
		if err = recordAgentAuditEvent(tx, actor, projectID, "g2.academic_catalog.updated", academicSourceCatalogObjectID, "", map[string]any{"catalog_version": catalog.SourceCatalogVersion, "rule_count": len(catalog.Rules)}); err != nil {
			return nil, 0, err
		}
		return catalog, 200, nil
	})
}
