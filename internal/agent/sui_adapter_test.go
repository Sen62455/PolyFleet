package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Sen62455/PolyFleet/internal/config"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type fakeSUIAPI struct {
	mu        sync.Mutex
	token     string
	version   string
	nextID    int64
	clients   map[int64]suiClientDetail
	actions   []string
	tokenSeen int
}

func newFakeSUIAPI() *fakeSUIAPI {
	return &fakeSUIAPI{
		token: "local-sui-token", version: "v1.5.3", nextID: 100,
		clients: make(map[int64]suiClientDetail),
	}
}

func (fake *fakeSUIAPI) handler(response http.ResponseWriter, request *http.Request) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if request.Header.Get("Token") != fake.token {
		writeSUIFixture(response, false, nil)
		return
	}
	fake.tokenSeen++
	action := strings.TrimPrefix(request.URL.Path, "/app/apiv2/")
	switch action {
	case "status":
		writeSUIFixture(response, true, map[string]any{
			"sys": map[string]any{"appVersion": fake.version},
			"sbd": map[string]any{"running": true},
		})
	case "inbounds":
		writeSUIFixture(response, true, map[string]any{"inbounds": []map[string]any{
			{"id": 7, "type": "hysteria2", "tag": "hy2-in", "listen": "::", "listen_port": 443},
			{"id": 8, "type": "http", "tag": "http-in", "listen": "127.0.0.1", "listen_port": 8080},
			{"id": 9, "type": "hysteria2", "tag": "hy2-alt", "listen": "::", "listen_port": 8443},
		}})
	case "clients":
		if idValue := request.URL.Query().Get("id"); idValue != "" {
			id, _ := strconv.ParseInt(idValue, 10, 64)
			client, ok := fake.clients[id]
			if !ok {
				writeSUIFixture(response, true, map[string]any{"clients": []any{}})
				return
			}
			writeSUIFixture(response, true, map[string]any{"clients": []suiClientDetail{client}})
			return
		}
		ids := make([]int64, 0, len(fake.clients))
		for id := range fake.clients {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(left, right int) bool { return ids[left] < ids[right] })
		summaries := make([]suiClientSummary, 0, len(ids))
		for _, id := range ids {
			client := fake.clients[id]
			summaries = append(summaries, suiClientSummary{
				ID: client.ID, Enable: client.Enable, Name: client.Name,
				Inbounds: client.Inbounds, Volume: client.Volume, Expiry: client.Expiry,
				Down: client.Down, Up: client.Up, Desc: client.Desc, Group: client.Group,
				OnlineAt: client.OnlineAt,
			})
		}
		writeSUIFixture(response, true, map[string]any{"clients": summaries})
	case "onlines":
		online := make([]string, 0)
		for _, client := range fake.clients {
			if client.OnlineAt > 0 {
				online = append(online, client.Name)
			}
		}
		writeSUIFixture(response, true, map[string]any{"user": online})
	case "save":
		if err := request.ParseForm(); err != nil || request.FormValue("object") != "clients" {
			writeSUIFixture(response, false, nil)
			return
		}
		saveAction := request.FormValue("action")
		fake.actions = append(fake.actions, saveAction)
		switch saveAction {
		case "new", "edit":
			var client suiClientDetail
			if json.Unmarshal([]byte(request.FormValue("data")), &client) != nil {
				writeSUIFixture(response, false, nil)
				return
			}
			if saveAction == "new" {
				client.ID = fake.nextID
				fake.nextID++
			}
			fake.clients[client.ID] = client
		case "del":
			var id int64
			if json.Unmarshal([]byte(request.FormValue("data")), &id) != nil {
				writeSUIFixture(response, false, nil)
				return
			}
			delete(fake.clients, id)
		default:
			writeSUIFixture(response, false, nil)
			return
		}
		writeSUIFixture(response, true, map[string]any{})
	default:
		http.NotFound(response, request)
	}
}

func writeSUIFixture(response http.ResponseWriter, success bool, object any) {
	response.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(response).Encode(map[string]any{
		"success": success, "msg": "fixture", "obj": object,
	})
}

