package workspace

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/lingdoc/delivery"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestServeIsolatedHTTP is an opt-in local acceptance process, never a production
// composition root. It uses a fixed local test actor. LINGDOC_SQLITE_PATH opts
// into a durable copy of the same fixture; an existing database is never seeded.
// Normal test runs skip it; the local launcher/verification explicitly enable it.
func TestServeIsolatedHTTP(t *testing.T) {
	if os.Getenv("LINGDOC_ISOLATED_HTTP_LISTEN") != "1" {
		t.Skip("set LINGDOC_ISOLATED_HTTP_LISTEN=1 only for the isolated local acceptance process")
	}
	addr := os.Getenv("LINGDOC_ISOLATED_HTTP_ADDR")
	if addr == "" {
		addr = "127.0.0.1:5320"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		t.Fatal("isolated DOCX HTTP must bind to loopback")
	}
	dsOrigin := os.Getenv("LINGDOC_DS_ORIGIN")
	if dsOrigin != "" && !isolatedLoopbackOrigin(dsOrigin) {
		t.Fatal("LINGDOC_DS_ORIGIN must be an exact HTTP loopback origin with an explicit port")
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var f candidateOneConnFixture
	var deliveryService *DOCXDeliveryService
	databasePath := os.Getenv("LINGDOC_SQLITE_PATH")
	if databasePath == "" {
		f, deliveryService = newDOCXDeliveryFixture(t)
	} else {
		f, deliveryService = openIsolatedDOCXDelivery(t, databasePath)
	}
	router := gin.New()
	if dsOrigin != "" {
		router.Use(isolatedDOCXCORS(dsOrigin))
	}
	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true, "candidate": "updf-workspace-r1-20261026", "project": "project-1",
			"persistent": databasePath != "", "runtimeId": os.Getenv("LINGDOC_ISOLATED_HTTP_RUNTIME_ID"), "pid": os.Getpid()})
	})
	router.Use(func(c *gin.Context) {
		ctx := context.WithValue(c.Request.Context(), types.UserIDContextKey, "reader")
		ctx = context.WithValue(ctx, types.TenantIDContextKey, uint64(7))
		ctx = types.WithCaller(ctx, types.Caller{TenantID: 7, UserID: "reader", Role: types.TenantRoleOwner})
		ctx = types.WithPrincipal(ctx, types.Principal{Type: types.PrincipalWebUser, ID: "reader"})
		ctx = types.WithExecutionTenant(ctx, 7)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	(&DOCXHTTPHandler{Runtime: f.runtime, Delivery: deliveryService}).Register(router)
	server := &http.Server{Handler: router}
	shutdown := make(chan struct{})
	var shutdownOnce sync.Once
	router.POST("/shutdown", func(c *gin.Context) {
		if c.RemoteIP() != "127.0.0.1" && c.RemoteIP() != "::1" {
			c.JSON(http.StatusForbidden, gin.H{"error": "PERMISSION_DENIED"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ok": true})
		shutdownOnce.Do(func() { close(shutdown) })
	})
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- server.Serve(listener) }()
	fmt.Printf("ISOLATED_DOCX_READY http://%s/healthz\n", listener.Addr())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-shutdown:
	case <-signals:
	case err := <-serveErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatal(err)
		}
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
}

func isolatedLoopbackOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return false
	}
	if host := u.Hostname(); host != "127.0.0.1" && host != "localhost" && host != "::1" {
		return false
	}
	port, err := strconv.Atoi(u.Port())
	return err == nil && port > 0 && port <= 65535
}

