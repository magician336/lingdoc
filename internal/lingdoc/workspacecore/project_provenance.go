package workspacecore

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/evidence"
	"github.com/Tencent/WeKnora/internal/types"
	"gorm.io/gorm"
)

// The digest is an immutable input identity, not a permission grant. Trusted
// builders additionally require the private context proof below.
type SpecProvenanceRef struct {
	FieldKey  string `json:"field_key"`
	ValueHash string `json:"value_hash"`
	PinID     string `json:"pin_id,omitempty"`
}
type ProjectProvenanceSnapshot struct {
	ProjectID           string              `json:"project_id"`
	SpecRevision        int64               `json:"spec_revision"`
	ContextRevision     int64               `json:"context_revision"`
	TemplateCopyVersion int64               `json:"template_copy_version"`
	Fields              []SpecProvenanceRef `json:"fields"`
	Digest              string              `json:"digest"`
}
type provenanceSourceVersion struct {
	ID, AssetID, Hash string
	Revision          int
}
type provenancePin struct {
	ID, ProjectID, ValueHash, Kind, ObjectID, SessionID, LocalHash, SourceDigest string
	ObjectVersion                                                                int64
	Record                                                                       ProvenanceRecord
	Sources                                                                      []provenanceSourceVersion
	SourceIDs                                                                    []string
	OriginalAIHashes                                                             []string
	Verified                                                                     bool
}
type provenanceRequest struct {
	Key, Value, Origin, Creator string
	Record                      *ProvenanceRecord
	Fresh                       bool
}
type provenanceProofKey struct{}
type provenanceSourceIOKey struct{}
type provenanceProof struct {
	Project                  Project
	ActorID                  string
	TenantID                 uint64
	Identity                 string
	ForModel                 bool
	Snapshot                 ProjectProvenanceSnapshot
	Pins                     map[string]provenancePin
	States                   map[string]bool
	Requests                 map[string]provenancePin
	FreshPins                map[string]provenancePin
	NativeBefore             map[string]string
	Operation, OperationBody string
	Frozen                   bool
}

func provenanceRecordKey(pid, value string, record ProvenanceRecord) string {
	record.Accessible = false
	record.NeedsReview = false
	return "g1." + hashImpactValue(struct {
		Project, Value string
		Record         ProvenanceRecord
	}{pid, hashImpactValue(value), record})
}
func provenanceRequestKey(record *ProvenanceRecord, value string) string {
	return hashImpactValue(struct {
		Record *ProvenanceRecord
		Value  string
	}{record, value})
}
func provenanceSnapshotDigest(snapshot ProjectProvenanceSnapshot) string {
	snapshot.Digest = ""
	return hashImpactValue(snapshot)
}
func provenanceProjectHash(p Project) string {
	return hashImpactValue(struct {
		ID                           string
		Version, Spec, Context, Copy int64
		Values                       map[string]string
		Fields                       map[string]SpecField
	}{p.ID, p.ProjectVersion, p.SpecRevision, p.CurrentContextRevision, p.TemplateCopyVersion, p.Spec, p.SpecFields})
}
func provenanceIdentityMatches(ctx context.Context, actor Actor) bool {
	caller := types.CallerFromContext(ctx)
	if caller.UserID != "" && (caller.UserID != actor.UserID || caller.TenantID != actor.TenantID) {
		return false
	}
	tenant, present := types.TenantIDFromContext(ctx)
	return !present || tenant == actor.TenantID
}
func provenanceSourceContext(ctx context.Context, actor Actor) context.Context {
	if types.CallerFromContext(ctx).UserID == "" {
		ctx = types.WithCaller(ctx, types.Caller{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role})
	}
	return ctx
}
func loadProvenancePin(tx Transaction, pid, value string, record *ProvenanceRecord) (provenancePin, bool, error) {
	if record == nil {
		return provenancePin{}, false, nil
	}
	store, ok := tx.(projectRecordTransaction)
	if !ok {
		return provenancePin{}, false, nil
	}
	id := provenanceRecordKey(pid, value, *record)
	var pin provenancePin
	_, err := store.GetProjectRecord(pid, "g1_provenance", id, &pin)
	if errors.Is(err, ErrNotFound) {
		return pin, false, nil
	}
	if err != nil {
		return pin, false, err
	}
	if pin.ID != id || pin.ProjectID != pid || pin.ValueHash != hashImpactValue(value) || provenanceRecordKey(pid, value, pin.Record) != id {
		return pin, false, ErrSourceUnavailable
	}
	return pin, true, nil
}
func unknownProvenancePin(p Project, actor Actor, request provenanceRequest) provenancePin {
	record := ProvenanceRecord{}
	if request.Record != nil {
		record = *request.Record
	}
	record.CreatedBy = actor.UserID
	record.CreatedAt = time.Now().UTC()
	record.Accessible = false
	record.NeedsReview = true
	record.AuthorizationScope = "project/" + p.ID
	pin := provenancePin{ProjectID: p.ID, ValueHash: hashImpactValue(request.Value), Record: record, SourceIDs: []string{}, Sources: []provenanceSourceVersion{}, OriginalAIHashes: []string{}}
	pin.ID = provenanceRecordKey(p.ID, request.Value, record)
	return pin
}
func provenanceVersionMatches(value string, version int64) bool {
	return value == "" || value == strconv.FormatInt(version, 10)
}
func nativeSourceDigest(tx Transaction, pid string, ids []string) (string, error) {
	if len(ids) == 0 {
		return hashImpactValue([]string{}), nil
	}
	port, ok := tx.(impactComputationSourceTransaction)
	if !ok {
		return "", ErrSourceUnavailable
	}
	return port.ImpactSourceDigest(pid, ids)
}

