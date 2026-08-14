package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

const (
	maxStatsResponseBytes = 2 * 1024 * 1024
	maxStatsUsers         = 10000
	maxOnlineConnections  = 1000000
)

type hysteriaStatsClient struct {
	baseURL string
	secret  string
	client  *http.Client
}

func newHysteriaStatsClient(baseURL, secret string) *hysteriaStatsClient {
	return &hysteriaStatsClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		secret:  secret,
		client:  &http.Client{Timeout: 5 * time.Second},
	}
}

func (client *hysteriaStatsClient) traffic(ctx context.Context) (map[string]trafficCounters, error) {
	var raw map[string]struct {
		TX uint64 `json:"tx"`
		RX uint64 `json:"rx"`
	}
	if err := client.getJSON(ctx, "/traffic", &raw); err != nil {
		return nil, err
	}
	if len(raw) > maxStatsUsers {
		return nil, errors.New("Hysteria traffic response contains too many users")
	}
	result := make(map[string]trafficCounters, len(raw))
	for userID, counters := range raw {
		if !validStatsUserID(userID) || counters.TX > math.MaxInt64 || counters.RX > math.MaxInt64 {
			return nil, errors.New("Hysteria traffic response contains invalid user data")
		}
		// Hysteria defines tx/rx from the server-to-proxy-target perspective.
		// Therefore tx is user upload and rx is user download.
		result[userID] = trafficCounters{TX: int64(counters.TX), RX: int64(counters.RX)}
	}
	return result, nil
}

func (client *hysteriaStatsClient) online(ctx context.Context) ([]protocol.OnlineUser, error) {
	var raw map[string]int
	if err := client.getJSON(ctx, "/online", &raw); err != nil {
		return nil, err
	}
	if len(raw) > maxStatsUsers {
		return nil, errors.New("Hysteria online response contains too many users")
	}
	userIDs := make([]string, 0, len(raw))
	for userID := range raw {
		userIDs = append(userIDs, userID)
	}
	sort.Strings(userIDs)
	result := make([]protocol.OnlineUser, 0, len(userIDs))
	for _, userID := range userIDs {
		connections := raw[userID]
		if !validStatsUserID(userID) || connections < 1 || connections > maxOnlineConnections {
			return nil, errors.New("Hysteria online response contains invalid user data")
		}
		result = append(result, protocol.OnlineUser{UserID: userID, Connections: connections})
	}
	return result, nil
}

func (client *hysteriaStatsClient) kick(ctx context.Context, userIDs []string) error {
	if len(userIDs) == 0 {
		return nil
	}
	if len(userIDs) > maxStatsUsers {
		return errors.New("too many Hysteria kick targets")
	}
	for _, userID := range userIDs {
		if !validStatsUserID(userID) {
			return errors.New("invalid Hysteria kick target")
		}
	}
	payload, err := json.Marshal(userIDs)
	if err != nil {
		return fmt.Errorf("encode Hysteria kick request: %w", err)
	}
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, client.baseURL+"/kick", bytes.NewReader(payload),
	)
	if err != nil {
		return fmt.Errorf("create Hysteria kick request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", client.secret)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Hysteria kick request: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("Hysteria kick returned HTTP %d", response.StatusCode)
	}
	return nil
}

func (client *hysteriaStatsClient) getJSON(ctx context.Context, path string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, client.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("create Hysteria stats request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", client.secret)
	response, err := client.client.Do(request)
	if err != nil {
		return fmt.Errorf("send Hysteria stats request: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("Hysteria stats returned HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxStatsResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read Hysteria stats response: %w", err)
	}
	if len(payload) > maxStatsResponseBytes {
		return errors.New("Hysteria stats response is too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode Hysteria stats response: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("Hysteria stats response contains trailing data")
	}
	return nil
}

func validStatsUserID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if character < 0x21 || character == 0x7f {
			return false
		}
	}
	return true
}