func suiDetail(t *testing.T, id int64, name, secret string, inbounds []int64) suiClientDetail {
	t.Helper()
	configData, err := json.Marshal(map[string]any{
		"hysteria2": map[string]any{"name": name, "password": secret},
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	return suiClientDetail{
		ID: id, Enable: true, Name: name, Config: configData,
		Inbounds: append([]int64(nil), inbounds...), Links: json.RawMessage("[]"),
		Desc: "manual metadata", Group: "personal", Volume: 9876,
	}
}

func TestSUIClientProbeAndDiscoveryUseRedactedHY2Views(t *testing.T) {
	fake := newFakeSUIAPI()
	fake.clients[10] = suiDetail(t, 10, "hy2-user", "sentinel-password", []int64{7})
	fake.clients[11] = suiDetail(t, 11, "http-only", "other-password", []int64{8})
	server := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer server.Close()
	client := newSUIClient(server.URL+"/app/apiv2", fake.token)
	adapter, core := client.probe(context.Background(), time.Now().UTC())
	if adapter.Status != "compatible" || adapter.Version != "v1.5.3" || !core.Running {
		t.Fatalf("probe = %#v %#v", adapter, core)
	}
	discovery, err := client.discover(context.Background())
	if err != nil || len(discovery.Inbounds) != 2 || len(discovery.Clients) != 1 ||
		discovery.Clients[0].Name != "hy2-user" {
		t.Fatalf("discover() = %#v, error = %v", discovery, err)
	}
	encoded, _ := json.Marshal(discovery.Clients[0])
	if bytes.Contains(encoded, []byte("sentinel-password")) || bytes.Contains(encoded, []byte("config")) {
		t.Fatalf("redacted discovery leaked a credential: %s", encoded)
	}
	fake.mu.Lock()
	fake.version = "v1.4.2"
	fake.mu.Unlock()
	adapter, _ = client.probe(context.Background(), time.Now().UTC())
	if adapter.Status != "incompatible" || adapter.ErrorCode != "sui_version_unsupported" {
		t.Fatalf("incompatible probe = %#v", adapter)
	}
}

func TestSUIReconciliationIsIdempotentAndGuardsOwnership(t *testing.T) {
	ctx := context.Background()
	fake := newFakeSUIAPI()
	manualSecret := "manual-secret-must-stay"
	manual := suiDetail(t, 10, "manual-user", manualSecret, []int64{7})
	manual.Remark = "preserve-me"
	fake.clients[10] = manual
	suiServer := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer suiServer.Close()

	credentialSecret := "managed-credential-material"
	credentialRequests := 0
	controller := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/agent/v1/credential-material" {
			http.NotFound(response, request)
			return
		}
		credentialRequests++
		var input protocol.CredentialMaterialRequest
		if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
			t.Fatalf("decode credential request: %v", err)
		}
		writeAgentJSON(response, protocol.CredentialMaterialResponse{
			CredentialRef: input.CredentialRef, Secret: credentialSecret,
		})
	}))
	defer controller.Close()

	databasePath := filepath.Join(t.TempDir(), "agent.db")
	local, err := openLocalStore(ctx, databasePath)
	if err != nil {
		t.Fatalf("openLocalStore() error = %v", err)
	}
	defer func() { _ = local.Close() }()
	nodeID := "019fe000-0000-7000-8000-000000000001"
	if err := local.bindSUIStore(ctx, nodeID); err != nil {
		t.Fatalf("bindSUIStore() error = %v", err)
	}
	agent := &Agent{
		config: config.Agent{ServerURL: controller.URL, AdapterType: "s_ui"},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		client: controller.Client(),
		state: State{
			NodeID: nodeID, NodeCredential: "agent-credential",
			InstallationID: "019fe000-0000-7000-8000-000000000002",
		},
		localStore: local,
		suiClient:  newSUIClient(suiServer.URL+"/app/apiv2", fake.token),
	}
	desired := protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: nodeID, Version: 2, Adapter: "s_ui",
		Users: []protocol.DesiredUser{{
			ID: "019fe000-0000-7000-8000-000000000003", Username: "managed-user",
			Credential: protocol.DesiredCredential{
				Ref:         "019fe000-0000-7000-8000-000000000004",
				Fingerprint: suiCredentialFingerprint(credentialSecret),
			},
			Enabled: true, QuotaState: "unlimited", ManagementMode: "managed",
		}},
		Kicks:       []protocol.DesiredKick{},
		SUI:         &protocol.DesiredSUI{TargetInboundIDs: []int64{7}},
		GeneratedAt: time.Now().UTC(),
	}
	envelope := envelopeFor(desired)
	if err := agent.applySUIDesired(ctx, envelope); err != nil {
		t.Fatalf("applySUIDesired() error = %v", err)
	}
	if err := agent.applySUIDesired(ctx, envelope); err != nil {
		t.Fatalf("applySUIDesired(repeated) error = %v", err)
	}
	fake.mu.Lock()
	if len(fake.actions) != 1 || fake.actions[0] != "new" || len(fake.clients) != 2 {
		t.Fatalf("S-UI actions = %#v, clients = %#v", fake.actions, fake.clients)
	}
	if got := fake.clients[10]; got.Remark != manual.Remark || got.Volume != manual.Volume || got.Name != manual.Name {
		t.Fatalf("unmanaged client changed: %#v", got)
	}
	managedID := int64(100)
	managed := fake.clients[managedID]
	managed.Inbounds = append(managed.Inbounds, 8)
	fake.clients[managedID] = managed
	fake.mu.Unlock()
	desired.Version++
	desired.SUI = &protocol.DesiredSUI{TargetInboundIDs: []int64{9}}
	envelope = envelopeFor(desired)
	if err := agent.applySUIDesired(ctx, envelope); err != nil {
		t.Fatalf("applySUIDesired(retarget) error = %v", err)
	}
	if err := agent.applySUIDesired(ctx, envelope); err != nil {
		t.Fatalf("applySUIDesired(repeated retarget) error = %v", err)
	}
	fake.mu.Lock()
	if !slices.Equal(fake.clients[managedID].Inbounds, []int64{8, 9}) ||
		len(fake.actions) != 2 || fake.actions[1] != "edit" {
		t.Fatalf("retargeted managed client = %#v, actions = %#v", fake.clients[managedID], fake.actions)
	}
	fake.mu.Unlock()
	missingTargets := desired
	missingTargets.Version++
	missingTargets.SUI = &protocol.DesiredSUI{}
	if err := agent.applySUIDesired(ctx, envelopeFor(missingTargets)); suiErrorCode(err) != "sui_target_inbound_required" {
		t.Fatalf("empty managed targets error = %v (%s)", err, suiErrorCode(err))
	}
	fake.mu.Lock()
	if !slices.Equal(fake.clients[managedID].Inbounds, []int64{8, 9}) || len(fake.actions) != 2 {
		t.Fatalf("empty targets changed managed client = %#v, actions = %#v", fake.clients[managedID], fake.actions)
	}
	managed = fake.clients[managedID]
	managed.ID = 115
	delete(fake.clients, managedID)
	fake.clients[managed.ID] = managed
	fake.mu.Unlock()
	if err := agent.applySUIDesired(ctx, envelope); err != nil {
		t.Fatalf("applySUIDesired(ID recovery) error = %v", err)
	}
	mappings, err := local.listSUIMappings(ctx)
	if err != nil || len(mappings) != 1 || mappings[0].RemoteClientID != 115 {
		t.Fatalf("recovered mappings = %#v, error = %v", mappings, err)
	}
	fake.mu.Lock()
	renamed := fake.clients[115]
	renamed.Name = "manually-renamed"
	fake.clients[115] = renamed
	fake.mu.Unlock()
	if err := agent.applySUIDesired(ctx, envelope); err != nil {
		t.Fatalf("applySUIDesired(name repair) error = %v", err)
	}
	fake.mu.Lock()
	if fake.clients[115].Name != "managed-user" {
		t.Fatalf("managed name was not reconciled: %#v", fake.clients[115])
	}
	tampered := fake.clients[115]
	if err := setSUIClientCredential(&tampered, tampered.Name, "manual-password-change"); err != nil {
		t.Fatal(err)
	}
	fake.clients[115] = tampered
	fake.mu.Unlock()
	empty := desired
	empty.Version++
	empty.Users = []protocol.DesiredUser{}
	emptyEnvelope := envelopeFor(empty)
	err = agent.applySUIDesired(ctx, emptyEnvelope)
	if suiErrorCode(err) != "sui_ownership_guard_failed" {
		t.Fatalf("ownership guard error = %v (%s)", err, suiErrorCode(err))
	}
	fake.mu.Lock()
	if _, exists := fake.clients[115]; !exists {
		t.Fatal("ownership guard allowed deletion after a manual credential change")
	}
	restored := fake.clients[115]
	if err := setSUIClientCredential(&restored, restored.Name, credentialSecret); err != nil {
		t.Fatal(err)
	}
	fake.clients[115] = restored
	fake.mu.Unlock()
	if err := agent.applySUIDesired(ctx, emptyEnvelope); err != nil {
		t.Fatalf("applySUIDesired(delete owned) error = %v", err)
	}
	fake.mu.Lock()
	_, managedExists := fake.clients[115]
	_, manualExists := fake.clients[10]
	fake.mu.Unlock()
	if managedExists || !manualExists {
		t.Fatalf("owned/unmanaged deletion result = managed:%v manual:%v", managedExists, manualExists)
	}
	if credentialRequests != 1 {
		t.Fatalf("credential material requests = %d, want 1", credentialRequests)
	}
	if err := local.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), filepath.Base(databasePath)) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(filepath.Dir(databasePath), entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte(credentialSecret)) || bytes.Contains(data, []byte(manualSecret)) {
			t.Fatalf("Agent local database file %s persisted plaintext", entry.Name())
		}
	}
}

