package workspace

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/candidateadoption"
)

// Reproduce Get-read -> Apply-write -> Get-stale-write deterministically. The
// observer holds a stale row while the actual store claims/persists the receipt.
func TestStaleObserverCannotEraseClaimedOrAppliedReceipt(t *testing.T) {
	for _, phase := range []string{"claimed", "applied"} {
		t.Run(phase, func(t *testing.T) {
			service, _, _, _, _ := inheritedRewriteFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			original := selectedRewriteRow{ID: "race-candidate", TenantID: 1, ActorID: "owner", ProjectID: "project", ChapterID: "chapter", IdempotencyKey: "race-create", RequestHash: "hash", Validity: string(candidateadoption.ValidityFresh), RunMode: "mock", ReplacementMarkdown: "replacement", SourceIDsJSON: "[]", AuthorizedSourcesJSON: "[]", ReviewItemsJSON: "[]", CreatedAt: now, UpdatedAt: now}
			row, _, err := service.store.createOrReplay(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			observer, err := service.store.byID(ctx, 1, "project", "chapter", row.ID)
			if err != nil {
				t.Fatal(err)
			}
			applied, err := service.store.claimApply(ctx, row, "apply-race-key", `{"working_copy":{}}`, now.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if phase == "applied" {
				applied.AppliedKey = "apply-race-key"
				applied.AppliedResponseJSON = `{"body_markdown":"replacement","working_copy_revision":2}`
				applied.ApplyPendingKey = ""
				applied.ApplyPendingInputJSON = ""
				if err := service.store.update(ctx, applied); err != nil {
					t.Fatal(err)
				}
			}
			latest, err := service.store.markStale(ctx, observer, now.Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			if phase == "claimed" {
				if latest.ApplyPendingKey != "apply-race-key" || latest.ApplyPendingInputJSON == "" {
					t.Fatal("observer erased pending claim")
				}
			} else {
				if latest.AppliedKey != "apply-race-key" || latest.AppliedResponseJSON != applied.AppliedResponseJSON {
					t.Fatal("observer erased completed receipt")
				}
				byKey, err := service.store.byOperation(ctx, 1, "project", "chapter", "apply-race-key")
				if err != nil || byKey.AppliedResponseJSON != applied.AppliedResponseJSON {
					t.Fatal("lost response cannot replay")
				}
			}
			if latest.Validity != string(candidateadoption.ValidityFresh) || latest.ReplacementMarkdown != "replacement" {
				t.Fatal("observer changed operation-owned candidate")
			}
		})
	}
}
func TestStaleUnclaimedObservationStillInvalidatesCandidate(t *testing.T) {
	service, _, _, _, _ := inheritedRewriteFixture(t)
	ctx := context.Background()
	now := time.Now().UTC()
	row := selectedRewriteRow{ID: "stale-candidate", TenantID: 1, ActorID: "owner", ProjectID: "project", ChapterID: "chapter", IdempotencyKey: "stale-create", RequestHash: "hash", Validity: string(candidateadoption.ValidityFresh), ReplacementMarkdown: "old", SourceIDsJSON: "[]", AuthorizedSourcesJSON: "[]", ReviewItemsJSON: "[]", CreatedAt: now, UpdatedAt: now}
	row, _, err := service.store.createOrReplay(ctx, row)
	if err != nil {
		t.Fatal(err)
	}
	latest, err := service.store.markStale(ctx, row, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if latest.Validity != string(candidateadoption.ValidityStale) || latest.ReplacementMarkdown != "" || latest.ErrorCode != "stale_input" {
		t.Fatal("unsafe candidate remained actionable")
	}
}
