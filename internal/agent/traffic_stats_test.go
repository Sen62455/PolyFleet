package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Sen62455/PolyFleet/internal/protocol"
)

func TestHysteriaStatsClientTrafficOnlineAndKick(t *testing.T) {
	const secret = "local-stats-secret"
	var kicked []string
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != secret {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/traffic":
			_, _ = response.Write([]byte(`{"user-b":{"tx":7,"rx":9},"user-a":{"tx":11,"rx":13}}`))
		case "/online":
			_, _ = response.Write([]byte(`{"user-b":2,"user-a":1}`))
		case "/kick":
			if request.Method != http.MethodPost {
				t.Errorf("kick method = %s", request.Method)
			}
			if err := json.NewDecoder(request.Body).Decode(&kicked); err != nil {
				t.Errorf("decode kick body: %v", err)
			}
			_, _ = response.Write([]byte(`{}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()

	client := newHysteriaStatsClient(server.URL, secret)
	traffic, err := client.traffic(context.Background())
	if err != nil || traffic["user-a"] != (trafficCounters{TX: 11, RX: 13}) {
		t.Fatalf("traffic = %#v, error = %v", traffic, err)
	}
	online, err := client.online(context.Background())
	wantOnline := []protocol.OnlineUser{
		{UserID: "user-a", Connections: 1},
		{UserID: "user-b", Connections: 2},
	}
	if err != nil || !reflect.DeepEqual(online, wantOnline) {
		t.Fatalf("online = %#v, error = %v", online, err)
	}
	if err := client.kick(context.Background(), []string{"user-a", "user-b"}); err != nil {
		t.Fatalf("kick() error = %v", err)
	}
	if !reflect.DeepEqual(kicked, []string{"user-a", "user-b"}) {
		t.Fatalf("kicked = %#v", kicked)
	}
}

func TestHysteriaStatsClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte(`{}` + strings.Repeat(" ", maxStatsResponseBytes)))
	}))
	defer server.Close()
	client := newHysteriaStatsClient(server.URL, "secret")
	if _, err := client.traffic(context.Background()); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("traffic() error = %v, want response size error", err)
	}
}