// Only local typed objects are read here. All gateway/origin IO is performed
// after the parent transaction has released SQLite's pooled connection.
func (s *Service) resolveProvenancePin(ctx context.Context, tx Transaction, actor Actor, p Project, request provenanceRequest) (provenancePin, error) {
	pin := unknownProvenancePin(p, actor, request)
	if request.Record == nil {
		return pin, nil
	}
	if previous, found, err := loadProvenancePin(tx, p.ID, request.Value, request.Record); err != nil {
		return pin, err
	} else if found {
		return previous, nil
	}
	record := pin.Record
	sourceType := strings.ToLower(strings.TrimSpace(record.SourceType))
	id := record.SourceID
	if id == "" {
		return pin, nil
	}
	if (sourceType == "source" || sourceType == "upload") && s.agentRuntime.Sources == nil {
		return pin, nil
	}
	switch sourceType {
	case "agent_turn", "conversation":
		store, ok := tx.(ObjectStore)
		if !ok {
			return pin, nil
		}
		turnID := id
		if sourceType == "conversation" {
			turnID = record.SourceVersion
		}
		object, err := store.Object(p.ID, AgentTurnKind, turnID)
		if errors.Is(err, ErrNotFound) {
			return pin, nil
		}
		if err != nil {
			return pin, err
		}
		var turn AgentTurn
		if err = json.Unmarshal(object.Payload, &turn); err != nil {
			return pin, err
		}
		sessionObject, err := store.Object(p.ID, AgentSessionKind, turn.SessionID)
		if errors.Is(err, ErrNotFound) {
			return pin, nil
		}
		if err != nil {
			return pin, err
		}
		var session AgentSession
		if err = json.Unmarshal(sessionObject.Payload, &session); err != nil {
			return pin, err
		}
		if turn.ID != turnID || turn.ProjectID != p.ID || turn.Version != object.Version || turn.Status != "completed" || turn.CreatedBy != actor.UserID || session.ProjectID != p.ID || session.ID != turn.SessionID || session.CreatedBy != actor.UserID || session.Version != sessionObject.Version || sourceType == "conversation" && session.ID != id || sourceType == "agent_turn" && !provenanceVersionMatches(record.SourceVersion, turn.Version) {
			return pin, nil
		}
		if _, err = s.authorizeProject(ctx, tx, authorizationRequest(actor, p.ID, AuthorizationAgent, "")); err != nil {
			return pin, nil
		}
		pin.Kind = "AgentTurn"
		pin.ObjectID = turn.ID
		pin.ObjectVersion = turn.Version
		pin.SessionID = turn.SessionID
		pin.LocalHash = hashImpactValue(struct {
			Turn                          json.RawMessage
			SessionID, ProjectID, Creator string
		}{object.Payload, session.ID, session.ProjectID, session.CreatedBy})
		if sourceType == "agent_turn" {
			record.SourceVersion = strconv.FormatInt(turn.Version, 10)
		} else {
			record.SourceVersion = turn.ID
		}
		record.Locator = "reply"
		pin.OriginalAIHashes = []string{hashImpactValue(strings.TrimSpace(turn.Reply))}
		var suggestions any
		if json.Unmarshal(turn.Suggestions, &suggestions) == nil {
			provenanceAIStrings(suggestions, &pin.OriginalAIHashes)
		}
		for _, source := range turn.Context.SourceRefs {
			if source.SourceID == "" || source.AssetRevision < 1 || source.QuotedTextHash == "" {
				return unknownProvenancePin(p, actor, request), nil
			}
			pin.SourceIDs = append(pin.SourceIDs, source.SourceID)
			pin.Sources = append(pin.Sources, provenanceSourceVersion{ID: source.SourceID, AssetID: source.AssetID, Revision: source.AssetRevision, Hash: source.QuotedTextHash})
		}
	case "source_anchor", "sourceanchor", "anchor":
		// Typed version lookup is deliberately separate from the current source IO.
		anchor, stored, err := loadStoredObject[SourceAnchor](tx, p.ID, SourceAnchorKind, id)
		if errors.Is(err, ErrNotFound) {
			return pin, nil
		}
		if err != nil {
			return pin, err
		}
		port, ok := tx.(assetPolicyTransaction)
		if !ok {
			return pin, nil
		}
		current, err := port.IsAnchorCurrent(anchor)
		if err != nil || !current || anchor.ID != id || anchor.ProjectID != p.ID || anchor.Version != stored.Version || !provenanceVersionMatches(record.SourceVersion, anchor.Version) {
			return pin, nil
		}
		pin.Kind = "SourceAnchor"
		pin.ObjectID = id
		pin.ObjectVersion = anchor.Version
		pin.LocalHash = hashImpactValue(anchor)
		pin.SourceIDs = []string{anchor.SourceID}
		record.SourceType = "source_anchor"
		record.SourceVersion = strconv.FormatInt(anchor.Version, 10)
		record.Locator = anchor.Locator
	case "project_asset", "projectasset", "asset", "upload":
		port, ok := tx.(assetPolicyTransaction)
		if !ok {
			return pin, nil
		}
		truth, err := port.AssetTruth(p, id)
		if err == nil {
			if !provenanceVersionMatches(record.SourceVersion, truth.Asset.AssetRevision) || len(truth.SourceIDs) == 0 {
				return pin, nil
			}
			pin.Kind = "ProjectAsset"
			pin.ObjectID = id
			pin.ObjectVersion = truth.Asset.AssetRevision
			pin.SourceIDs = slices.Clone(truth.SourceIDs)
			pin.LocalHash = hashImpactValue(truth)
			record.SourceType = "project_asset"
			record.SourceVersion = strconv.FormatInt(pin.ObjectVersion, 10)
			break
		}
		if !errors.Is(err, ErrNotFound) {
			return pin, nil
		}
		fallthrough
	case "source", "chunk", "retrieval":
		db, err := TransactionDatabase(tx)
		if err != nil {
			return pin, nil
		}
		var chunk types.Chunk
		if err = db.Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, actor.TenantID).First(&chunk).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return pin, nil
		} else if err != nil {
			return pin, err
		}
		asset, err := evidence.NewBindings(db).AssetForKnowledge(ctx, p.ID, chunk.KnowledgeID)
		if err != nil {
			return pin, nil
		}
		if !provenanceVersionMatches(record.SourceVersion, int64(asset.AssetRevision)) {
			return pin, nil
		}
		pin.Kind = "Source"
		pin.ObjectID = id
		pin.ObjectVersion = int64(asset.AssetRevision)
		pin.SourceIDs = []string{id}
		pin.LocalHash = hashImpactValue(struct {
			ID, AssetID string
			Version     int
		}{id, asset.ID, asset.AssetRevision})
		record.SourceType = "source"
		record.SourceVersion = strconv.Itoa(asset.AssetRevision)
	default:
		return pin, nil
	}
	pin.SourceIDs = sortedImpactSourceIDs(pin.SourceIDs)
	digest, err := nativeSourceDigest(tx, p.ID, pin.SourceIDs)
	if err != nil {
		return unknownProvenancePin(p, actor, request), nil
	}
	pin.Record = record
	pin.SourceDigest = digest
	pin.Verified = true
	pin.ID = provenanceRecordKey(p.ID, request.Value, record)
	return pin, nil
}
func provenanceAIStrings(value any, hashes *[]string) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			*hashes = append(*hashes, hashImpactValue(strings.TrimSpace(typed)))
		}
	case map[string]any:
		for _, item := range typed {
			provenanceAIStrings(item, hashes)
		}
	case []any:
		for _, item := range typed {
			provenanceAIStrings(item, hashes)
		}
	}
}

