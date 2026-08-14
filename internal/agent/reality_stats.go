package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/Sen62455/PolyFleet/internal/protocol"
)

type realityStatsClient struct {
	baseURL string
	secret  string
	client  *http.Client
}

type realityUserSnapshot struct {
	User        string `json:"user"`
	Upload      int64  `json:"upload"`
	Download    int64  `json:"download"`
	Connections int    `json:"connections"`
}

func newRealityStatsClient(baseURL, secret string) *realityStatsClient {
	return &realityStatsClient{
		baseURL: strings.TrimRight(baseURL, "/"), secret: secret,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

func (client *realityStatsClient) users(ctx context.Context) (string, []realityUserSnapshot, error) {
	var response struct {
		Epoch string                `json:"epoch"`
		Users []realityUserSnapshot `json:"users"`
	}
	if err := client.doJSON(ctx, http.MethodGet, "/hyfleet/v1/users", nil, &response); err != nil {
		return "", nil, err
	}
	if _, err := uuid.Parse(response.Epoch); err != nil || response.Users == nil ||
		len(response.Users) > maxStatsUsers {
		return "", nil, errors.New("Reality user response has an invalid size")
	}
	seen := make(map[string]struct{}, len(response.Users))
	for _, user := range response.Users {
		parsed, err := uuid.Parse(user.User)
		if err != nil || parsed.String() != user.User || user.Upload < 0 || user.Download < 0 ||
			user.Connections < 0 || user.Connections > maxOnlineConnections {
			return "", nil, errors.New("Reality user response contains invalid data")
		}
		if _, duplicate := seen[user.User]; duplicate {
			return "", nil, errors.New("Reality user response contains a duplicate user")
		}
		seen[user.User] = struct{}{}
	}
	sort.Slice(response.Users, func(left, right int) bool {
		return response.Users[left].User < response.Users[right].User
	})
	return response.Epoch, response.Users, nil
}

func (client *realityStatsClient) kick(ctx context.Context, userID string) (int, error) {
	parsed, err := uuid.Parse(userID)
	if err != nil || parsed.String() != userID {
		return 0, errors.New("invalid Reality kick target")
	}
	var response struct {
		Closed int `json:"closed"`
	}
	path := "/hyfleet/v1/users/" + url.PathEscape(userID)
	if err := client.doJSON(ctx, http.MethodDelete, path, nil, &response); err != nil {
		return 0, err
	}
	if response.Closed < 0 || response.Closed > maxOnlineConnections {
		return 0, errors.New("Reality kick response contains an invalid connection count")
	}
	return response.Closed, nil
}

func (client *realityStatsClient) doJSON(
	ctx context.Context,
	method, path string,
	body io.Reader,
	destination any,
) error {
	request, err := http.NewRequestWithContext(ctx, method, client.baseURL+path, body)
	if err != nil {
		return fmt.Errorf("create Reality API request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+client.secret)
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Reality API request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Reality API returned HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxStatsResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Reality API response: %w", err)
	}
	if len(payload) > maxStatsResponseBytes {
		return errors.New("Reality API response is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode Reality API response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Reality API response contains trailing data")
	}
	return nil
}

func (agent *Agent) runRealityUsageCycle(ctx context.Context) error {
	var cycleErrors []error
	if agent.realityStatsClient != nil {
		agent.dataPlaneMu.Lock()
		online, sampledAt, err := agent.sampleRealityUsage(ctx, true)
		agent.dataPlaneMu.Unlock()
		if err != nil {
			cycleErrors = append(cycleErrors, err)
		} else if err := agent.postOnlineSnapshot(ctx, online, sampledAt); err != nil {
			cycleErrors = append(cycleErrors, err)
		}
	}
	if err := agent.flushTrafficOutbox(ctx); err != nil {
		cycleErrors = append(cycleErrors, err)
	}
	if agent.realityStatsClient != nil {
		agent.dataPlaneMu.Lock()
		err := agent.executeRealityPendingKicks(ctx)
		agent.dataPlaneMu.Unlock()
		if err != nil {
			cycleErrors = append(cycleErrors, err)
		}
	}
	return errors.Join(cycleErrors...)
}

func (agent *Agent) sampleRealityUsage(
	ctx context.Context,
	updateStatus bool,
) ([]protocol.OnlineUser, time.Time, error) {
	if agent.realityStatsClient == nil {
		return nil, time.Time{}, nil
	}
	counterEpoch, users, err := agent.realityStatsClient.users(ctx)
	if err != nil {
		if updateStatus {
			agent.usage.Available = false
			agent.usage.LastErrorCode = "reality_api_unavailable"
		}
		return nil, time.Time{}, err
	}
	sampledAt := time.Now().UTC()
	counters := make(map[string]trafficCounters, len(users))
	online := make([]protocol.OnlineUser, 0, len(users))
	for _, user := range users {
		counters[user.User] = trafficCounters{TX: user.Upload, RX: user.Download}
		if user.Connections > 0 {
			online = append(online, protocol.OnlineUser{
				UserID: user.User, Connections: user.Connections,
			})
		}
	}
	if _, err := agent.localStore.recordTrafficSample(
		ctx, agent.state.InstallationID, counters, sampledAt, counterEpoch,
	); err != nil {
		if updateStatus {
			agent.usage.Available = false
			agent.usage.LastErrorCode = "traffic_store_failed"
		}
		return nil, time.Time{}, err
	}
	if updateStatus {
		agent.usage.Available = true
		agent.usage.LastErrorCode = ""
		agent.usage.LastSampledAt = &sampledAt
	}
	return online, sampledAt, nil
}

func (agent *Agent) executeRealityPendingKicks(ctx context.Context) error {
	kicks, err := agent.localStore.listPendingKicks(ctx, 256)
	if err != nil || len(kicks) == 0 {
		return err
	}
	if agent.realityStatsClient == nil {
		return errors.New("Reality user control API is not configured")
	}
	for _, kick := range kicks {
		if _, err := agent.realityStatsClient.kick(ctx, kick.UserID); err != nil {
			_ = agent.localStore.recordKickFailure(
				ctx, []pendingKick{kick}, "kick_api_unavailable", time.Now().UTC(),
			)
			return err
		}
		if err := agent.localStore.markKicksApplied(
			ctx, []pendingKick{kick}, time.Now().UTC(),
		); err != nil {
			return err
		}
	}
	return nil
}
