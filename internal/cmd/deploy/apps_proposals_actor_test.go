package deploy

import "testing"

// DIB-1826: proposals AI Maintenance wrote before the rename still carry the
// old author name; the CLI names them by the author id.
func TestProposalActorNamesAIMaintenanceByID(t *testing.T) {
	cases := []struct{ id, name, email, want string }{
		{"agent.maintenance", "Dibbla maintenance agent", "maintenance-agent@dibbla.app", "AI Maintenance"},
		{"u1", "Ada", "ada@example.com", "Ada"},
		{"u2", "", "bo@example.com", "bo@example.com"},
		{"u3", "", "", "u3"},
	}
	for _, c := range cases {
		if got := proposalActor(c.id, c.name, c.email); got != c.want {
			t.Errorf("proposalActor(%q, %q, %q) = %q, want %q", c.id, c.name, c.email, got, c.want)
		}
	}
}
