package agent

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
	"github.com/google/uuid"
)

func TestTrafficOutboxPersistsDeltasAndRotatesAfterCounterReset(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	installationID := uuid.NewString()
	userID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)

	local, err := openLocalStore(ctx, path)
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	if batches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 100, RX: 200},
	}, now); err != nil || len(batches) != 0 {
		t.Fatalf("initial baseline batches = %#v, error = %v", batches, err)
	}
	firstBatches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 150, RX: 260},
	}, now.Add(time.Second))
	if err != nil || len(firstBatches) != 1 {
		t.Fatalf("first delta batches = %#v, error = %v", firstBatches, err)
	}
	first := firstBatches[0]
	assertTrafficDelta(t, first, userID, 50, 60)
	if first.Sequence != 1 {
		t.Fatalf("first sequence = %d, want 1", first.Sequence)
	}
	firstEpoch := first.SourceEpoch
	if err := local.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	local, err = openLocalStore(ctx, path)
	if err != nil {
		t.Fatalf("openLocalStore(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	secondBatches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 170, RX: 280},
	}, now.Add(2*time.Second))
	if err != nil || len(secondBatches) != 1 {
		t.Fatalf("restart delta batches = %#v, error = %v", secondBatches, err)
	}
	second := secondBatches[0]
	assertTrafficDelta(t, second, userID, 20, 20)
	if second.SourceEpoch != firstEpoch || second.Sequence != 2 {
		t.Fatalf("restart source = %s/%d, want %s/2", second.SourceEpoch, second.Sequence, firstEpoch)
	}

	resetBatches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 5, RX: 7},
	}, now.Add(3*time.Second))
	if err != nil || len(resetBatches) != 1 {
		t.Fatalf("reset delta batches = %#v, error = %v", resetBatches, err)
	}
	reset := resetBatches[0]
	assertTrafficDelta(t, reset, userID, 5, 7)
	if reset.SourceEpoch == firstEpoch || reset.Sequence != 1 {
		t.Fatalf("reset source = %s/%d, old epoch = %s", reset.SourceEpoch, reset.Sequence, firstEpoch)
	}
	batches, err := local.listTrafficOutbox(ctx, 10)
	if err != nil || len(batches) != 3 {
		t.Fatalf("outbox batches = %d, error = %v", len(batches), err)
	}
}

func TestTrafficCounterEpochChangeCountsNewProcessWithoutComparingOldBaseline(t *testing.T) {
	ctx := context.Background()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	installationID := uuid.NewString()
	userID := uuid.NewString()
	firstCounterEpoch := uuid.NewString()
	secondCounterEpoch := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)

	if batches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 100, RX: 200},
	}, now, firstCounterEpoch); err != nil || len(batches) != 0 {
		t.Fatalf("initial epoch baseline = %#v, error = %v", batches, err)
	}
	first, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 150, RX: 260},
	}, now.Add(time.Second), firstCounterEpoch)
	if err != nil || len(first) != 1 {
		t.Fatalf("first epoch traffic = %#v, error = %v", first, err)
	}
	assertTrafficDelta(t, first[0], userID, 50, 60)

	second, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 500, RX: 700},
	}, now.Add(2*time.Second), secondCounterEpoch)
	if err != nil || len(second) != 1 {
		t.Fatalf("new counter epoch traffic = %#v, error = %v", second, err)
	}
	assertTrafficDelta(t, second[0], userID, 500, 700)
	if second[0].SourceEpoch == first[0].SourceEpoch || second[0].Sequence != 1 {
		t.Fatalf("new counter epoch source = %s/%d, old = %s",
			second[0].SourceEpoch, second[0].Sequence, first[0].SourceEpoch)
	}
}

