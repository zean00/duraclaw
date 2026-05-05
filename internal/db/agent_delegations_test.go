package db

import "testing"

func TestAgentDelegationAllowedMatchesHandleOrAgentID(t *testing.T) {
	access := EffectiveAgentDelegationAccess{AllowedAgents: []string{"finance", "agent-2"}}
	if !agentDelegationAllowed(access, "agent-2", "other") {
		t.Fatal("expected agent id allow")
	}
	if !agentDelegationAllowed(access, "agent-3", "@finance") {
		t.Fatal("expected handle allow")
	}
	if agentDelegationAllowed(access, "agent-4", "legal") {
		t.Fatal("unexpected allow")
	}
}

func TestAgentDelegationDeniedWins(t *testing.T) {
	access := EffectiveAgentDelegationAccess{AllowedAgents: []string{"finance"}, DeniedAgents: []string{"@finance"}}
	if agentDelegationAllowed(access, "agent-2", "finance") {
		t.Fatal("denied handle should win")
	}
}
