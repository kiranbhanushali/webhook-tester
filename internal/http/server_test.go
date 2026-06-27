package http_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"gh.tarampamp.am/webhook-tester/v2/internal/config"
	appHttp "gh.tarampamp.am/webhook-tester/v2/internal/http"
	"gh.tarampamp.am/webhook-tester/v2/internal/pubsub"
	"gh.tarampamp.am/webhook-tester/v2/internal/storage"
)

func TestServer_StartHTTP(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		log = zap.NewNop()
		srv = appHttp.NewServer(ctx, log)
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	const webhookResponse = "CAPTURED !!! OLOLO"

	sID, err := db.NewSession(ctx, storage.Session{
		Code:         http.StatusExpectationFailed,
		ResponseBody: []byte(webhookResponse),
		Headers:      []storage.HttpHeader{{Name: "Content-Type", Value: "text/someShit"}},
	})
	require.NoError(t, err)

	rID, err := db.NewRequest(ctx, sID, storage.Request{})
	require.NoError(t, err)

	srv.Register(
		context.Background(),
		log,
		func(context.Context) error { return nil },
		func(context.Context) (string, error) { return "v1.0.0", nil },
		&config.AppSettings{},
		db,
		pubsub.NewInMemory[pubsub.RequestEvent](),
		nil, // extractor (Task 10 wires a real one)
		nil, // hot index (Task 10 wires a real one)
		time.Second,
		false,
	)

	var baseUrl, stop = startServer(t, ctx, srv)

	t.Cleanup(stop)

	t.Run("index", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "GET", baseUrl)

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, string(body), "<html")
		require.Contains(t, headers.Get("Content-Type"), "text/html")
	})

	t.Run("robots.txt", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "GET", baseUrl+"/////robots.txt")

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, string(body), "User-agent")
		require.Contains(t, string(body), "Disallow")
		require.Contains(t, headers.Get("Content-Type"), "text/plain")
	})

	t.Run("SPA 404", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "GET", baseUrl+"/foo-404")

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, string(body), "<html")
		require.Contains(t, headers.Get("Content-Type"), "text/html")
	})

	t.Run("API 404", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "GET", baseUrl+"/////api/foo-404")

		require.Equal(t, http.StatusNotFound, status)
		require.Contains(t, string(body), "not found")
		require.Contains(t, headers.Get("Content-Type"), "application/json")
	})

	t.Run("ready handler (outside /api prefix)", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "GET", baseUrl+"/ready")

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, string(body), "OK")
		require.Contains(t, headers.Get("Content-Type"), "text/plain")
	})

	t.Run("api handler", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "GET", baseUrl+"/api/settings")

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, string(body), "{")
		require.Contains(t, headers.Get("Content-Type"), "application/json")
	})

	t.Run("webhook capture", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "POST", baseUrl+"/w/"+sID)

		require.Equal(t, http.StatusExpectationFailed, status)
		require.Contains(t, string(body), webhookResponse)
		require.Contains(t, headers.Get("Content-Type"), "text/someShit")
		require.Equal(t, headers.Get("Access-Control-Allow-Origin"), "*")
		require.Equal(t, headers.Get("Access-Control-Allow-Methods"), "*")
		require.Equal(t, headers.Get("Access-Control-Allow-Headers"), "*")
	})

	t.Run("API routes exists", func(t *testing.T) {
		t.Parallel()

		for i, params := range []struct{ method, url string }{ // order matters
			{http.MethodPost, "/api/session"},
			{http.MethodGet, "/api/session/" + sID},
			{http.MethodGet, "/api/sessions"},
			{http.MethodGet, "/api/search?value=anything"},
			{http.MethodPatch, "/api/session/" + sID},
			{http.MethodGet, "/api/session/" + sID + "/requests"},
			{http.MethodGet, "/api/session/" + sID + "/requests/subscribe"},
			{http.MethodGet, "/api/firehose/subscribe"},
			{http.MethodGet, "/api/session/" + sID + "/requests/" + rID},
			{http.MethodPost, "/api/session/" + sID + "/requests/" + rID + "/replay"},
			{http.MethodGet, "/api/settings"},
			{http.MethodGet, "/api/version"},
			{http.MethodGet, "/api/version/latest"},
			{http.MethodDelete, "/api/session/" + sID + "/requests/" + rID},
			{http.MethodDelete, "/api/session/" + sID + "/requests"},
			{http.MethodDelete, "/api/session/" + sID},
		} {
			t.Run(fmt.Sprintf("(%d) %s %s", i, params.method, params.url), func(t *testing.T) {
				var status, body, headers = sendRequest(t, params.method, baseUrl+params.url)

				require.NotEqual(t, http.StatusNotFound, status)
				require.NotEmpty(t, body)
				require.Contains(t, headers.Get("Content-Type"), "application/json")
			})
		}
	})
}

