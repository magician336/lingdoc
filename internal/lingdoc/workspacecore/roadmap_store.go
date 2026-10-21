package workspacecore

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Persistence for the versioned route-roadmap editing session (#250).
//
// Two shapes live here:
//
//   - the session row, which owns the unfrozen editing session. It is written
//     while the session is open and closed exactly once (committed or
//     cancelled).
//   - the route edge rows, one per version, mirroring how route node versions
//     are keyed in lingdoc_g3_records ("<id>/<version>").
//
// The route NODE versions deliberately stay in the shared lingdoc_g3_records
// seam, because the ChangeSet semantic-delta path already writes them. Keeping
// one write path for node versions is the whole point of the module: a
// parallel writer would fork the version semantic.

type roadmapEditSessionRow struct {
	ID                string    `gorm:"primaryKey;size:36"`
	ProjectID         string    `gorm:"not null;size:64;index"`
	Status            string    `gorm:"not null;size:16"`
	BaselineJSON      string    `gorm:"column:baseline_json;type:text;not null"`
	NodeProposalsJSON string    `gorm:"column:node_proposals_json;type:text;not null"`
	EdgeProposalsJSON string    `gorm:"column:edge_proposals_json;type:text;not null"`
	ResultJSON        string    `gorm:"column:result_json;type:text;not null"`
	ActorID           string    `gorm:"not null;size:64"`
	Reason            string    `gorm:"type:text;not null"`
	CreatedAt         time.Time `gorm:"not null"`
	UpdatedAt         time.Time `gorm:"not null"`
}

func (roadmapEditSessionRow) TableName() string { return "lingdoc_g3_roadmap_edit_sessions" }

type routeEdgeRow struct {
	ID              string    `gorm:"primaryKey;size:191"`
	ProjectID       string    `gorm:"not null;size:64;index"`
	EdgeID          string    `gorm:"not null;size:64;index"`
	Version         int64     `gorm:"not null"`
	SourceID        string    `gorm:"not null;size:64"`
	TargetID        string    `gorm:"not null;size:64"`
	Relation        string    `gorm:"not null;size:64"`
	ContextRevision int64     `gorm:"not null"`
	ActorID         string    `gorm:"not null;size:64"`
	Reason          string    `gorm:"type:text;not null"`
	CreatedAt       time.Time `gorm:"not null"`
}

func (routeEdgeRow) TableName() string { return "lingdoc_g3_route_edges" }

// roadmapStore is the persistence port of the roadmap module. It is declared
// here, separately from the shared transaction interfaces, so that adding the
// module does not widen interfaces other features assert on.
type roadmapStore interface {
	InsertRoadmapSession(RoadmapEditSession) error
	RoadmapSession(string, string) (RoadmapEditSession, error)
	RoadmapSessions(string) ([]RoadmapEditSession, error)
	UpdateRoadmapSession(RoadmapEditSession, RoadmapEditSession) error
	InsertRouteEdge(RouteEdge) error
	RouteEdge(string, string) (RouteEdge, error)
	RouteEdges(string) ([]RouteEdge, error)
	RouteEdgeVersions(string, string) ([]RouteEdge, error)
	RouteNodeVersions(string, string) ([]RouteNode, error)
}

func encodeRoadmapSession(session RoadmapEditSession) (roadmapEditSessionRow, error) {
	baseline, err := json.Marshal(session.Baseline)
	if err != nil {
		return roadmapEditSessionRow{}, err
	}
	nodes, err := json.Marshal(session.NodeProposals)
	if err != nil {
		return roadmapEditSessionRow{}, err
	}
	edges, err := json.Marshal(session.EdgeProposals)
	if err != nil {
		return roadmapEditSessionRow{}, err
	}
	result := ""
	if session.Result != nil {
		raw, err := json.Marshal(session.Result)
		if err != nil {
			return roadmapEditSessionRow{}, err
		}
		result = string(raw)
	}
	return roadmapEditSessionRow{
		ID:                session.ID,
		ProjectID:         session.ProjectID,
		Status:            session.Status,
		BaselineJSON:      string(baseline),
		NodeProposalsJSON: string(nodes),
		EdgeProposalsJSON: string(edges),
		ResultJSON:        result,
		ActorID:           session.ActorID,
		Reason:            session.Reason,
		CreatedAt:         session.CreatedAt,
		UpdatedAt:         session.UpdatedAt,
	}, nil
}

func decodeRoadmapSession(row roadmapEditSessionRow) (RoadmapEditSession, error) {
	session := RoadmapEditSession{
		ID:        row.ID,
		ProjectID: row.ProjectID,
		Status:    row.Status,
		ActorID:   row.ActorID,
		Reason:    row.Reason,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
	if err := json.Unmarshal([]byte(row.BaselineJSON), &session.Baseline); err != nil {
		return RoadmapEditSession{}, err
	}
	if err := json.Unmarshal([]byte(row.NodeProposalsJSON), &session.NodeProposals); err != nil {
		return RoadmapEditSession{}, err
	}
	if err := json.Unmarshal([]byte(row.EdgeProposalsJSON), &session.EdgeProposals); err != nil {
		return RoadmapEditSession{}, err
	}
	if row.ResultJSON != "" {
		var result RoadmapCommitResult
		if err := json.Unmarshal([]byte(row.ResultJSON), &result); err != nil {
			return RoadmapEditSession{}, err
		}
		session.Result = &result
	}
	return session, nil
}

func (t gormTransaction) InsertRoadmapSession(session RoadmapEditSession) error {
	row, err := encodeRoadmapSession(session)
	if err != nil {
		return err
	}
	return t.db.Create(&row).Error
}

func (t gormTransaction) RoadmapSession(project, id string) (RoadmapEditSession, error) {
	var row roadmapEditSessionRow
	if err := t.db.Where("project_id = ? AND id = ?", project, id).First(&row).Error; err != nil {
		return RoadmapEditSession{}, storageError(err)
	}
	return decodeRoadmapSession(row)
}

func (t gormTransaction) RoadmapSessions(project string) ([]RoadmapEditSession, error) {
	var rows []roadmapEditSessionRow
	if err := t.db.Where("project_id = ?", project).Order("created_at, id").Find(&rows).Error; err != nil {
		return nil, err
	}
	sessions := []RoadmapEditSession{}
	for _, row := range rows {
		session, err := decodeRoadmapSession(row)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	return sessions, nil
}

func (t gormTransaction) UpdateRoadmapSession(previous, next RoadmapEditSession) error {
	row, err := encodeRoadmapSession(next)
	if err != nil {
		return err
	}
	row.CreatedAt = previous.CreatedAt
	return t.db.Model(&roadmapEditSessionRow{}).Where("id = ? AND project_id = ? AND status = ?", previous.ID, previous.ProjectID, previous.Status).
		Updates(map[string]any{
			"status":              row.Status,
			"baseline_json":       row.BaselineJSON,
			"node_proposals_json": row.NodeProposalsJSON,
			"edge_proposals_json": row.EdgeProposalsJSON,
			"result_json":         row.ResultJSON,
			"actor_id":            row.ActorID,
			"reason":              row.Reason,
			"updated_at":          row.UpdatedAt,
		}).Error
}

func (t gormTransaction) InsertRouteEdge(edge RouteEdge) error {
	row := routeEdgeRow{
		ID:              edge.ID + "/" + strconv.FormatInt(edge.Version, 10),
		ProjectID:       edge.ProjectID,
		EdgeID:          edge.ID,
		Version:         edge.Version,
		SourceID:        edge.SourceID,
		TargetID:        edge.TargetID,
		Relation:        edge.Relation,
		ContextRevision: edge.ContextRevision,
		ActorID:         edge.ActorID,
		Reason:          edge.Reason,
		CreatedAt:       edge.UpdatedAt,
	}
	return t.db.Create(&row).Error
}

func routeEdgeFromRow(row routeEdgeRow) RouteEdge {
	return RouteEdge{
		ID:              row.EdgeID,
		ProjectID:       row.ProjectID,
		Version:         row.Version,
		SourceID:        row.SourceID,
		TargetID:        row.TargetID,
		Relation:        row.Relation,
		ContextRevision: row.ContextRevision,
		ActorID:         row.ActorID,
		Reason:          row.Reason,
		UpdatedAt:       row.CreatedAt,
	}
}

// RouteEdgeVersions returns every version of one edge, oldest first.
func (t gormTransaction) RouteEdgeVersions(project, edgeID string) ([]RouteEdge, error) {
	var rows []routeEdgeRow
	if err := t.db.Where("project_id = ? AND edge_id = ?", project, edgeID).Order("version").Find(&rows).Error; err != nil {
		return nil, err
	}
	edges := []RouteEdge{}
	for _, row := range rows {
		edges = append(edges, routeEdgeFromRow(row))
	}
	return edges, nil
}

// RouteEdge returns the current version of one edge.
func (t gormTransaction) RouteEdge(project, edgeID string) (RouteEdge, error) {
	var row routeEdgeRow
	if err := t.db.Where("project_id = ? AND edge_id = ?", project, edgeID).Order("version DESC").First(&row).Error; err != nil {
		return RouteEdge{}, storageError(err)
	}
	return routeEdgeFromRow(row), nil
}

// RouteEdges returns the current version of every edge of the project.
func (t gormTransaction) RouteEdges(project string) ([]RouteEdge, error) {
	var rows []routeEdgeRow
	if err := t.db.Where("project_id = ?", project).Order("edge_id, version").Find(&rows).Error; err != nil {
		return nil, err
	}
	current := map[string]RouteEdge{}
	for _, row := range rows {
		edge := routeEdgeFromRow(row)
		if existing, ok := current[edge.ID]; !ok || edge.Version > existing.Version {
			current[edge.ID] = edge
		}
	}
	edges := []RouteEdge{}
	for _, edge := range current {
		edges = append(edges, edge)
	}
	sort.Slice(edges, func(i, j int) bool { return edges[i].ID < edges[j].ID })
	return edges, nil
}

// RouteNodeVersions returns the frozen version sequence of one route node.
// The rows live in the shared versioned-record seam, keyed "<node>/<version>".
func (t gormTransaction) RouteNodeVersions(project, nodeID string) ([]RouteNode, error) {
	rows, err := t.listVersionedRecords(project, "route_node_version")
	if err != nil {
		return nil, err
	}
	prefix := nodeID + "/"
	nodes := []RouteNode{}
	for _, row := range rows {
		if !strings.HasPrefix(row.RecordKey, prefix) {
			continue
		}
		version, err := strconv.ParseInt(strings.TrimPrefix(row.RecordKey, prefix), 10, 64)
		if err != nil {
			return nil, err
		}
		var node RouteNode
		if err := json.Unmarshal([]byte(row.BodyJSON), &node); err != nil {
			return nil, err
		}
		node.Version = version
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Version < nodes[j].Version })
	return nodes, nil
}

// newRoadmapSessionID mints the session identity. Kept next to the store so
// the id shape stays in one place.
func newRoadmapSessionID() string { return uuid.NewString() }