// The native DS host has its own port. Allow only that configured origin;
// command-line requests without Origin remain available for the local launcher.
func isolatedDOCXCORS(origin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Vary", "Origin")
		requestOrigin := c.GetHeader("Origin")
		if requestOrigin != "" && requestOrigin != origin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "ORIGIN_NOT_ALLOWED"})
			return
		}
		if requestOrigin == origin {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Access-Control-Expose-Headers", "Content-Disposition")
		}
		if c.Request.Method == http.MethodOptions {
			method := c.GetHeader("Access-Control-Request-Method")
			if requestOrigin != origin || (method != http.MethodGet && method != http.MethodPost) {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			for _, header := range strings.Split(c.GetHeader("Access-Control-Request-Headers"), ",") {
				switch strings.ToLower(strings.TrimSpace(header)) {
				case "", "content-type", "idempotency-key", "accept":
				default:
					c.AbortWithStatus(http.StatusForbidden)
					return
				}
			}
			c.Header("Access-Control-Allow-Methods", "GET, POST")
			c.Header("Access-Control-Allow-Headers", "Content-Type, Idempotency-Key, Accept")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

func TestIsolatedDOCXPersistentHTTPRestart(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "lingdoc.db")
	first, stop := startIsolatedDOCXTestProcess(t, databasePath)
	const prefix = "/api/docx/projects/project-1"
	request := func(origin, method, endpoint, key string, body any, want int, target any) []byte {
		t.Helper()
		var raw []byte
		if body != nil {
			var err error
			raw, err = json.Marshal(body)
			require.NoError(t, err)
		}
		req, err := http.NewRequest(method, origin+prefix+endpoint, bytes.NewReader(raw))
		require.NoError(t, err)
		req.Header.Set("Origin", "http://127.0.0.1:57123")
		req.Header.Set("Content-Type", "application/json")
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		require.NoError(t, err)
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, want, response.StatusCode, string(data))
		require.Equal(t, req.Header.Get("Origin"), response.Header.Get("Access-Control-Allow-Origin"))
		if target != nil {
			require.NoError(t, json.Unmarshal(data, target), string(data))
		}
		return data
	}
	preflight, err := http.NewRequest(http.MethodOptions, first+prefix+"/versions", nil)
	require.NoError(t, err)
	preflight.Header.Set("Origin", "http://127.0.0.1:57123")
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "content-type,idempotency-key")
	response, err := (&http.Client{Timeout: 3 * time.Second}).Do(preflight)
	require.NoError(t, err)
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	require.Equal(t, preflight.Header.Get("Origin"), response.Header.Get("Access-Control-Allow-Origin"))
	_ = response.Body.Close()
	preflight.Header.Set("Origin", "http://127.0.0.1:57124")
	response, err = (&http.Client{Timeout: 3 * time.Second}).Do(preflight)
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, response.StatusCode)
	require.Empty(t, response.Header.Get("Access-Control-Allow-Origin"))
	_ = response.Body.Close()
	request(first, "GET", "/versions/latest", "", nil, 404, nil)
	data := docxFixtureWithAllBodies(t, "persistent-http-v1")
	body := map[string]any{"expectedDiskVersion": 0, "data": base64.StdEncoding.EncodeToString(data)}
	var saved, replay docxHTTPSaveResponse
	request(first, "POST", "/versions", "persistent-save-1", body, 200, &saved)
	require.False(t, saved.Replayed)
	request(first, "POST", "/versions", "persistent-save-1", body, 200, &replay)
	require.True(t, replay.Replayed)
	require.Equal(t, saved.Version.ID, replay.Version.ID)
	body["expectedDiskVersion"] = 1
	request(first, "POST", "/versions", "persistent-save-1", body, 409, nil)
	var status DOCXDeliveryStatus
	request(first, "GET", "/versions/"+saved.Version.ID+"/delivery", "", nil, 200, &status)
	for _, section := range saved.Version.Projection.Sections {
		confirmation := docxHTTPConfirmationRequest(saved.Version, section.SectionID, status.SpecRevision)
		request(first, "POST", "/confirmations", "persistent-confirm-"+section.SectionID, confirmation, 200, nil)
	}
	var frozen DOCXFreezeResult
	request(first, "POST", "/freezes", "persistent-freeze-1", map[string]any{"savedVersionId": saved.Version.ID}, 200, &frozen)
	require.True(t, frozen.DeliveryEligible)
	var exported struct {
		Export delivery.ExportArtifact `json:"export"`
	}
	request(first, "POST", "/exports", "persistent-export-1", map[string]any{"snapshotId": frozen.Snapshot.ID}, 200, &exported)
	require.Equal(t, delivery.ExportVerified, exported.Export.Status)
	require.Equal(t, saved.Version.DocSHA256, exported.Export.FileSHA256)
	require.Equal(t, data, request(first, "GET", "/exports/"+exported.Export.ID, "", nil, 200, nil))
	stop() // waits for the Go process and its temporary fixture cleanup to exit

	second, stopSecond := startIsolatedDOCXTestProcess(t, databasePath)
	defer stopSecond()
	var latest DOCXSavedVersion
	request(second, "GET", "/versions/latest", "", nil, 200, &latest)
	require.Equal(t, saved.Version.ID, latest.ID)
	require.Equal(t, saved.Version.DocSHA256, latest.DocSHA256)
	request(second, "GET", "/versions/"+latest.ID+"/delivery", "", nil, 200, &status)
	require.Len(t, status.Chapters, 6)
	for _, chapter := range status.Chapters {
		require.True(t, chapter.Confirmed)
	}
	require.NotNil(t, status.Snapshot)
	require.Equal(t, frozen.Snapshot.ID, status.Snapshot.ID)
	require.True(t, status.DeliveryEligible)
	require.Equal(t, data, request(second, "GET", "/exports/"+exported.Export.ID, "", nil, 200, nil))
	body["data"] = base64.StdEncoding.EncodeToString(docxFixtureWithAllBodies(t, "persistent-http-v2"))
	var next docxHTTPSaveResponse
	request(second, "POST", "/versions", "persistent-save-2", body, 200, &next)
	require.EqualValues(t, 2, next.Version.DiskVersion)
	require.NotEqual(t, saved.Version.DocSHA256, next.Version.DocSHA256)
	require.Equal(t, data, request(second, "GET", "/exports/"+exported.Export.ID, "", nil, 200, nil))
}

