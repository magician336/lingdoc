package workspacecore

import "time"

// The versioned route-roadmap editing session (G3 gap closure #250).
//
// One deep module owns "how the roadmap is written and how its versions are
// counted", so a parallel RouteNode write endpoint cannot grow a second,
// diverging version semantic. The module separates two things that used to be
// the same thing:
//
//   - the FROZEN roadmap: the current route node / route edge versions that
//     every reader (including the export input builder) is allowed to see;
//   - the UNFROZEN editing session: proposals that live only inside a session
//     record until they are committed.
//
// An unfrozen proposal never reaches the frozen tables. That is what makes
// "the export only reads frozen versions" a structural property instead of a
// filter that can be forgotten.

// Roadmap session states.
const (
	// RoadmapEditSessionOpen is an unfrozen editing session. At most one may
	// exist per project; the database enforces that with a partial unique
	// index, so two editors cannot both claim the roadmap.
	RoadmapEditSessionOpen = "open"
	// RoadmapEditSessionCommitted is a session whose proposals were frozen.
	RoadmapEditSessionCommitted = "committed"
	// RoadmapEditSessionCancelled is a session whose proposals were discarded.
	// The frozen roadmap it was opened against is still the current one.
	RoadmapEditSessionCancelled = "cancelled"
)

// Roadmap node proposal kinds.
const (
	RoadmapNodeProposalAdd    = "add"
	RoadmapNodeProposalUpdate = "update"
	RoadmapNodeProposalDelete = "delete"
)

// Roadmap edge proposal kinds.
const (
	RoadmapEdgeProposalAdd    = "add"
	RoadmapEdgeProposalUpdate = "update"
	RoadmapEdgeProposalDelete = "delete"
)

