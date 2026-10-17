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
type g1SourceVersion struct {
	ID, AssetID, Hash string
	Revision          int
}
type g1ProvenancePin struct {
	ID, ProjectID, ValueHash, Kind, ObjectID, SessionID, LocalHash, SourceDigest string
	ObjectVersion                                                                int64
	Record                                                                       ProvenanceRecord
	Sources                                                                      []g1SourceVersion
	SourceIDs                                                                    []string
	OriginalAIHashes                                                             []string
	Verified                                                                     bool
}
type g1ProvenanceRequest struct {
	Key, Value, Origin, Creator string
	Record                      *ProvenanceRecord
	Fresh                       bool
}
type g1ProvenanceProofKey struct{}
type g1SourceIOKey struct{}
type g1ProvenanceProof struct {
	Project                  Project
	ActorID                  string
	TenantID                 uint64
	Identity                 string
	ForModel                 bool
	Snapshot                 ProjectProvenanceSnapshot
	Pins                     map[string]g1ProvenancePin
	States                   map[string]bool
	Requests                 map[string]g1ProvenancePin
	FreshPins                map[string]g1ProvenancePin
	NativeBefore             map[string]string
	Operation, OperationBody string
	Frozen                   bool
}

func g1RecordKey(pid, value string, record ProvenanceRecord) string {
	record.Accessible = false
	record.NeedsReview = false
	return "g1." + hashImpactValue(struct {
		Project, Value string
		Record         ProvenanceRecord
	}{pid, hashImpactValue(value), record})
}
func g1RequestKey(record *ProvenanceRecord, value string) string {
	return hashImpactValue(struct {
		Record *ProvenanceRecord
		Value  string
	}{record, value})
}
func g1SnapshotDigest(snapshot ProjectProvenanceSnapshot) string {
	snapshot.Digest = ""
	return hashImpactValue(snapshot)
}
func g1ProjectHash(p Project) string {
	return hashImpactValue(struct {
		ID                           string
		Version, Spec, Context, Copy int64
		Values                       map[string]string
		Fields                       map[string]SpecField
	}{p.ID, p.ProjectVersion, p.SpecRevision, p.CurrentContextRevision, p.TemplateCopyVersion, p.Spec, p.SpecFields})
}
func g1IdentityMatches(ctx context.Context, actor Actor) bool {
	caller := types.CallerFromContext(ctx)
	if caller.UserID != "" && (caller.UserID != actor.UserID || caller.TenantID != actor.TenantID) {
		return false
	}
	tenant, present := types.TenantIDFromContext(ctx)
	return !present || tenant == actor.TenantID
}
func g1SourceContext(ctx context.Context, actor Actor) context.Context {
	if types.CallerFromContext(ctx).UserID == "" {
		ctx = types.WithCaller(ctx, types.Caller{TenantID: actor.TenantID, UserID: actor.UserID, Role: actor.Role})
	}
	return ctx
}
func g1LoadPin(tx Transaction, pid, value string, record *ProvenanceRecord) (g1ProvenancePin, bool, error) {
	if record == nil {
		return g1ProvenancePin{}, false, nil
	}
	store, ok := tx.(g5Transaction)
	if !ok {
		return g1ProvenancePin{}, false, nil
	}
	id := g1RecordKey(pid, value, *record)
	var pin g1ProvenancePin
	_, err := store.G5Get(pid, "g1_provenance", id, &pin)
	if errors.Is(err, ErrNotFound) {
		return pin, false, nil
	}
	if err != nil {
		return pin, false, err
	}
	if pin.ID != id || pin.ProjectID != pid || pin.ValueHash != hashImpactValue(value) || g1RecordKey(pid, value, pin.Record) != id {
		return pin, false, ErrSourceUnavailable
	}
	return pin, true, nil
}
func g1UnknownPin(p Project, actor Actor, request g1ProvenanceRequest) g1ProvenancePin {
	record := ProvenanceRecord{}
	if request.Record != nil {
		record = *request.Record
	}
	record.CreatedBy = actor.UserID
	record.CreatedAt = time.Now().UTC()
	record.Accessible = false
	record.NeedsReview = true
	record.AuthorizationScope = "project/" + p.ID
	pin := g1ProvenancePin{ProjectID: p.ID, ValueHash: hashImpactValue(request.Value), Record: record, SourceIDs: []string{}, Sources: []g1SourceVersion{}, OriginalAIHashes: []string{}}
	pin.ID = g1RecordKey(p.ID, request.Value, record)
	return pin
}
func g1VersionMatches(value string, version int64) bool {
	return value == "" || value == strconv.FormatInt(version, 10)
}
func g1NativeSourceDigest(tx Transaction, pid string, ids []string) (string, error) {
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
func (s *Service) g1ResolvePin(ctx context.Context, tx Transaction, actor Actor, p Project, request g1ProvenanceRequest) (g1ProvenancePin, error) {
	pin := g1UnknownPin(p, actor, request)
	if request.Record == nil {
		return pin, nil
	}
	if previous, found, err := g1LoadPin(tx, p.ID, request.Value, request.Record); err != nil {
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
	if (sourceType == "source" || sourceType == "upload") && s.g2Runtime.Sources == nil {
		return pin, nil
	}
	switch sourceType {
	case "agent_turn", "conversation":
		store, ok := tx.(G2Transaction)
		if !ok {
			return pin, nil
		}
		turnID := id
		if sourceType == "conversation" {
			turnID = record.SourceVersion
		}
		object, err := store.G2Object(p.ID, G2TurnKind, turnID)
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
		sessionObject, err := store.G2Object(p.ID, G2SessionKind, turn.SessionID)
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
		if turn.ID != turnID || turn.ProjectID != p.ID || turn.Version != object.Version || turn.Status != "completed" || turn.CreatedBy != actor.UserID || session.ProjectID != p.ID || session.ID != turn.SessionID || session.CreatedBy != actor.UserID || session.Version != sessionObject.Version || sourceType == "conversation" && session.ID != id || sourceType == "agent_turn" && !g1VersionMatches(record.SourceVersion, turn.Version) {
			return pin, nil
		}
		if _, err = s.authorizeProject(ctx, tx, actor, p.ID, "agent"); err != nil {
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
			g1AIStrings(suggestions, &pin.OriginalAIHashes)
		}
		for _, source := range turn.Context.SourceRefs {
			if source.SourceID == "" || source.AssetRevision < 1 || source.QuotedTextHash == "" {
				return g1UnknownPin(p, actor, request), nil
			}
			pin.SourceIDs = append(pin.SourceIDs, source.SourceID)
			pin.Sources = append(pin.Sources, g1SourceVersion{ID: source.SourceID, AssetID: source.AssetID, Revision: source.AssetRevision, Hash: source.QuotedTextHash})
		}
	case "source_anchor", "sourceanchor", "anchor":
		// Typed version lookup is deliberately separate from the current source IO.
		anchor, stored, err := g2Load[SourceAnchor](tx, p.ID, G2AnchorKind, id)
		if errors.Is(err, ErrNotFound) {
			return pin, nil
		}
		if err != nil {
			return pin, err
		}
		port, ok := tx.(g2AssetPolicyTransaction)
		if !ok {
			return pin, nil
		}
		current, err := port.G2AnchorCurrent(anchor)
		if err != nil || !current || anchor.ID != id || anchor.ProjectID != p.ID || anchor.Version != stored.Version || !g1VersionMatches(record.SourceVersion, anchor.Version) {
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
		port, ok := tx.(g2AssetPolicyTransaction)
		if !ok {
			return pin, nil
		}
		truth, err := port.G2AssetTruth(p, id)
		if err == nil {
			if !g1VersionMatches(record.SourceVersion, truth.Asset.AssetRevision) || len(truth.SourceIDs) == 0 {
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
		if !g1VersionMatches(record.SourceVersion, int64(asset.AssetRevision)) {
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
	digest, err := g1NativeSourceDigest(tx, p.ID, pin.SourceIDs)
	if err != nil {
		return g1UnknownPin(p, actor, request), nil
	}
	pin.Record = record
	pin.SourceDigest = digest
	pin.Verified = true
	pin.ID = g1RecordKey(p.ID, request.Value, record)
	return pin, nil
}
func g1AIStrings(value any, hashes *[]string) {
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) != "" {
			*hashes = append(*hashes, hashImpactValue(strings.TrimSpace(typed)))
		}
	case map[string]any:
		for _, item := range typed {
			g1AIStrings(item, hashes)
		}
	case []any:
		for _, item := range typed {
			g1AIStrings(item, hashes)
		}
	}
}

func (s *Service) g1PinCurrent(ctx context.Context, tx Transaction, actor Actor, p Project, pin g1ProvenancePin, forModel bool) bool {
	if !pin.Verified || pin.ProjectID != p.ID {
		return false
	}
	request := g1ProvenanceRequest{Record: &pin.Record, Value: ""}
	// Resolve exact typed descriptor without reusing its server pin.
	request.Record = new(ProvenanceRecord)
	*request.Record = pin.Record
	request.Record.CreatedAt = time.Time{}
	actual, err := s.g1ResolvePin(ctx, tx, actor, p, request)
	if err != nil || !actual.Verified || actual.Kind != pin.Kind || actual.ObjectID != pin.ObjectID || actual.ObjectVersion != pin.ObjectVersion || actual.LocalHash != pin.LocalHash || actual.SourceDigest != pin.SourceDigest || !slices.Equal(actual.SourceIDs, pin.SourceIDs) {
		return false
	}
	return g1NativeSourceScope(ctx, tx, actor, p, pin.SourceIDs, forModel) == nil
}
func g1NativeSourceScope(ctx context.Context, tx Transaction, actor Actor, p Project, ids []string, forModel bool) error {
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
		allowed, err := access.NewKBPermissions(g1SourceContext(ctx, actor), nil).Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
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
func g1FieldRequests(p Project) []g1ProvenanceRequest {
	keys := map[string]bool{}
	for key := range p.Spec {
		keys[key] = true
	}
	for key := range p.SpecFields {
		keys[key] = true
	}
	out := []g1ProvenanceRequest{}
	for key := range keys {
		field := p.SpecFields[key]
		value, found := p.Spec[key]
		if !found {
			value = field.Value
		}
		out = append(out, g1ProvenanceRequest{Key: key, Value: value, Origin: field.Origin, Creator: field.ModifiedBy, Record: field.Provenance})
	}
	slices.SortFunc(out, func(a, b g1ProvenanceRequest) int { return strings.Compare(a.Key, b.Key) })
	return out
}
func g1Snapshot(p Project, pins map[string]g1ProvenancePin) ProjectProvenanceSnapshot {
	result := ProjectProvenanceSnapshot{ProjectID: p.ID, SpecRevision: p.SpecRevision, ContextRevision: p.CurrentContextRevision, TemplateCopyVersion: p.TemplateCopyVersion, Fields: []SpecProvenanceRef{}}
	for _, request := range g1FieldRequests(p) {
		if _, found := p.Spec[request.Key]; !found {
			continue
		}
		ref := SpecProvenanceRef{FieldKey: request.Key, ValueHash: hashImpactValue(request.Value)}
		if request.Record != nil || request.Origin != "" && request.Origin != "human" {
			ref.PinID = pins[g1RequestKey(request.Record, request.Value)].ID
			if ref.PinID == "" {
				ref.PinID = "unverified"
			}
		}
		result.Fields = append(result.Fields, ref)
	}
	result.Digest = g1SnapshotDigest(result)
	return result
}
func g1RedactProject(p Project, proof *g1ProvenanceProof) Project {
	p.Spec = maps.Clone(p.Spec)
	p.SpecFields = maps.Clone(p.SpecFields)
	for key, field := range p.SpecFields {
		if field.Provenance == nil && (field.Origin == "" || field.Origin == "human") {
			continue
		}
		pin, found := proof.Requests[g1RequestKey(field.Provenance, field.Value)]
		record := ProvenanceRecord{CreatedBy: field.ModifiedBy, NeedsReview: true}
		if found {
			record = pin.Record
		}
		record.Accessible = found && proof.States[pin.ID]
		record.NeedsReview = !record.Accessible
		field.Provenance = &record
		if !record.Accessible {
			g1RedactRecord(field.Provenance, pin.Verified)
			field.Value = ""
			field.Status = "pending_confirmation"
			p.Spec[key] = ""
		}
		p.SpecFields[key] = field
	}
	return p
}
func g1RedactRecord(record *ProvenanceRecord, known bool) {
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
func g1StorageSnapshot(tx Transaction, p Project) (ProjectProvenanceSnapshot, error) {
	pins := map[string]g1ProvenancePin{}
	for _, request := range g1FieldRequests(p) {
		if request.Record == nil {
			continue
		}
		pin, found, err := g1LoadPin(tx, p.ID, request.Value, request.Record)
		if err != nil {
			return ProjectProvenanceSnapshot{}, err
		}
		if found {
			pins[g1RequestKey(request.Record, request.Value)] = pin
		}
	}
	return g1Snapshot(p, pins), nil
}

func ValidateFrozenProvenanceInput(spec map[string]string, snapshot ProjectProvenanceSnapshot) error {
	if snapshot.Digest == "" || snapshot.ProjectID == "" || g1SnapshotDigest(snapshot) != snapshot.Digest {
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
	proof, ok := ctx.Value(g1ProvenanceProofKey{}).(*g1ProvenanceProof)
	if !ok || proof.Frozen || proof.Project.ID != pid {
		return ProjectProvenanceSnapshot{}, false
	}
	snapshot := proof.Snapshot
	snapshot.Fields = slices.Clone(snapshot.Fields)
	return snapshot, true
}
func ProvenanceSourceIDs(tx Transaction, p Project) ([]string, error) {
	ids := []string{}
	for _, request := range g1FieldRequests(p) {
		if request.Record == nil && (request.Origin == "" || request.Origin == "human") {
			continue
		}
		pin, found, err := g1LoadPin(tx, p.ID, request.Value, request.Record)
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
