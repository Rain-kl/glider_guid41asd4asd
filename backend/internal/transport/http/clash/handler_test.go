package clash_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	clashapp "github.com/Rain-kl/Foam/backend/internal/application/clash"
	"github.com/Rain-kl/Foam/backend/internal/infra/persistence/relational"
	clashhttp "github.com/Rain-kl/Foam/backend/internal/transport/http/clash"
	"github.com/gin-gonic/gin"
)

func setupTestServer(t *testing.T) (*gin.Engine, *clashapp.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	ctx := context.Background()
	tmpDir := t.TempDir()
	db, err := relational.OpenSQLite(ctx, tmpDir+"/test.db")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}

	if err := db.InitializeSchema(ctx); err != nil {
		t.Fatalf("failed to initialize schema: %v", err)
	}

	sourceRepo := relational.NewSourceConfigRepository(db)
	nodeRepo := relational.NewProxyNodeRepository(db)
	testResRepo := relational.NewNodeTestResultRepository(db)
	profileRepo := relational.NewPortProfileRepository(db)
	tplRepo := relational.NewPortProfileTemplateRepository(db)
	rtRepo := relational.NewRuntimeConfigRepository(db)
	kernelRepo := relational.NewKernelInstanceRepository(db)

	svc := clashapp.NewService(sourceRepo, nodeRepo, testResRepo, profileRepo, tplRepo, rtRepo, kernelRepo, nil)
	handler := clashhttp.NewHandler(svc)

	engine := gin.New()
	adminGroup := engine.Group("/api/v1/admin")
	handler.Register(adminGroup)

	return engine, svc
}

func TestUploadConfigAndListNodesHTTP(t *testing.T) {
	engine, _ := setupTestServer(t)

	uploadBody := map[string]string{
		"filename": "test.yaml",
		"raw_content": `
proxies:
  - name: "Node-HTTP-1"
    type: ss
    server: 8.8.8.8
    port: 8388
    cipher: aes-256-gcm
    password: "pass"
`,
	}
	bodyBytes, _ := json.Marshal(uploadBody)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/clash/source-configs/upload", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected HTTP 201 Created, got %d, body=%s", rec.Code, rec.Body.String())
	}

	var res struct {
		ErrorMsg string `json:"error_msg"`
		Data     struct {
			Config struct {
				ID int `json:"id"`
			} `json:"config"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("failed to parse json response: %v", err)
	}

	if res.Data.Config.ID <= 0 {
		t.Fatalf("expected positive config ID, got %d", res.Data.Config.ID)
	}

	// Confirm
	confirmReq := httptest.NewRequest(http.MethodPost, "/api/v1/admin/clash/source-configs/1/confirm", nil)
	confirmRec := httptest.NewRecorder()
	engine.ServeHTTP(confirmRec, confirmReq)

	if confirmRec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", confirmRec.Code)
	}

	// List nodes
	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/admin/clash/nodes", nil)
	listRec := httptest.NewRecorder()
	engine.ServeHTTP(listRec, listReq)

	if listRec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d", listRec.Code)
	}
}

func TestInspectKernelHTTP_MissingBinary(t *testing.T) {
	engine, _ := setupTestServer(t)

	missing := t.TempDir() + "/no-such-mihomo"
	body, err := json.Marshal(map[string]string{"install_path": missing})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/clash/kernels/inspect", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("InspectKernel HTTP status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("未找到 Mihomo 二进制文件")) {
		t.Fatalf("InspectKernel body = %s, want missing-binary message", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("未找到 Mihomo 二进制文件: mihomo")) {
		t.Fatalf("InspectKernel still reports bare mihomo: %s", rec.Body.String())
	}
}

func TestInspectKernelHTTP_InvalidBinaryIsBadRequest(t *testing.T) {
	engine, _ := setupTestServer(t)

	dummy := filepath.Join(t.TempDir(), "mihomo")
	if err := os.WriteFile(dummy, []byte("not-mihomo"), 0o755); err != nil {
		t.Fatalf("write dummy binary: %v", err)
	}
	body, err := json.Marshal(map[string]string{"install_path": dummy})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/clash/kernels/inspect", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("InspectKernel HTTP status = %d, want 400, body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("internalError")) {
		t.Fatalf("InspectKernel treated invalid binary as 500: %s", rec.Body.String())
	}
}

func TestKernelStatusHTTP(t *testing.T) {
	engine, _ := setupTestServer(t)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/clash/runtime/status", nil)
	rec := httptest.NewRecorder()

	engine.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected HTTP 200, got %d, body=%s", rec.Code, rec.Body.String())
	}
}
