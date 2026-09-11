package swiftcontrol

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
)

func TestObjectContentUsesCapturedTrust(t *testing.T) {
	for _, tc := range []struct {
		name        string
		customCA    bool
		configureH2 bool
		wantProto   int
	}{
		{name: "captured custom CA", customCA: true, wantProto: 1},
		{name: "explicit HTTP2 transport control", customCA: true, configureH2: true, wantProto: 2},
		{name: "system trust refuses fixture certificate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var authRequests, storageRequests atomic.Int32
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.ProtoMajor != tc.wantProto {
					t.Errorf("observed HTTP protocol %s, want major %d", request.Proto, tc.wantProto)
				}
				if request.URL.Path == "/auth/v1.0" {
					authRequests.Add(1)
					w.Header().Set("X-Storage-Url", "https://"+request.Host+"/v1/AUTH_fixture")
					w.Header().Set("X-Auth-Token", "fixture-token")
					return
				}
				storageRequests.Add(1)
				if request.Header.Get("X-Auth-Token") != "fixture-token" || request.Header.Get("Accept-Encoding") != "identity" {
					t.Error("content request lost its accepted credentials or representation")
				}
				if _, err := io.WriteString(w, "opaque bytes"); err != nil {
					t.Errorf("fixture content: %v", err)
				}
			}))
			server.EnableHTTP2 = true
			server.StartTLS()
			t.Cleanup(server.Close)
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw})
			policy := TLSConfig{Mode: TLSModeSystem}
			if tc.customCA {
				policy = TLSConfig{Mode: TLSModeCustomCA, CACertPEM: ca}
			}
			session, err := New(Config{
				AuthURL: server.URL + "/auth/v1.0", Username: "fixture", APIKey: "fixture", TLS: policy,
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.customCA {
				transport := session.conn.Transport.(redirectRefusingTransport).inner.(*http.Transport)
				t.Cleanup(transport.CloseIdleConnections)
				if tc.configureH2 {
					// Production custom-CA policy currently negotiates HTTP/1.1.
					// This separate control proves the reader on an actual HTTP/2 connection.
					transport.ForceAttemptHTTP2 = true
				}
				clear(ca)
			}
			reader, err := session.OpenObjectContent(t.Context(), "owned", "object")
			if !tc.customCA {
				if reader != nil {
					if closeErr := reader.Close(); closeErr != nil {
						t.Error(closeErr)
					}
				}
				if err == nil || reader != nil || authRequests.Load() != 0 || storageRequests.Load() != 0 {
					t.Fatalf("untrusted transport: reader=%v error=%v auth=%d storage=%d", reader, err, authRequests.Load(), storageRequests.Load())
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			content, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if string(content) != "opaque bytes" || readErr != nil || closeErr != nil || authRequests.Load() != 1 || storageRequests.Load() != 1 {
				t.Fatalf("captured trust read: content=%q read=%v close=%v auth=%d storage=%d", content, readErr, closeErr, authRequests.Load(), storageRequests.Load())
			}
		})
	}
}