func TestSUIReadOnlyMappingNeverMutatesOrFetchesCredentials(t *testing.T) {
	ctx := context.Background()
	fake := newFakeSUIAPI()
	fake.clients[10] = suiDetail(t, 10, "existing-user", "existing-password", []int64{7})
	suiServer := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer suiServer.Close()
	controllerRequests := 0
	controller := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		controllerRequests++
		http.Error(response, "unexpected", http.StatusInternalServerError)
	}))
	defer controller.Close()
	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	nodeID := "019fe000-0000-7000-8000-000000000011"
	if err := local.bindSUIStore(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		config: config.Agent{ServerURL: controller.URL, AdapterType: "s_ui"},
		client: controller.Client(), logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		state: State{NodeID: nodeID, NodeCredential: "credential",
			InstallationID: "019fe000-0000-7000-8000-000000000012"},
		localStore: local, suiClient: newSUIClient(suiServer.URL+"/app/apiv2", fake.token),
	}
	snapshot := protocol.DesiredSnapshot{
		SchemaVersion: 1, NodeID: nodeID, Version: 1, Adapter: "s_ui",
		Users: []protocol.DesiredUser{{
			ID: "019fe000-0000-7000-8000-000000000013", Username: "mapped-user",
			Credential: protocol.DesiredCredential{Ref: "ref", Fingerprint: "fp_unused"},
			Enabled:    true, QuotaState: "unlimited", ManagementMode: "read_only", RemoteClientID: 10,
		}},
		Kicks: []protocol.DesiredKick{}, SUI: &protocol.DesiredSUI{TargetInboundIDs: []int64{7}},
		GeneratedAt: time.Now().UTC(),
	}
	if err := agent.applySUIDesired(ctx, envelopeFor(snapshot)); err != nil {
		t.Fatalf("applySUIDesired(read-only) error = %v", err)
	}
	fake.mu.Lock()
	actions := append([]string(nil), fake.actions...)
	client := fake.clients[10]
	fake.mu.Unlock()
	if len(actions) != 0 || controllerRequests != 0 || client.Name != "existing-user" {
		t.Fatalf("read-only mapping mutated state: actions=%v requests=%d client=%#v", actions, controllerRequests, client)
	}
	mappings, _ := local.listSUIMappings(ctx)
	if len(mappings) != 1 || mappings[0].ManagementMode != "read_only" || mappings[0].RemoteClientID != 10 {
		t.Fatalf("read-only mappings = %#v", mappings)
	}
}

