package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/lingdoc/sourcemarker"
	core "github.com/Tencent/WeKnora/internal/lingdoc/workspacecore"
	"gorm.io/gorm"
)

// DOCXCitationRef supplies the provenance for a source marker, never a body.
// Only refs whose source marker occurs in the saved section are persisted.
type DOCXCitationRef struct {
	SectionID           string `json:"sectionId"`
	SourceID            string `json:"sourceId"`
	EvidenceCardID      string `json:"evidenceCardId"`
	EvidenceCardVersion int64  `json:"evidenceCardVersion"`
}

type docxCitationValidator func(*gorm.DB, string, []string, []DOCXCitationRef) error

func normalizeDOCXCitations(refs []DOCXCitationRef) ([]DOCXCitationRef, error) {
	if len(refs) > 256 {
		return nil, errors.New("DOCX_CITATION_INVALID_REQUEST")
	}
	out := append([]DOCXCitationRef{}, refs...)
	sections := map[string]bool{}
	for _, spec := range docxSectionSpecs {
		sections[spec.ID] = true
	}
	for _, ref := range out {
		marker := "[[source:" + ref.SourceID + "]]"
		parsed := sourcemarker.IDs(marker)
		if !sections[ref.SectionID] || ref.SourceID == "" || len(ref.SourceID) > 128 || sourcemarker.Validate(marker) != nil || len(parsed) != 1 || parsed[0] != ref.SourceID || ref.EvidenceCardID == "" || len(ref.EvidenceCardID) > 128 || ref.EvidenceCardVersion < 1 {
			return nil, errors.New("DOCX_CITATION_INVALID_REQUEST")
		}
	}
	slices.SortFunc(out, func(a, b DOCXCitationRef) int {
		left := a.SectionID + "\x00" + a.SourceID + "\x00" + a.EvidenceCardID
		right := b.SectionID + "\x00" + b.SourceID + "\x00" + b.EvidenceCardID
		if compared := strings.Compare(left, right); compared != 0 {
			return compared
		}
		if a.EvidenceCardVersion < b.EvidenceCardVersion {
			return -1
		}
		if a.EvidenceCardVersion > b.EvidenceCardVersion {
			return 1
		}
		return 0
	})
	return slices.Compact(out), nil
}

func decodeDOCXCitations(raw string) ([]DOCXCitationRef, error) {
	refs := []DOCXCitationRef{}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &refs); err != nil {
			return nil, err
		}
	}
	return normalizeDOCXCitations(refs)
}

func docxCitationJSON(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}

// resolveDOCXCitations follows the authoritative saved bytes. A cached selection
// without a marker is ignored, and deletion/undo removes its source and refs.
// Previously persisted sources remain compatible with pre-citation clients;
// adding an unregistered source requires an explicit confirmed-card reference.
func resolveDOCXCitations(section DOCXProjectedChapter, baseSourceJSON, baseUsageJSON string, inherited, requested []DOCXCitationRef) ([]string, []core.CitationUsage, []DOCXCitationRef, error) {
	if err := sourcemarker.Validate(section.Body); err != nil {
		return nil, nil, nil, errors.New("DOCX_CITATION_INVALID: malformed source marker")
	}
	ids := sourcemarker.IDs(section.Body)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	var previousIDs []string
	if err := json.Unmarshal([]byte(baseSourceJSON), &previousIDs); err != nil {
		return nil, nil, nil, err
	}
	var previousUsages []core.CitationUsage
	if err := json.Unmarshal([]byte(baseUsageJSON), &previousUsages); err != nil {
		return nil, nil, nil, err
	}
	refs := []DOCXCitationRef{}
	for _, id := range ids {
		selected := []DOCXCitationRef{}
		for _, ref := range requested {
			if ref.SectionID == section.SectionID && ref.SourceID == id {
				selected = append(selected, ref)
			}
		}
		if len(selected) == 0 {
			for _, ref := range inherited {
				if ref.SectionID == section.SectionID && ref.SourceID == id {
					selected = append(selected, ref)
				}
			}
		}
		if len(selected) == 0 && !slices.Contains(previousIDs, id) {
			return nil, nil, nil, fmt.Errorf("DOCX_CITATION_REQUIRED: source %s has no confirmed evidence reference", id)
		}
		refs = append(refs, selected...)
	}
	usages := []core.CitationUsage{}
	for _, usage := range previousUsages {
		if slices.Contains(ids, usage.SourceID) {
			usages = append(usages, usage)
		}
	}
	refs, err := normalizeDOCXCitations(refs)
	return ids, usages, refs, err
}

// validateDOCXCitations reuses the live source policy and G2 card checks. This
// manual citation path deliberately does not ask for model-use consent.
func validateDOCXCitations(ctx context.Context, tx *gorm.DB, runtime *SourceRuntime, projects *core.Service, actor Actor, projectID, chapterID string, ids []string, refs []DOCXCitationRef) error {
	if len(ids) > 0 {
		verdicts, err := runtime.sourceRevalidator().Review(ctx, projectID, evidence.Actor{UserID: actor.UserID, TenantID: fmt.Sprint(actor.TenantID)}, ids)
		if err != nil {
			return fmt.Errorf("DOCX_SOURCE_UNAVAILABLE: %w", err)
		}
		if len(verdicts) != len(ids) {
			return errors.New("DOCX_SOURCE_UNAVAILABLE: incomplete review")
		}
		for _, verdict := range verdicts {
			if !verdict.Usable || !slices.Contains(ids, verdict.SourceID) {
				return errors.New("DOCX_SOURCE_UNAVAILABLE")
			}
		}
	}
	if len(refs) == 0 {
		return nil
	}
	cards, err := projects.ListEvidenceCards(ctx, actor, projectID)
	if err != nil {
		return fmt.Errorf("DOCX_CITATION_INVALID: %w", err)
	}
	reader := core.NewTransactionAdapter(tx).(interface {
		CurrentEvidenceObject(string, string, string) (core.ImpactObjectState, error)
	})
	for _, ref := range refs {
		index := slices.IndexFunc(cards, func(card core.EvidenceCard) bool { return card.ID == ref.EvidenceCardID })
		if index < 0 {
			return errors.New("DOCX_CITATION_INVALID: evidence card unavailable")
		}
		card := cards[index]
		if card.ProjectID != projectID || card.Version != ref.EvidenceCardVersion || card.Status != "CONFIRMED" || card.Confirmation == nil || !slices.Contains(card.SourceIDs, ref.SourceID) || !slices.Contains(card.ScopeChapterIDs, chapterID) || !slices.Contains(card.Confirmation.ScopeChapterIDs, chapterID) {
			return errors.New("DOCX_CITATION_INVALID: card version, confirmation, source or chapter scope changed")
		}
		current, err := reader.CurrentEvidenceObject(projectID, "EvidenceCard", card.ID)
		if err != nil || !current.Confirmed || current.Ref.Version != strconv.FormatInt(ref.EvidenceCardVersion, 10) {
			return errors.New("DOCX_CITATION_INVALID: confirmation is no longer current")
		}
	}
	return nil
}
