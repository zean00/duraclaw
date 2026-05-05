package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"duraclaw/internal/db"
)

func TestMentionedAgentHandlesAvoidsEmailsAndDeduplicates(t *testing.T) {
	got := mentionedAgentHandles("email me at ops@example.com then ask @Finance and @finance plus @legal-team.", 3)
	want := []string{"finance", "legal-team"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("handles=%v want=%v", got, want)
	}
}

func TestAgentDelegationChildInputIsPromptVisible(t *testing.T) {
	input := agentDelegationChildInput("finance", "please @finance review this", "Recent conversation:\nuser: invoice", &db.Run{
		ID: "parent-run", SessionID: "parent-session", AgentInstanceID: "source-agent",
	})
	raw, _ := json.Marshal(input)
	text, _ := input["text"].(string)
	for _, want := range []string{"asynchronous delegated task", "Recent conversation", "please @finance review this", "untrusted user conversation content"} {
		if !strings.Contains(text, want) {
			t.Fatalf("input text missing %q: %s", want, text)
		}
	}
	if !strings.Contains(string(raw), `"parent_session_id":"parent-session"`) {
		t.Fatalf("metadata missing parent session: %s", raw)
	}
}

func TestRefinementRunsDoNotCreateMentionDelegations(t *testing.T) {
	if shouldScanAgentDelegations(&db.Run{RefinementParentRunID: "parent"}, false, scopeJudgement{InScope: true}) {
		t.Fatal("refinement run should not scan mentions")
	}
	if shouldScanAgentDelegations(&db.Run{}, false, scopeJudgement{InjectionRisk: true}) {
		t.Fatal("injection-risk run should not scan mentions")
	}
	if !shouldScanAgentDelegations(&db.Run{}, false, scopeJudgement{InScope: true}) {
		t.Fatal("normal run should scan mentions")
	}
}