func TestServer_PublicURLRoot(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		log = zap.NewNop()
		srv = appHttp.NewServer(ctx, log)
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	// Configure PublicURLRoot
	publicURLRoot, err := url.Parse("https://example.com")
	require.NoError(t, err)

	srv.Register(
		context.Background(),
		log,
		func(context.Context) error { return nil },
		func(context.Context) (string, error) { return "v1.0.0", nil },
		&config.AppSettings{PublicURLRoot: publicURLRoot},
		db,
		pubsub.NewInMemory[pubsub.RequestEvent](),
		nil, // extractor (Task 10 wires a real one)
		nil, // hot index (Task 10 wires a real one)
		time.Second,
		false,
	)

	var baseUrl, stop = startServer(t, ctx, srv)

	t.Cleanup(stop)

	t.Run("api settings includes public_url_root", func(t *testing.T) {
		t.Parallel()

		var status, body, headers = sendRequest(t, "GET", baseUrl+"/api/settings")

		require.Equal(t, http.StatusOK, status)
		require.Contains(t, headers.Get("Content-Type"), "application/json")
		require.Contains(t, string(body), `"public_url_root":"https://example.com"`)
	})
}

// TestServer_FirehoseIsAuthGated proves the firehose WebSocket lives under /api/ and is therefore
// gated by the shared-token auth middleware: with a token configured, an unauthenticated request to
// /api/firehose/subscribe is rejected with 401 (not 404 — i.e. the route exists and is protected).
func TestServer_FirehoseIsAuthGated(t *testing.T) {
	t.Parallel()

	var (
		ctx = context.Background()
		log = zap.NewNop()
		srv = appHttp.NewServer(ctx, log)
		db  = storage.NewInMemory(time.Minute, 8)
	)

	t.Cleanup(func() { require.NoError(t, db.Close()) })

	srv.Register(
		context.Background(),
		log,
		func(context.Context) error { return nil },
		func(context.Context) (string, error) { return "v1.0.0", nil },
		&config.AppSettings{AuthToken: "super-secret"}, // auth ENABLED
		db,
		pubsub.NewInMemory[pubsub.RequestEvent](),
		nil, // extractor
		nil, // hot index
		time.Second,
		false,
	)

	var baseUrl, stop = startServer(t, ctx, srv)

	t.Cleanup(stop)

	t.Run("no token ⇒ 401 (gated, route exists)", func(t *testing.T) {
		t.Parallel()

		var status, body, _ = sendRequest(t, http.MethodGet, baseUrl+"/api/firehose/subscribe")

		require.Equal(t, http.StatusUnauthorized, status)
		require.NotEqual(t, http.StatusNotFound, status, "route must be registered under /api/")
		require.Contains(t, string(body), "unauthorized")
	})

	t.Run("valid bearer token passes the gate", func(t *testing.T) {
		t.Parallel()

		// With a valid token the auth gate is cleared; the request then reaches the WS handler
		// wrapper, which (lacking the WebSocket upgrade headers) reports a 400 — NOT 401/404.
		var status, _, _ = sendRequest(t, http.MethodGet, baseUrl+"/api/firehose/subscribe",
			map[string]string{"Authorization": "Bearer super-secret"},
		)

		require.NotEqual(t, http.StatusUnauthorized, status)
		require.NotEqual(t, http.StatusNotFound, status)
	})
}

// sendRequest is a helper function to send an HTTP request and return its status code, body, and headers.
func sendRequest(t *testing.T, method, url string, headers ...map[string]string) (
	status int,
	body []byte,
	_ http.Header,
) {
	t.Helper()

	req, reqErr := http.NewRequest(method, url, nil)

	require.NoError(t, reqErr)

	if len(headers) > 0 {
		for key, value := range headers[0] {
			req.Header.Add(key, value)
		}
	}

	resp, err := http.DefaultClient.Do(req) //nolint:gosec
	require.NoError(t, err)

	body, _ = io.ReadAll(resp.Body)

	require.NoError(t, resp.Body.Close())

	return resp.StatusCode, body, resp.Header
}

// startServer is a helper function to start an HTTP server and return its base URL.
func startServer(t *testing.T, pCtx context.Context, srv interface {
	StartHTTP(ctx context.Context, ln net.Listener) error
}) (string /* baseurl */, func() /* stop */) {
	t.Helper()

	var (
		port     = getFreeTcpPort(t)
		hostPort = net.JoinHostPort("127.0.0.1", fmt.Sprintf("%d", port)) //nolint:govet
	)

	// open HTTP port
	ln, lnErr := net.Listen("tcp", hostPort)
	require.NoError(t, lnErr)

	var ctx, cancel = context.WithCancel(pCtx)

	go func() {
		err := srv.StartHTTP(ctx, ln)
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			require.NoError(t, err)
		}
	}()

	// wait until the server starts
	for {
		if conn, err := net.DialTimeout("tcp", hostPort, time.Second); err == nil {
			require.NoError(t, conn.Close())

			break
		}

		<-time.After(5 * time.Millisecond)
	}

	return fmt.Sprintf("http://%s", hostPort), cancel
}

// getFreeTcpPort is a helper function to get a free TCP port number.
func getFreeTcpPort(t *testing.T) uint16 {
	t.Helper()

	l, lErr := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, lErr)

	port := l.Addr().(*net.TCPAddr).Port
	require.NoError(t, l.Close())

	// make sure port is closed
	for {
		conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err != nil {
			break
		}

		require.NoError(t, conn.Close())
		<-time.After(5 * time.Millisecond)
	}

	return uint16(port) //nolint:gosec
}