func TestFirstCounterEpochAfterUpgradeRotatesLegacyBaseline(t *testing.T) {
	ctx := context.Background()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	installationID := uuid.NewString()
	userID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if batches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 100, RX: 200},
	}, now); err != nil || len(batches) != 0 {
		t.Fatalf("legacy baseline = %#v, error = %v", batches, err)
	}
	legacy, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 150, RX: 260},
	}, now.Add(time.Second))
	if err != nil || len(legacy) != 1 {
		t.Fatalf("legacy traffic = %#v, error = %v", legacy, err)
	}

	upgraded, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 500, RX: 700},
	}, now.Add(2*time.Second), uuid.NewString())
	if err != nil || len(upgraded) != 1 {
		t.Fatalf("first epoch-aware traffic = %#v, error = %v", upgraded, err)
	}
	assertTrafficDelta(t, upgraded[0], userID, 500, 700)
	if upgraded[0].SourceEpoch == legacy[0].SourceEpoch || upgraded[0].Sequence != 1 {
		t.Fatalf("upgrade counter epoch source = %s/%d, old = %s",
			upgraded[0].SourceEpoch, upgraded[0].Sequence, legacy[0].SourceEpoch)
	}
}

func TestTrafficSampleSplitsOutboxAtProtocolLimit(t *testing.T) {
	ctx := context.Background()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	installationID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	baseline := make(map[string]trafficCounters, protocol.MaxTrafficItemsPerBatch+1)
	current := make(map[string]trafficCounters, protocol.MaxTrafficItemsPerBatch+1)
	for range protocol.MaxTrafficItemsPerBatch + 1 {
		userID := uuid.NewString()
		baseline[userID] = trafficCounters{TX: 10, RX: 20}
		current[userID] = trafficCounters{TX: 11, RX: 22}
	}
	if batches, err := local.recordTrafficSample(ctx, installationID, baseline, now); err != nil || len(batches) != 0 {
		t.Fatalf("baseline batches = %d, error = %v", len(batches), err)
	}
	batches, err := local.recordTrafficSample(ctx, installationID, current, now.Add(time.Second))
	if err != nil {
		t.Fatalf("recordTrafficSample() error = %v", err)
	}
	if len(batches) != 2 || len(batches[0].Items) != protocol.MaxTrafficItemsPerBatch ||
		len(batches[1].Items) != 1 || batches[0].Sequence != 1 || batches[1].Sequence != 2 ||
		batches[0].SourceEpoch != batches[1].SourceEpoch {
		t.Fatalf("split batches = %#v", batches)
	}
	stored, err := local.listTrafficOutbox(ctx, 10)
	if err != nil || len(stored) != 2 || stored[0].ID != batches[0].ID || stored[1].ID != batches[1].ID {
		t.Fatalf("stored split batches = %#v, error = %v", stored, err)
	}
}

func TestTrafficBaselinePrimeExcludesPreImportUsage(t *testing.T) {
	ctx := context.Background()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	installationID := uuid.NewString()
	userID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	if batches, err := local.recordTrafficSample(
		ctx, installationID, map[string]trafficCounters{}, now,
	); err != nil || len(batches) != 0 {
		t.Fatalf("initialize empty traffic source = %#v, error = %v", batches, err)
	}
	if err := local.primeTrafficBaseline(
		ctx, userID, trafficCounters{TX: 500, RX: 700}, now.Add(time.Second),
	); err != nil {
		t.Fatalf("primeTrafficBaseline() error = %v", err)
	}
	if batches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 500, RX: 700},
	}, now.Add(2*time.Second)); err != nil || len(batches) != 0 {
		t.Fatalf("pre-import usage was emitted = %#v, error = %v", batches, err)
	}
	batches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		userID: {TX: 510, RX: 720},
	}, now.Add(3*time.Second))
	if err != nil || len(batches) != 1 {
		t.Fatalf("post-import traffic batches = %#v, error = %v", batches, err)
	}
	assertTrafficDelta(t, batches[0], userID, 10, 20)
}