// RouteEdge is the first-class relation between two route nodes. It carries
// the minimum field set the acceptance criteria need: source node, target
// node, relation type and version. It is versioned the same way route nodes
// are - one row per version, the newest version being the current edge.
type RouteEdge struct {
	ID        string `json:"id"`
	ProjectID string `json:"project_id"`
	Version   int64  `json:"version"`
	SourceID  string `json:"source_id"`
	TargetID  string `json:"target_id"`
	Relation  string `json:"relation"`
	// Status is "active" or "retired". The minimal field set of the
	// acceptance criteria is source, target, relation and version; the status
	// is the one addition that makes "withdraw this relation" expressible
	// without destroying the version history.
	Status          string    `json:"status"`
	ContextRevision int64     `json:"context_revision"`
	ActorID         string    `json:"actor_id"`
	Reason          string    `json:"reason"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// Route edge statuses.
const (
	RouteEdgeStatusActive  = "active"
	RouteEdgeStatusRetired = "retired"
)

// Roadmap node states. The stored node status says what a human declared; the
// state says what the frozen roadmap currently is, which is what the
// required-route check and the acceptance console need to read.//
//   - current       : ready, and every pinned input still points at the
//     current version of that input.
//   - pending_review: the node exists but has not been reviewed (its stored
//     status is not "ready").
//   - stale         : the node is ready, but one of the versions it pinned has
//     moved on - the node is behind ("版本落后").
//   - missing       : the template rules declare the node as required, but no
//     route node with that identity exists.
//   - retired       : the node was retired and is no longer part of the route.
const (
	RoadmapNodeStateCurrent       = "current"
	RoadmapNodeStatePendingReview = "pending_review"
	RoadmapNodeStateStale         = "stale"
	RoadmapNodeStateMissing       = "missing"
	RoadmapNodeStateRetired       = "retired"
)

// RoadmapNodeStatuses lists the stored route node statuses.
//
// "pending_review" is the explicit 待复核 status: the node exists and has been
// declared, but no human has reviewed it yet, so it cannot satisfy a required
// route rule. "planned" keeps its original meaning (not started),
// "ready" means reviewed and current, "blocked" means the node cannot proceed
// and "retired" means it left the route.
var RoadmapNodeStatuses = []string{"planned", "pending_review", "ready", "blocked", "retired"}

// RoadmapNodeView is the read model of one route node in the frozen roadmap.
// It adds the derived state and the template-declared required flag to the
// stored node, because both are properties of the roadmap, not of the row.
type RoadmapNodeView struct {
	ID              string             `json:"id"`
	ProjectID       string             `json:"project_id"`
	Version         int64              `json:"version"`
	Name            string             `json:"name"`
	Inputs          []ObjectVersionRef `json:"inputs"`
	Targets         []ObjectVersionRef `json:"targets"`
	Required        bool               `json:"required"`
	Status          string             `json:"status"`
	State           string             `json:"state"`
	StateReason     string             `json:"state_reason,omitempty"`
	ContextRevision int64              `json:"context_revision"`
	ActorID         string             `json:"actor_id"`
	Reason          string             `json:"reason"`
	UpdatedAt       time.Time          `json:"updated_at"`
	// Frozen is true for every node version that reached the frozen roadmap.
	// An unfrozen proposal is never published as a node view.
	Frozen bool `json:"frozen"`
}

// RoadmapBaseline is the frozen roadmap a session was opened against. It is
// the state the roadmap returns to when the session is cancelled.
type RoadmapBaseline struct {
	// Version counts the committed editing sessions of this project. It is
	// monotonic, and it is the version a session binds itself to.
	Version         int64             `json:"version"`
	ContextRevision int64             `json:"context_revision"`
	Nodes           []RoadmapNodeView `json:"nodes"`
	Edges           []RouteEdge       `json:"edges"`
}

// RoadmapNodeProposal is one proposed route node change inside a session.
//
// TemporaryVersion is the version the proposal would occupy once committed.
// It is a session-scoped number: it never reaches the frozen roadmap, and the
// cancellation path has to make sure no impact task or closure evidence keeps
// pointing at it afterwards.
type RoadmapNodeProposal struct {
	NodeID           string             `json:"node_id"`
	Kind             string             `json:"kind"`
	Name             string             `json:"name"`
	Status           string             `json:"status"`
	Inputs           []ObjectVersionRef `json:"inputs"`
	Targets          []ObjectVersionRef `json:"targets"`
	Reason           string             `json:"reason"`
	TemporaryVersion int64              `json:"temporary_version"`
}

// RoadmapEdgeProposal is one proposed route edge change inside a session.
type RoadmapEdgeProposal struct {
	EdgeID           string `json:"edge_id"`
	Kind             string `json:"kind"`
	SourceID         string `json:"source_id"`
	TargetID         string `json:"target_id"`
	Relation         string `json:"relation"`
	Reason           string `json:"reason"`
	TemporaryVersion int64  `json:"temporary_version"`
}

// RoadmapCommitResult is what a commit produced.
type RoadmapCommitResult struct {
	SessionID string `json:"session_id"`
	// BaselineVersion is the roadmap version the session was opened against.
	BaselineVersion int64 `json:"baseline_version"`
	// CommittedVersion is the roadmap version after the commit. It stays at
	// the baseline when the commit produced a ChangeSet instead of frozen
	// node versions, because in that case nothing was written to the roadmap.
	CommittedVersion int64 `json:"committed_version"`
	// AppliedNodes lists the new frozen route node versions.
	AppliedNodes []RoadmapNodeView `json:"applied_nodes"`
	// AppliedEdges lists the new frozen route edge versions.
	AppliedEdges []RouteEdge `json:"applied_edges"`
	// ChangeSetID is set when the commit produced a ChangeSet instead of
	// writing the roadmap, which is the path semantic changes take.
	ChangeSetID string `json:"change_set_id,omitempty"`
	// Semantic is true when the commit produced a ChangeSet.
	Semantic bool `json:"semantic"`
}

// RoadmapEditSession is one versioned editing session.
type RoadmapEditSession struct {
	ID            string                `json:"id"`
	ProjectID     string                `json:"project_id"`
	Status        string                `json:"status"`
	Baseline      RoadmapBaseline       `json:"baseline"`
	NodeProposals []RoadmapNodeProposal `json:"node_proposals"`
	EdgeProposals []RoadmapEdgeProposal `json:"edge_proposals"`
	Result        *RoadmapCommitResult  `json:"result,omitempty"`
	ActorID       string                `json:"actor_id"`
	Reason        string                `json:"reason"`
	CreatedAt     time.Time             `json:"created_at"`
	UpdatedAt     time.Time             `json:"updated_at"`
}

// RoadmapEditBeginInput opens an unfrozen editing session.
type RoadmapEditBeginInput struct {
	ExpectedContextRevision int64  `json:"expected_context_revision"`
	Reason                  string `json:"reason"`
}

// RoadmapNodeProposalInput proposes one route node change.
//
// Required is a pointer so an absent value is distinguishable from an
// explicit false. The required flag is declared by the template rules, never
// by the client, so an explicit value that disagrees with the rules is
// rejected instead of silently ignored.
type RoadmapNodeProposalInput struct {
	NodeID   string             `json:"node_id"`
	Kind     string             `json:"kind"`
	Name     string             `json:"name"`
	Status   string             `json:"status"`
	Inputs   []ObjectVersionRef `json:"inputs"`
	Targets  []ObjectVersionRef `json:"targets"`
	Required *bool              `json:"required,omitempty"`
	Reason   string             `json:"reason"`
}

// RoadmapEdgeProposalInput proposes one route edge change.
type RoadmapEdgeProposalInput struct {
	EdgeID   string `json:"edge_id"`
	Kind     string `json:"kind"`
	SourceID string `json:"source_id"`
	TargetID string `json:"target_id"`
	Relation string `json:"relation"`
	Reason   string `json:"reason"`
}

// RoadmapCommitInput commits an editing session.
type RoadmapCommitInput struct {
	ExpectedBaselineVersion int64  `json:"expected_baseline_version"`
	Reason                  string `json:"reason"`
}

// RoadmapCancelInput cancels an editing session.
type RoadmapCancelInput struct {
	ExpectedBaselineVersion int64  `json:"expected_baseline_version"`
	Reason                  string `json:"reason"`
}

// RoadmapVersionEntry is one entry of a route node's version sequence.
type RoadmapVersionEntry struct {
	Version         int64              `json:"version"`
	Name            string             `json:"name"`
	Status          string             `json:"status"`
	Required        bool               `json:"required"`
	State           string             `json:"state"`
	StateReason     string             `json:"state_reason,omitempty"`
	ContextRevision int64              `json:"context_revision"`
	ActorID         string             `json:"actor_id"`
	Reason          string             `json:"reason"`
	Inputs          []ObjectVersionRef `json:"inputs"`
	Targets         []ObjectVersionRef `json:"targets"`
	UpdatedAt       time.Time          `json:"updated_at"`
}

// RoadmapFieldChange is one field-level difference between two versions.
type RoadmapFieldChange struct {
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// RoadmapComparison is the before/after comparison of two versions.
type RoadmapComparison struct {
	Kind    string               `json:"kind"`
	ID      string               `json:"id"`
	From    int64                `json:"from"`
	To      int64                `json:"to"`
	Changes []RoadmapFieldChange `json:"changes"`
}