func TestSUIUsageCycleReportsUnmappedClientsAsUnattributed(t *testing.T) {
	ctx := context.Background()
	fake := newFakeSUIAPI()
	client := suiDetail(t, 10, "unmapped-client", "unmapped-password", []int64{7})
	client.Up = 100
	client.Down = 200
	client.OnlineAt = time.Now().UTC().Unix()
	fake.clients[client.ID] = client
	suiServer := httptest.NewServer(http.HandlerFunc(fake.handler))
	defer suiServer.Close()

	var captureMu sync.Mutex
	var onlineSnapshots []protocol.OnlineSnapshotRequest
	var trafficRequests []protocol.TrafficBatchesRequest
	controller := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/agent/v1/s-ui-report":
			writeAgentJSON(response, protocol.SUIReportResponse{Accepted: true, ServerTime: time.Now().UTC()})
		case "/agent/v1/online-snapshot":
			var snapshot protocol.OnlineSnapshotRequest
			if err := json.NewDecoder(request.Body).Decode(&snapshot); err != nil {
				t.Errorf("decode online snapshot: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			captureMu.Lock()
			onlineSnapshots = append(onlineSnapshots, snapshot)
			captureMu.Unlock()
			writeAgentJSON(response, protocol.OnlineSnapshotResponse{Accepted: true, ServerTime: time.Now().UTC()})
		case "/agent/v1/traffic-batches":
			var batches protocol.TrafficBatchesRequest
			if err := json.NewDecoder(request.Body).Decode(&batches); err != nil {
				t.Errorf("decode traffic batches: %v", err)
				response.WriteHeader(http.StatusBadRequest)
				return
			}
			captureMu.Lock()
			trafficRequests = append(trafficRequests, batches)
			captureMu.Unlock()
			results := make([]protocol.TrafficBatchResult, 0, len(batches.Batches))
			for _, batch := range batches.Batches {
				results = append(results, protocol.TrafficBatchResult{ID: batch.ID, Status: "accepted"})
			}
			writeAgentJSON(response, protocol.TrafficBatchesResponse{Results: results, ServerTime: time.Now().UTC()})
		default:
			http.NotFound(response, request)
		}
	}))
	defer controller.Close()

	local, err := openLocalStore(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer local.Close()
	nodeID := "019fe000-0000-7000-8000-000000000021"
	if err := local.bindSUIStore(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	agent := &Agent{
		config: config.Agent{ServerURL: controller.URL, AdapterType: "s_ui", SUIToken: fake.token},
		client: controller.Client(), logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		state: State{NodeID: nodeID, NodeCredential: "credential",
			InstallationID: "019fe000-0000-7000-8000-000000000022"},
		localStore: local, suiClient: newSUIClient(suiServer.URL+"/app/apiv2", fake.token),
	}
	if err := agent.runSUIUsageCycle(ctx); err != nil {
		t.Fatalf("runSUIUsageCycle(first) error = %v", err)
	}
	fake.mu.Lock()
	updated := fake.clients[client.ID]
	updated.Up += 50
	updated.Down += 60
	fake.clients[client.ID] = updated
	fake.mu.Unlock()
	if err := agent.runSUIUsageCycle(ctx); err != nil {
		t.Fatalf("runSUIUsageCycle(second) error = %v", err)
	}

	captureMu.Lock()
	defer captureMu.Unlock()
	if len(onlineSnapshots) != 2 || len(onlineSnapshots[1].Users) != 1 ||
		onlineSnapshots[1].Users[0].UserID != "sui:10" ||
		onlineSnapshots[1].Users[0].Connections != 1 {
		t.Fatalf("online snapshots = %#v", onlineSnapshots)
	}
	if len(trafficRequests) != 1 || len(trafficRequests[0].Batches) != 1 ||
		len(trafficRequests[0].Batches[0].Items) != 1 {
		t.Fatalf("traffic requests = %#v", trafficRequests)
	}
	item := trafficRequests[0].Batches[0].Items[0]
	if item.UserID != "sui:10" || item.UploadBytes != 50 || item.DownloadBytes != 60 {
		t.Fatalf("unattributed traffic item = %#v", item)
	}
}
