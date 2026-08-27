package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Sen62455/PolyFleet/internal/cryptoutil"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestValidateWebhookURLRejectsLocalAndAmbiguousTargets(t *testing.T) {
	tests := []string{
		"http://hooks.example.com/path",
		"https://localhost/path",
		"https://127.0.0.1/path",
		"https://[::1]/path",
		"https://user:password@hooks.example.com/path",
		"https://hooks.example.com:8443/path",
		"https://hooks.example.com/path#fragment",
	}
	for _, raw := range tests {
		if _, err := validateWebhookURL(raw); err == nil {
			t.Errorf("validateWebhookURL(%q) unexpectedly succeeded", raw)
		}
	}
	if _, err := validateWebhookURL("https://hooks.example.com/polyfleet"); err != nil {
		t.Fatalf("public HTTPS webhook rejected: %v", err)
	}
}

func TestTelegramTransportFailureDoesNotLeakBotToken(t *testing.T) {
	app := newTestApp(t)
	const notifierID = "notifier-secret-test"
	const botToken = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijk"
	plaintext, err := json.Marshal(notifierSecretConfig{BotToken: botToken, ChatID: "12345678"})
	if err != nil {
		t.Fatalf("encode notifier secret: %v", err)
	}
	ciphertext, err := cryptoutil.Seal(
		bytes.Repeat([]byte{0x42}, 32), plaintext, notificationConfigAD(notifierID),
	)
	if err != nil {
		t.Fatalf("seal notifier secret: %v", err)
	}
	app.application.notifierClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed for " + request.URL.String())
	})}
	_, deliveryErr := app.application.deliverNotification(
		context.Background(), "telegram", notifierID, ciphertext,
		[]byte(`{"event":"test","message":"test"}`),
	)
	if deliveryErr == nil {
		t.Fatal("deliverNotification() unexpectedly succeeded")
	}
	if strings.Contains(deliveryErr.Error(), botToken) || deliveryErr.Error() != "notification transport failed" {
		t.Fatalf("transport error leaked secret or changed contract: %q", deliveryErr)
	}
}

func TestTelegramResponseErrorExplainsInvalidTargetWithoutLeakingSecrets(t *testing.T) {
	app := newTestApp(t)
	const notifierID = "notifier-telegram-error"
	const botToken = "123456789:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijk"
	plaintext, err := json.Marshal(notifierSecretConfig{BotToken: botToken, ChatID: "@polyfleet_bot"})
	if err != nil {
		t.Fatalf("encode notifier secret: %v", err)
	}
	ciphertext, err := cryptoutil.Seal(
		bytes.Repeat([]byte{0x42}, 32), plaintext, notificationConfigAD(notifierID),
	)
	if err != nil {
		t.Fatalf("seal notifier secret: %v", err)
	}
	app.application.notifierClient = &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(
				`{"ok":false,"error_code":400,"description":"Bad Request: chat not found ` + botToken + `"}`,
			)),
		}, nil
	})}
	status, deliveryErr := app.application.deliverNotification(
		context.Background(), "telegram", notifierID, ciphertext,
		[]byte(`{"event":"test","message":"test"}`),
	)
	if status != http.StatusBadRequest {
		t.Fatalf("deliverNotification() status = %d, want %d", status, http.StatusBadRequest)
	}
	if deliveryErr == nil || !strings.Contains(deliveryErr.Error(), "Telegram 找不到目标会话") {
		t.Fatalf("deliverNotification() error = %q, want actionable Telegram target error", deliveryErr)
	}
	if strings.Contains(deliveryErr.Error(), botToken) {
		t.Fatalf("Telegram response error leaked bot token: %q", deliveryErr)
	}
}

func TestTelegramResponseErrorMapsForbiddenPermissions(t *testing.T) {
	err := telegramResponseError(http.StatusForbidden, []byte(
		`{"ok":false,"error_code":403,"description":"Forbidden: bot is not a member of the channel chat"}`,
	))
	if !strings.Contains(err.Error(), "没有向目标会话发消息的权限") {
		t.Fatalf("telegramResponseError() = %q, want permission guidance", err)
	}
}