func (s *Service) provenancePinCurrent(ctx context.Context, tx Transaction, actor Actor, p Project, pin provenancePin, forModel bool) bool {
	if !pin.Verified || pin.ProjectID != p.ID {
		return false
	}
	request := provenanceRequest{Record: &pin.Record, Value: ""}
	// Resolve exact typed descriptor without reusing its server pin.
	request.Record = new(ProvenanceRecord)
	*request.Record = pin.Record
	request.Record.CreatedAt = time.Time{}
	actual, err := s.resolveProvenancePin(ctx, tx, actor, p, request)
	if err != nil || !actual.Verified || actual.Kind != pin.Kind || actual.ObjectID != pin.ObjectID || actual.ObjectVersion != pin.ObjectVersion || actual.LocalHash != pin.LocalHash || actual.SourceDigest != pin.SourceDigest || !slices.Equal(actual.SourceIDs, pin.SourceIDs) {
		return false
	}
	return nativeSourceScope(ctx, tx, actor, p, pin.SourceIDs, forModel) == nil
}
func nativeSourceScope(ctx context.Context, tx Transaction, actor Actor, p Project, ids []string, forModel bool) error {
	if len(ids) == 0 {
		return nil
	}
	db, err := TransactionDatabase(tx)
	if err != nil {
		return ErrSourceUnavailable
	}
	for _, id := range ids {
		var chunk types.Chunk
		if err = db.Where("id=? AND tenant_id=? AND deleted_at IS NULL", id, actor.TenantID).First(&chunk).Error; err != nil {
			return ErrSourceUnavailable
		}
		var knowledge types.Knowledge
		if err = db.Where("id=? AND tenant_id=? AND deleted_at IS NULL", chunk.KnowledgeID, actor.TenantID).First(&knowledge).Error; err != nil {
			return ErrSourceUnavailable
		}
		var kb types.KnowledgeBase
		if err = db.Where("id=? AND tenant_id=? AND deleted_at IS NULL", knowledge.KnowledgeBaseID, actor.TenantID).First(&kb).Error; err != nil {
			return ErrSourceUnavailable
		}
		allowed, err := access.NewKBPermissions(provenanceSourceContext(ctx, actor), nil).Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
		if err != nil || !allowed {
			return ErrSourceUnavailable
		}
		asset, err := evidence.NewBindings(db).AssetForKnowledge(ctx, p.ID, knowledge.ID)
		if err != nil {
			return ErrSourceUnavailable
		}
		policy, _, _, err := ReadProjectAssetPolicy(tx, p, asset.ID)
		if err != nil {
			return err
		}
		if (policy.Scope != "project" && policy.Scope != "team" && (policy.Scope != "personal" || policy.CreatedBy != actor.UserID)) || len(policy.AllowedUserIDs) > 0 && !slices.Contains(policy.AllowedUserIDs, actor.UserID) || forModel && !policy.ModelUseAllowed {
			return ErrSourceUnavailable
		}
	}
	return nil
}
func provenanceFieldRequests(p Project) []provenanceRequest {
	keys := map[string]bool{}
	for key := range p.Spec {
		keys[key] = true
	}
	for key := range p.SpecFields {
		keys[key] = true
	}
	out := []provenanceRequest{}
	for key := range keys {
		field := p.SpecFields[key]
		value, found := p.Spec[key]
		if !found {
			value = field.Value
		}
		out = append(out, provenanceRequest{Key: key, Value: value, Origin: field.Origin, Creator: field.ModifiedBy, Record: field.Provenance})
	}
	slices.SortFunc(out, func(a, b provenanceRequest) int { return strings.Compare(a.Key, b.Key) })
	return out
}
func provenanceSnapshot(p Project, pins map[string]provenancePin) ProjectProvenanceSnapshot {
	result := ProjectProvenanceSnapshot{ProjectID: p.ID, SpecRevision: p.SpecRevision, ContextRevision: p.CurrentContextRevision, TemplateCopyVersion: p.TemplateCopyVersion, Fields: []SpecProvenanceRef{}}
	for _, request := range provenanceFieldRequests(p) {
		if _, found := p.Spec[request.Key]; !found {
			continue
		}
		ref := SpecProvenanceRef{FieldKey: request.Key, ValueHash: hashImpactValue(request.Value)}
		if request.Record != nil || request.Origin != "" && request.Origin != "human" {
			ref.PinID = pins[provenanceRequestKey(request.Record, request.Value)].ID
			if ref.PinID == "" {
				ref.PinID = "unverified"
			}
		}
		result.Fields = append(result.Fields, ref)
	}
	result.Digest = provenanceSnapshotDigest(result)
	return result
}
func redactProvenanceProject(p Project, proof *provenanceProof) Project {
	p.Spec = maps.Clone(p.Spec)
	p.SpecFields = maps.Clone(p.SpecFields)
	for key, field := range p.SpecFields {
		if field.Provenance == nil && (field.Origin == "" || field.Origin == "human") {
			continue
		}
		pin, found := proof.Requests[provenanceRequestKey(field.Provenance, field.Value)]
		record := ProvenanceRecord{CreatedBy: field.ModifiedBy, NeedsReview: true}
		if found {
			record = pin.Record
		}
		record.Accessible = found && proof.States[pin.ID]
		record.NeedsReview = !record.Accessible
		field.Provenance = &record
		if !record.Accessible {
			redactProvenanceRecord(field.Provenance, pin.Verified)
			field.Value = ""
			field.Status = "pending_confirmation"
			p.Spec[key] = ""
		}
		p.SpecFields[key] = field
	}
	return p
}
func redactProvenanceRecord(record *ProvenanceRecord, known bool) {
	record.Locator = ""
	record.Purpose = ""
	record.AuthorizationScope = ""
	if !known {
		record.SourceType = "unverified"
		record.CreatedAt = time.Time{}
		if !runtimeSafeCode.MatchString(record.SourceID) {
			record.SourceID = ""
		}
		if !runtimeSafeCode.MatchString(record.SourceVersion) {
			record.SourceVersion = ""
		}
	}
}
func provenanceStorageSnapshot(tx Transaction, p Project) (ProjectProvenanceSnapshot, error) {
	pins := map[string]provenancePin{}
	for _, request := range provenanceFieldRequests(p) {
		if request.Record == nil {
			continue
		}
		pin, found, err := loadProvenancePin(tx, p.ID, request.Value, request.Record)
		if err != nil {
			return ProjectProvenanceSnapshot{}, err
		}
		if found {
			pins[provenanceRequestKey(request.Record, request.Value)] = pin
		}
	}
	return provenanceSnapshot(p, pins), nil
}