func TestTrafficResetDoesNotRecountUnaffectedUsers(t *testing.T) {
	ctx := context.Background()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	installationID := uuid.NewString()
	resetUserID := uuid.NewString()
	steadyUserID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	baseline := map[string]trafficCounters{
		resetUserID:  {TX: 100, RX: 200},
		steadyUserID: {TX: 300, RX: 400},
	}
	if batches, err := local.recordTrafficSample(ctx, installationID, baseline, now); err != nil || len(batches) != 0 {
		t.Fatalf("initial baselines = %#v, error = %v", batches, err)
	}
	if _, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		resetUserID:  {TX: 150, RX: 250},
		steadyUserID: {TX: 350, RX: 450},
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("first traffic sample error = %v", err)
	}
	batches, err := local.recordTrafficSample(ctx, installationID, map[string]trafficCounters{
		resetUserID:  {TX: 5, RX: 7},
		steadyUserID: {TX: 370, RX: 480},
	}, now.Add(2*time.Second))
	if err != nil || len(batches) != 1 || len(batches[0].Items) != 2 {
		t.Fatalf("partial reset batches = %#v, error = %v", batches, err)
	}
	deltas := make(map[string]protocol.TrafficDelta, len(batches[0].Items))
	for _, item := range batches[0].Items {
		deltas[item.UserID] = item
	}
	if delta := deltas[resetUserID]; delta.UploadBytes != 5 || delta.DownloadBytes != 7 {
		t.Fatalf("reset user delta = %#v", delta)
	}
	if delta := deltas[steadyUserID]; delta.UploadBytes != 20 || delta.DownloadBytes != 30 {
		t.Fatalf("steady user was recounted = %#v", delta)
	}
}

func TestKickOutboxPersistsAndAppliesHighestGeneration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")
	userID := uuid.NewString()
	now := time.Now().UTC().Truncate(time.Millisecond)
	local, err := openLocalStore(ctx, path)
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	if err := local.queueKicks(ctx, []protocol.DesiredKick{{UserID: userID, Generation: 1}}, now); err != nil {
		t.Fatalf("queueKicks(1) error = %v", err)
	}
	if err := local.recordKickFailure(ctx, []pendingKick{{UserID: userID, Generation: 1}}, "offline", now); err != nil {
		t.Fatalf("recordKickFailure() error = %v", err)
	}
	if err := local.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	local, err = openLocalStore(ctx, path)
	if err != nil {
		t.Fatalf("openLocalStore(restart) error = %v", err)
	}
	t.Cleanup(func() { _ = local.Close() })
	if err := local.queueKicks(ctx, []protocol.DesiredKick{
		{UserID: userID, Generation: 1},
		{UserID: userID, Generation: 3},
	}, now.Add(time.Second)); err != nil {
		t.Fatalf("queueKicks(3) error = %v", err)
	}
	kicks, err := local.listPendingKicks(ctx, 10)
	if err != nil || len(kicks) != 1 || kicks[0].Generation != 3 {
		t.Fatalf("pending kicks = %#v, error = %v", kicks, err)
	}
	if err := local.markKicksApplied(ctx, kicks, now.Add(2*time.Second)); err != nil {
		t.Fatalf("markKicksApplied() error = %v", err)
	}
	if err := local.queueKicks(ctx, []protocol.DesiredKick{{UserID: userID, Generation: 2}}, now.Add(3*time.Second)); err != nil {
		t.Fatalf("queueKicks(stale) error = %v", err)
	}
	kicks, err = local.listPendingKicks(ctx, 10)
	if err != nil || len(kicks) != 0 {
		t.Fatalf("stale kick was queued: %#v, error = %v", kicks, err)
	}
}

func assertTrafficDelta(t *testing.T, batch protocol.TrafficBatch, userID string, upload, download int64) {
	t.Helper()
	if len(batch.Items) != 1 || batch.Items[0].UserID != userID ||
		batch.Items[0].UploadBytes != upload || batch.Items[0].DownloadBytes != download {
		t.Fatalf("traffic batch items = %#v, want %s %d/%d", batch.Items, userID, upload, download)
	}
}
