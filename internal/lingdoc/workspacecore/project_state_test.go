package workspacecore

import (
	"testing"
	"time"
)

// TestProjectArchivedRecognizesBothForms pins the two ways a project stops
// being active.
//
// The discarded timestamp is the production form: it is a column, and a
// discarded project is read back as discarded. The archived status is not a
// value the storage vocabulary allows yet, so it is only reachable in memory;
// naming it here keeps the guard's two branches from drifting apart.
func TestProjectArchivedRecognizesBothForms(t *testing.T) {
	now := time.Now().UTC()
	for _, c := range []struct {
		name    string
		project Project
		want    bool
	}{
		{"draft", Project{Status: "draft"}, false},
		{"active", Project{Status: "active"}, false},
		{"discarded", Project{Status: "active", DiscardedAt: &now}, true},
		{"archived", Project{Status: "archived"}, true},
	} {
		if got := c.project.Archived(); got != c.want {
			t.Fatalf("%s project: Archived() = %v, want %v", c.name, got, c.want)
		}
	}
}