func ValidateFrozenProvenanceInput(spec map[string]string, snapshot ProjectProvenanceSnapshot) error {
	if snapshot.Digest == "" || snapshot.ProjectID == "" || provenanceSnapshotDigest(snapshot) != snapshot.Digest {
		return ErrSourceUnavailable
	}
	seen := map[string]bool{}
	for _, ref := range snapshot.Fields {
		value, found := spec[ref.FieldKey]
		if seen[ref.FieldKey] || !found || hashImpactValue(value) != ref.ValueHash {
			return ErrSourceUnavailable
		}
		seen[ref.FieldKey] = true
	}
	return nil
}
func ProjectProvenanceSnapshotFromContext(ctx context.Context, pid string) (ProjectProvenanceSnapshot, bool) {
	proof, ok := ctx.Value(provenanceProofKey{}).(*provenanceProof)
	if !ok || proof.Frozen || proof.Project.ID != pid {
		return ProjectProvenanceSnapshot{}, false
	}
	snapshot := proof.Snapshot
	snapshot.Fields = slices.Clone(snapshot.Fields)
	return snapshot, true
}
func ProvenanceSourceIDs(tx Transaction, p Project) ([]string, error) {
	ids := []string{}
	for _, request := range provenanceFieldRequests(p) {
		if request.Record == nil && (request.Origin == "" || request.Origin == "human") {
			continue
		}
		pin, found, err := loadProvenancePin(tx, p.ID, request.Value, request.Record)
		if err != nil {
			return nil, err
		}
		if !found || !pin.Verified {
			return nil, ErrSourceUnavailable
		}
		ids = append(ids, pin.SourceIDs...)
	}
	return sortedImpactSourceIDs(ids), nil
}
