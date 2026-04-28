package db

import (
	"os"
	"strings"
	"testing"
)

func TestStoreClaimQueriesUseSkipLocked(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if strings.Count(sql, "FOR UPDATE SKIP LOCKED") < 3 {
		t.Fatalf("expected run, scheduler, and outbox claim queries to use FOR UPDATE SKIP LOCKED")
	}
}

func TestStoreHasLeaseExtensionOwnershipCheck(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"ExtendRunLease", "lease_owner=$2", "state IN ('leased','running','running_workflow')"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing %q", want)
		}
	}
}

func TestStoreCanReleaseOutboxForRetry(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"ReleaseOutbox", "claimed_at=NULL", "available_at=$2"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing %q", want)
		}
	}
}

func TestEventsPageHasLimit(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !strings.Contains(sql, "EventsPage") || !strings.Contains(sql, "LIMIT $3") {
		t.Fatalf("events query should be paginated")
	}
}

func TestRunByIdempotencyKeyIsCustomerScoped(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	if !strings.Contains(sql, "WHERE customer_id=$1 AND session_id=$2 AND idempotency_key=$3") {
		t.Fatalf("RunByIdempotencyKey must include customer_id in lookup")
	}
}

func TestOutboundStatusUpdatesBroadcastTargets(t *testing.T) {
	raw, err := os.ReadFile("outbound.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"UPDATE broadcast_targets", "outbound_intent_id=$1", "UPDATE broadcasts b"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("outbound status update missing %q", want)
		}
	}
}

func TestStoreCanLoadCompletedNonRetryableToolCalls(t *testing.T) {
	raw, err := os.ReadFile("store.go")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"CompletedNonRetryableToolCalls", "retryable=false", "completed_at IS NOT NULL"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("store missing %q", want)
		}
	}
}