func TestObjectContentRechecksAdmissionBetweenDispatches(t *testing.T) {
	refused := errors.New("fixture generation revoked")
	for _, tc := range []struct {
		name         string
		revoke       string
		unauthorized bool
		wantAuth     int32
		wantStorage  int32
		wantAdmit    int32
	}{
		{name: "admitted", wantAuth: 1, wantStorage: 1, wantAdmit: 3},
		{name: "revoked after authentication", revoke: "auth", wantAuth: 1, wantAdmit: 6},
		{name: "admitted retry", unauthorized: true, wantAuth: 2, wantStorage: 2, wantAdmit: 5},
		{name: "revoked after unauthorized storage", revoke: "storage", unauthorized: true, wantAuth: 1, wantStorage: 1, wantAdmit: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var revoked atomic.Bool
			var authRequests, storageRequests, admissions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/auth/v1.0" {
					n := authRequests.Add(1)
					w.Header().Set("X-Storage-Url", "http://"+request.Host+"/v1/AUTH_fixture")
					w.Header().Set("X-Auth-Token", "fixture-"+strconv.Itoa(int(n)))
					if tc.revoke == "auth" {
						revoked.Store(true)
					}
					return
				}
				n := storageRequests.Add(1)
				if request.Header.Get("X-Auth-Token") != "fixture-"+strconv.Itoa(int(authRequests.Load())) {
					t.Error("storage request used the wrong accepted token")
				}
				if tc.unauthorized && n == 1 {
					if tc.revoke == "storage" {
						revoked.Store(true)
					}
					w.WriteHeader(http.StatusUnauthorized)
					return
				}
				if _, err := io.WriteString(w, "data"); err != nil {
					t.Errorf("fixture content: %v", err)
				}
			}))
			t.Cleanup(server.Close)
			session, err := New(Config{
				AuthURL: server.URL + "/auth/v1.0", Username: "fixture", APIKey: "fixture",
				AdmitRequest: func(context.Context) error {
					admissions.Add(1)
					if revoked.Load() {
						return refused
					}
					return nil
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			reader, openErr := session.OpenObjectContent(t.Context(), "owned", "object")
			var content []byte
			var readErr, closeErr error
			if reader != nil {
				content, readErr = io.ReadAll(reader)
				closeErr = reader.Close()
			}
			if tc.revoke != "" {
				if reader != nil || !errors.Is(openErr, refused) {
					t.Errorf("revocation lost: reader=%v open=%v", reader, openErr)
				}
			} else if openErr != nil || string(content) != "data" || readErr != nil || closeErr != nil {
				t.Errorf("admitted content: open=%v content=%q read=%v close=%v", openErr, content, readErr, closeErr)
			}
			if authRequests.Load() != tc.wantAuth || storageRequests.Load() != tc.wantStorage || admissions.Load() != tc.wantAdmit {
				t.Fatalf("dispatch boundary: auth=%d/%d storage=%d/%d admission=%d/%d", authRequests.Load(), tc.wantAuth, storageRequests.Load(), tc.wantStorage, admissions.Load(), tc.wantAdmit)
			}
		})
	}
}

func TestObjectContentRefusesFailedNativeRefresh(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(strconv.FormatBool(force), func(t *testing.T) {
			var authRequests, storageRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path != "/auth/v1.0" {
					storageRequests.Add(1)
					t.Error("failed refresh reached storage")
					return
				}
				if authRequests.Add(1) != 1 {
					w.WriteHeader(http.StatusForbidden)
					return
				}
				w.Header().Set("X-Storage-Url", "http://"+request.Host+"/v1/AUTH_fixture")
				w.Header().Set("X-Auth-Token", "old-fixture-token")
				w.Header().Set("X-Auth-Token-Expires", "3600")
			}))
			t.Cleanup(server.Close)
			session, err := New(Config{AuthURL: server.URL + "/auth/v1.0", Username: "fixture", APIKey: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			if err := session.Authenticate(t.Context()); err != nil {
				t.Fatal(err)
			}
			if force {
				session.ForceReauth()
			} else {
				nearExpiryObjectContentCredentials(session)
			}
			priorAge := session.authedAt
			reader, err := session.OpenObjectContent(t.Context(), "owned", "object")
			if reader != nil {
				if closeErr := reader.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			}
			if reader != nil || err == nil || authRequests.Load() != 2 || storageRequests.Load() != 0 {
				t.Fatalf("failed refresh reused old credentials: reader=%v err=%v auth=%d storage=%d", reader, err, authRequests.Load(), storageRequests.Load())
			}
			if !session.authedAt.Equal(priorAge) || session.auth.snapshot().token != "old-fixture-token" {
				t.Fatal("failed native refresh changed the explicit refresh age or accepted snapshot")
			}
		})
	}
}
