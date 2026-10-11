package store

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
	"github.com/google/uuid"
)

func TestExitProxyCatalogAndSubscriptionChain(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "server.db"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()
	now := time.Now().UTC().Truncate(time.Millisecond)
	masterKey := bytes.Repeat([]byte{0x17}, 32)

	landing, err := database.CreateExitProxy(ctx, NewExitProxy{
		ID: uuid.NewString(), Name: "UK landing", Kind: ExitProxyKindLandingVPS,
		Protocol: ExitProxyProtocolSOCKS5, Host: "203.0.113.9", Port: 1080,
		Username: "polyfleet", Password: "landing-secret", UDP: true,
		Now: now, MasterKey: masterKey,
	})
	if err != nil || !landing.PasswordSet || landing.Host != "203.0.113.9" {
		t.Fatalf("CreateExitProxy() = %#v, error = %v", landing, err)
	}
	if _, err := database.CreateExitProxy(ctx, NewExitProxy{
		ID: uuid.NewString(), Name: "UK landing", Kind: ExitProxyKindRentedIP,
		Protocol: ExitProxyProtocolHTTP, Host: "203.0.113.10", Port: 8080,
		Now: now.Add(time.Second), MasterKey: masterKey,
	}); err != ErrConflict {
		t.Fatalf("duplicate name error = %v, want conflict", err)
	}

	node, err := database.CreateNode(ctx, NewNode{
		ID: uuid.NewString(), Name: "LA entry", AdapterType: "native_hysteria2",
		PublicHost: "hy2.example.com", PublicPort: 443, Enabled: true,
		ExitProxyID: landing.ID, Now: now.Add(2 * time.Second),
	})
	if err != nil || node.ExitProxyID != landing.ID || node.ExitProxy == nil ||
		node.ExitProxy.Name != "UK landing" {
		t.Fatalf("CreateNode with exit = %#v, error = %v", node, err)
	}

	listed, err := database.ListExitProxies(ctx)
	if err != nil || len(listed) != 1 || len(listed[0].Assignments) != 1 ||
		listed[0].Assignments[0].NodeName != "LA entry" || listed[0].PasswordCiphertext != nil {
		t.Fatalf("ListExitProxies() = %#v, error = %v", listed, err)
	}

	user, _, err := database.CreateUser(ctx, NewUser{
		ID: uuid.NewString(), Username: "chain-user", Enabled: true,
		NodeIDs: []string{node.ID}, Now: now.Add(3 * time.Second),
	}, masterKey)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	issued, err := database.CreateSubscriptionToken(ctx, NewSubscriptionToken{
		ID: uuid.NewString(), UserID: user.ID, Name: "primary",
		Now: now.Add(4 * time.Second),
	})
	if err != nil {
		t.Fatalf("CreateSubscriptionToken() error = %v", err)
	}
	ackCurrentDesired(t, database, node.ID, now.Add(5*time.Second))
	subscription, err := database.ResolveSubscription(
		ctx, cryptoutil.TokenHash(issued.Secret), "clash", now.Add(6*time.Second), masterKey,
	)
	if err != nil || len(subscription.Endpoints) != 1 || subscription.Endpoints[0].Exit == nil ||
		subscription.Endpoints[0].Exit.Host != "203.0.113.9" ||
		subscription.Endpoints[0].Exit.Password != "landing-secret" {
		t.Fatalf("ResolveSubscription() = %#v, error = %v", subscription, err)
	}

	if landing.ServerSide {
		t.Fatal("existing exits must default to client chains")
	}
	serverSide := true
	update := UpdateExitProxy{Name: landing.Name, Kind: landing.Kind, Protocol: landing.Protocol, Host: landing.Host, Port: landing.Port, Username: landing.Username, UDP: true, ServerSide: &serverSide, Now: now, MasterKey: masterKey}
	updated, err := database.UpdateExitProxy(ctx, landing.ID, update)
	if err != nil || !updated.ServerSide {
		t.Fatalf("server side update: %v, %v", updated.ServerSide, err)
	}
	update.ServerSide = nil // Older clients must not silently revert an active route.
	updated, err = database.UpdateExitProxy(ctx, landing.ID, update)
	if err != nil || !updated.ServerSide {
		t.Fatalf("omitted flag lost server-side mode: %v", err)
	}
	subscription, err = database.ResolveSubscription(ctx, cryptoutil.TokenHash(issued.Secret), "clash", now.Add(6*time.Second), masterKey)
	if err != nil || len(subscription.Endpoints) != 1 || subscription.Endpoints[0].Exit == nil || !subscription.Endpoints[0].Exit.ServerSide {
		t.Fatalf("subscription lost routing mode: %v", err)
	}
	cleared, err := database.ReplaceExitProxyNodes(ctx, landing.ID, nil, now.Add(7*time.Second))
	if err != nil || len(cleared.Assignments) != 0 {
		t.Fatalf("ReplaceExitProxyNodes(clear) = %#v, error = %v", cleared, err)
	}
	node, err = database.GetNode(ctx, node.ID)
	if err != nil || node.ExitProxyID != "" || node.ExitProxy != nil {
		t.Fatalf("cleared node exit = %#v, error = %v", node, err)
	}

	if err := database.DeleteExitProxy(ctx, landing.ID); err != nil {
		t.Fatalf("DeleteExitProxy() error = %v", err)
	}
	if _, err := database.GetExitProxy(ctx, landing.ID); err != ErrNotFound {
		t.Fatalf("deleted proxy error = %v, want not found", err)
	}
}