// Spawn the compiled test binary, not an in-process handler or another service
// sharing its connection. This exercises initialization and OS-process restart.
func startIsolatedDOCXTestProcess(t *testing.T, databasePath string) (string, func()) {
	t.Helper()
	reservation, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := reservation.Addr().String()
	require.NoError(t, reservation.Close())
	executable, err := os.Executable()
	require.NoError(t, err)
	logFile, err := os.CreateTemp(t.TempDir(), "isolated-process-*.log")
	require.NoError(t, err)
	cmd := exec.Command(executable, "-test.run=^TestServeIsolatedHTTP$", "-test.v=true")
	cmd.Env = append(os.Environ(), "LINGDOC_ISOLATED_HTTP_LISTEN=1", "LINGDOC_ISOLATED_HTTP_ADDR="+addr,
		"LINGDOC_SQLITE_PATH="+databasePath, "LINGDOC_DS_ORIGIN=http://127.0.0.1:57123", "LINGDOC_ISOLATED_HTTP_RUNTIME_ID="+logFile.Name())
	cmd.Stdout, cmd.Stderr = logFile, logFile
	require.NoError(t, cmd.Start())
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	origin := "http://" + addr
	client := &http.Client{Timeout: 500 * time.Millisecond}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			response, _ := client.Post(origin+"/shutdown", "application/json", nil)
			if response != nil {
				_ = response.Body.Close()
			}
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(6 * time.Second):
				_ = cmd.Process.Kill()
				<-done
				t.Error("isolated process did not stop gracefully")
			}
		})
	}
	t.Cleanup(func() {
		stop()
		_ = logFile.Close()
		if t.Failed() {
			output, _ := os.ReadFile(logFile.Name())
			t.Log(string(output))
		}
	})
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		response, err := client.Get(origin + "/healthz")
		if err == nil {
			var health struct {
				RuntimeID string `json:"runtimeId"`
			}
			decodeErr := json.NewDecoder(response.Body).Decode(&health)
			_ = response.Body.Close()
			if response.StatusCode == 200 && decodeErr == nil && health.RuntimeID == logFile.Name() {
				return origin, stop
			}
		}
		select {
		case err := <-done:
			done <- err // cleanup still owns reaping the process result
			t.Fatalf("isolated process exited before ready: %v", err)
		default:
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("isolated process readiness timed out")
	return "", stop
}
