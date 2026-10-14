package store

import (
	"bytes"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSubscriptionOnlineNodesOnlyCountCurrentAssignments(t *testing.T) {
	ctx := t.Context()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	assigned := createTestNode(t, database, "assigned", "native_hysteria2", now)
	unassigned := createTestNode(t, database, "unassigned", "native_hysteria2", now)
	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "renewal-user", Enabled: true,
		NodeIDs: []string{assigned.ID}, Now: now,
	}, bytes.Repeat([]byte{0x52}, 32))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
			ID: uuid.NewString(), UserID: user.ID, Name: "example", Now: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []struct {
		id    string
		known int
	}{{assigned.ID, 1}, {unassigned.ID, 0}} {
		if _, err := database.DB().ExecContext(ctx, `INSERT INTO node_online_users
			(node_id, user_id, connections, known_assignment, sampled_at) VALUES (?, ?, 2, ?, ?)`,
			record.id, user.ID, record.known, now.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	assertCounts := func(want int) {
		t.Helper()
		items, total, err := database.ListSubscriptionOperations(ctx, SubscriptionOperationFilter{Now: now})
		if err != nil || total != 2 || len(items) != 2 {
			t.Fatalf("list: count=%d len=%d err=%v", total, len(items), err)
		}
		for _, item := range items {
			if item.OnlineNodes != want || item.AssignmentCount != want {
				t.Fatalf("online=%d assigned=%d, want both %d", item.OnlineNodes, item.AssignmentCount, want)
			}
		}
	}
	assertCounts(1)
	// A connection can outlive assignment removal, even with a stale known_assignment=1.
	if _, err := database.DB().ExecContext(ctx, `DELETE FROM node_user_assignments WHERE user_id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	assertCounts(0)
}
