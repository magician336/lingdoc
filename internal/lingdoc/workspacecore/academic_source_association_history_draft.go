package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/operationcore"
)

// AssociationView is a historical human assertion, never a current evidence
// grant. A revoked or changed source is not made available by this record.
type AcademicSourceAssociationView struct {
	AcademicSourceAssociation
	ReviewState string `json:"review_state"`
}
type AssociationRevocationReceipt struct {
	ID      string `json:"id"`
	Version int64  `json:"version"`
	Status  string `json:"status"`
}

func (s *Service) ListAcademicSourceAssociations(ctx context.Context, actor Actor, projectID string) ([]AcademicSourceAssociationView, error) {
	rows := []AcademicSourceAssociation{}
	err := s.readWithCapability(ctx, actor, projectID, "read", func(tx Transaction, _ Project) error {
		store, e := agentObjectStore(tx)
		if e != nil {
			return e
		}
		values, e := store.Objects(projectID, AcademicSourceAssociationKind)
		if e != nil {
			return e
		}
		for _, row := range values {
			var a AcademicSourceAssociation
			if e = json.Unmarshal(row.Payload, &a); e != nil {
				return e
			}
			rows = append(rows, a)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if s.agentRuntime.Sources == nil {
		return nil, ErrDependencyUnavailable
	}
	out := []AcademicSourceAssociationView{}
	for _, a := range rows {
		source, e := s.agentRuntime.Sources.ReadResearchSource(ctx, actor, projectID, a.SourceID)
		if e != nil {
			if errors.Is(e, ErrSourceUnavailable) || errors.Is(e, ErrNotFound) {
				continue
			}
			return nil, e
		} // Do not reveal a restricted source or associated metadata.
		state := "historical"
		if !validAssociationSource(source, a.SourceID) || source.AssetID != a.AssetID || source.AssetRevision != a.AssetRevision || source.Locator != a.Locator || source.QuotedTextHash != a.QuotedTextHash {
			state = "needs_review"
		}
		e = s.readWithCapability(ctx, actor, projectID, "read", func(tx Transaction, p Project) error {
			if err := nativeSourceScope(ctx, tx, actor, p, []string{a.SourceID}, false); err != nil {
				return err
			}
			current, _, err := loadStoredObject[AcademicSourceAssociation](tx, projectID, AcademicSourceAssociationKind, a.ID)
			if err != nil {
				return err
			}
			if current.Version != a.Version {
				return ErrVersionConflict
			}
			candidate, _, err := loadStoredObject[ImportCandidate](tx, projectID, ImportCandidateKind, a.CandidateRef.ID)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
			if err != nil || candidate.Version != a.CandidateRef.Version || candidate.DOI != a.DOI || candidate.PolicyStatus != "approved" || candidate.RetractionStatus == "retracted" || candidate.RetractionStatus == "retraction_notice" || candidate.DuplicateStatus == "duplicate" {
				state = "needs_review"
			}
			catalog, err := currentAcademicSourceCatalog(tx, projectID)
			gate, ok := s.agentRuntime.Research.(ResearchExecutionGate)
			if err != nil {
				return err
			}
			if !ok {
				return ErrAcademicGlobalCatalogUnavailable
			}
			if catalog.SourceCatalogVersion != a.CatalogVersion {
				state = "needs_review"
			} else {
				err = gate.ValidateAcademicResearchResult(ctx, actor, projectID, ResearchResult{Provider: "crossref", PolicySnapshotVersion: a.PolicySnapshotVersion}, catalog)
				if errors.Is(err, ErrVersionConflict) {
					state = "needs_review"
				} else if err != nil {
					return err
				}
			}
			if !versionBasisCurrent(p, a.Basis) {
				state = "needs_review"
			}
			return nil
		})
		if e != nil {
			if errors.Is(e, ErrSourceUnavailable) || errors.Is(e, ErrNotFound) {
				continue
			}
			return nil, e
		}
		if a.Status == "revoked" {
			state = "revoked"
		} else if a.Status != "recorded" {
			state = "needs_review"
		}
		out = append(out, AcademicSourceAssociationView{AcademicSourceAssociation: a, ReviewState: state})
	}
	return out, nil
}

// Revoke creates only a tombstone. The response and audit do not expose the
// source, so an authorized reviewer can withdraw their assertion even after
// access to that source has been removed.
func (s *Service) RevokeAcademicSourceAssociation(ctx context.Context, actor Actor, projectID, associationID, key string, input ConfirmationInput) (json.RawMessage, int, bool, error) {
	if err := checkConfirmed(input.Confirmed, input.Reason); err != nil {
		return nil, 0, false, err
	}
	if strings.TrimSpace(associationID) == "" {
		return nil, 0, false, ErrInvalidRequest
	}
	return s.runAgentOperation(ctx, actor, projectID, "evidence-confirm", "revokeAcademicSourceAssociation", projectID+"/"+associationID, key, input, nil, operationcore.HumanGateRequired, func(tx Transaction, _ Project) (any, int, error) {
		locked, err := s.lockAssociationDecisionRows(ctx, tx, actor, projectID)
		if err != nil {
			return nil, 0, err
		}
		p := locked
		a, old, err := loadStoredObject[AcademicSourceAssociation](tx, projectID, AcademicSourceAssociationKind, associationID)
		if err != nil {
			return nil, 0, err
		}
		if !expectationMatches(p, input.VersionExpectation) || a.Version != input.ExpectedVersion {
			return nil, 0, ErrVersionConflict
		}
		if a.Status != "recorded" {
			return nil, 0, ErrInvalidState
		}
		now := time.Now().UTC()
		a.Version++
		a.Status = "revoked"
		a.RevokedBy = actor.UserID
		a.RevokedAt = &now
		a.RevocationReason = strings.TrimSpace(input.Reason)
		if err = storeStoredObject(tx, old, projectID, AcademicSourceAssociationKind, a.ID, a.Version, a); err != nil {
			return nil, 0, err
		}
		if err = recordAgentAuditEvent(tx, actor, projectID, "g2.academic_source_association.revoke", projectID+"/"+a.ID, a.RevocationReason, map[string]any{"version": a.Version}); err != nil {
			return nil, 0, err
		}
		return AssociationRevocationReceipt{ID: a.ID, Version: a.Version, Status: a.Status}, 200, nil
	})
}
