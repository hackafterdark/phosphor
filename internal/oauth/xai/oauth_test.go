package xai

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// withStubAuth points the package at a stub server for the duration of fn.
func withStubAuth(t *testing.T, h http.HandlerFunc, fn func()) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)

	old := authBase
	authBase = srv.URL
	t.Cleanup(func() { authBase = old })

	fn()
}

func TestRequestDeviceCode(t *testing.T) {

	withStubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/oauth2/device/code", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		require.NoError(t, r.ParseForm())
		require.Equal(t, clientID, r.FormValue("client_id"))
		require.Contains(t, r.FormValue("scope"), "api:access")
		require.Contains(t, r.FormValue("scope"), "offline_access")

		fmt.Fprint(w, `{"device_code":"dc-123","user_code":"USER-1","verification_uri":"https://x.ai/activate","expires_in":900,"interval":0}`)
	}, func() {
		dc, err := RequestDeviceCode(context.Background())
		require.NoError(t, err)
		require.Equal(t, "dc-123", dc.DeviceCode)
		require.Equal(t, "USER-1", dc.UserCode)
		require.Equal(t, "https://x.ai/activate", dc.VerificationURI)
	})
}

func TestPollForToken_AuthorizationPending(t *testing.T) {

	var calls atomic.Int32
	withStubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, `{"error":"authorization_pending"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"tok","refresh_token":"ref","expires_in":3600}`)
	}, func() {
		// Interval is clamped to a 5s floor, so this waits ~5s on the
		// first (pending) poll before the second succeeds.
		dc := &DeviceCode{DeviceCode: "dc", ExpiresIn: 60, Interval: 0}
		tok, err := PollForToken(context.Background(), dc)
		require.NoError(t, err)
		require.Equal(t, "tok", tok.AccessToken)
		require.Equal(t, "ref", tok.RefreshToken)
		require.Greater(t, tok.ExpiresAt, int64(0))
	})
}

func TestPollForToken_AuthDenied(t *testing.T) {

	withStubAuth(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"error":"access_denied"}`)
	}, func() {
		dc := &DeviceCode{DeviceCode: "dc", ExpiresIn: 60, Interval: 0}
		_, err := PollForToken(context.Background(), dc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "access_denied")
	})
}

func TestRefreshToken_Success(t *testing.T) {

	withStubAuth(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/oauth2/token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, "refresh_token", r.FormValue("grant_type"))
		require.Equal(t, "old-ref", r.FormValue("refresh_token"))

		fmt.Fprint(w, `{"access_token":"new-tok","refresh_token":"new-ref","expires_in":3600}`)
	}, func() {
		tok, err := RefreshToken(context.Background(), "old-ref")
		require.NoError(t, err)
		require.Equal(t, "new-tok", tok.AccessToken)
		require.Equal(t, "new-ref", tok.RefreshToken)
	})
}

func TestRefreshToken_NotAvailable(t *testing.T) {

	withStubAuth(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"error":"forbidden"}`)
	}, func() {
		_, err := RefreshToken(context.Background(), "ref")
		require.ErrorIs(t, err, ErrNotAvailable)
	})
}

func TestRefreshToken_Error(t *testing.T) {

	withStubAuth(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant"}`)
	}, func() {
		_, err := RefreshToken(context.Background(), "ref")
		require.Error(t, err)
		require.True(t, strings.Contains(err.Error(), "invalid_grant"))
	})
}
