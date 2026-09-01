package clash_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Rain-kl/Foam/backend/internal/application/clash"
	domainclash "github.com/Rain-kl/Foam/backend/internal/domain/clash"
	settingsdomain "github.com/Rain-kl/Foam/backend/internal/domain/settings"
	"github.com/Rain-kl/Foam/backend/internal/infra/persistence/relational"
)

func setupTestService(t *testing.T) (*clash.Service, context.Context) {
	t.Helper()
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

	svc := clash.NewService(sourceRepo, nodeRepo, testResRepo, profileRepo, tplRepo, rtRepo, kernelRepo, nil)
	return svc, ctx
}

func setupTestServiceWithSettings(t *testing.T) (*clash.Service, context.Context, *relational.RuntimeSettingsRepository) {
	t.Helper()
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
	settingsRepo := relational.NewRuntimeSettingsRepository(db)

	svc := clash.NewService(sourceRepo, nodeRepo, testResRepo, profileRepo, tplRepo, rtRepo, kernelRepo, settingsRepo)
	return svc, ctx, settingsRepo
}

func importOneTestNode(t *testing.T, svc *clash.Service, ctx context.Context) int {
	t.Helper()
	yamlContent := `
proxies:
  - name: "Node-1"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
`
	cfg, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "nodes.yaml",
		RawContent: yamlContent,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig: %v", err)
	}
	if _, err := svc.ConfirmSourceConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("ConfirmSourceConfig: %v", err)
	}
	nodes, _, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{})
	if err != nil || len(nodes) == 0 {
		t.Fatalf("ListNodes: err=%v len=%d", err, len(nodes))
	}
	return nodes[0].ID
}

func TestUploadSourceConfigAndConfirm(t *testing.T) {
	svc, ctx := setupTestService(t)

	validYAML := `
proxies:
  - name: "Node-1"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
  - name: "Node-2"
    type: vmess
    server: 2.2.2.2
    port: 443
    uuid: "a3b82384-143f-423c-a612-42171171a812"
`

	cfg, parseRes, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "test_subscription.yaml",
		RawContent: validYAML,
		UploadedBy: "admin",
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig failed: %v", err)
	}

	if cfg.ID <= 0 {
		t.Errorf("expected positive ID, got %d", cfg.ID)
	}
	if len(parseRes.Nodes) != 2 {
		t.Errorf("expected 2 parsed nodes, got %d", len(parseRes.Nodes))
	}

	// Confirm import
	importedCount, err := svc.ConfirmSourceConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("ConfirmSourceConfig failed: %v", err)
	}

	if importedCount != 2 {
		t.Errorf("expected 2 imported nodes, got %d", importedCount)
	}

	// Verify nodes listed
	nodes, total, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{})
	if err != nil {
		t.Fatalf("ListNodes failed: %v", err)
	}
	if total != 2 || len(nodes) != 2 {
		t.Errorf("expected 2 total nodes in pool, got total=%d, len=%d", total, len(nodes))
	}
}

func TestDuplicateNodesInSameFile(t *testing.T) {
	svc, ctx := setupTestService(t)

	// Single YAML containing TWO identical node definitions
	dupYAML := `
proxies:
  - name: "Node-Dup"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
  - name: "Node-Dup"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
`

	cfg, parseRes, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "dup.yaml",
		RawContent: dupYAML,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig with duplicate YAML failed: %v", err)
	}

	if cfg.DuplicateNodes != 1 {
		t.Errorf("expected 1 duplicate node detected, got %d", cfg.DuplicateNodes)
	}
	if len(parseRes.Nodes) != 2 {
		t.Errorf("expected 2 parsed nodes in file, got %d", len(parseRes.Nodes))
	}

	// Confirm import must succeed without "duplicated key not allowed" error
	importedCount, err := svc.ConfirmSourceConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("ConfirmSourceConfig with duplicates failed: %v", err)
	}

	if importedCount != 1 {
		t.Errorf("expected 1 unique imported node, got %d", importedCount)
	}
}

func TestPortProfileCRUDAndRenderPreview(t *testing.T) {
	svc, ctx := setupTestService(t)

	// Upload & Confirm node
	validYAML := `
proxies:
  - name: "HK-Node-01"
    type: ss
    server: 1.2.3.4
    port: 8388
    cipher: aes-128-gcm
    password: "pass"
`
	cfg, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "hk.yaml",
		RawContent: validYAML,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig failed: %v", err)
	}
	_, err = svc.ConfirmSourceConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("ConfirmSourceConfig failed: %v", err)
	}

	nodes, _, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{})
	if err != nil || len(nodes) == 0 {
		t.Fatalf("ListNodes failed or empty: %v", err)
	}

	// Create Port Profile
	profileView, err := svc.CreatePortProfile(ctx, &domainclash.PortProfile{
		Name:       "Test-Profile-7890",
		ListenHost: "127.0.0.1",
		MixedPort:  7890,
		KernelType: "mihomo",
		ProxySettings: domainclash.PortProfileProxySettings{
			StrategyType: domainclash.PortProfileStrategySelect,
		},
	}, []int{nodes[0].ID})
	if err != nil {
		t.Fatalf("CreatePortProfile failed: %v", err)
	}

	if profileView.Profile.ID <= 0 {
		t.Errorf("expected positive profile ID, got %d", profileView.Profile.ID)
	}

	// Render preview
	renderRes, err := svc.RenderProfilePreview(ctx, profileView.Profile.ID)
	if err != nil {
		t.Fatalf("RenderProfilePreview failed: %v", err)
	}

	if renderRes.Checksum == "" || renderRes.Content == "" {
		t.Errorf("expected rendered checksum and content, got checksum=%s", renderRes.Checksum)
	}
}

func TestDeleteSourceConfigCascadesNodesAndBindings(t *testing.T) {
	svc, ctx := setupTestService(t)

	yamlA := `
proxies:
  - name: "A-Node"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
`
	cfg, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "a.yaml",
		RawContent: yamlA,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig failed: %v", err)
	}
	if _, err := svc.ConfirmSourceConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("ConfirmSourceConfig failed: %v", err)
	}

	nodes, total, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{SourceConfigID: cfg.ID})
	if err != nil || total != 1 || len(nodes) != 1 {
		t.Fatalf("ListNodes by source: total=%d len=%d err=%v", total, len(nodes), err)
	}
	nodeID := nodes[0].ID

	profileView, err := svc.CreatePortProfile(ctx, &domainclash.PortProfile{
		Name:       "Cascade-Profile",
		ListenHost: "127.0.0.1",
		MixedPort:  17890,
		KernelType: "mihomo",
		ProxySettings: domainclash.PortProfileProxySettings{
			StrategyType: domainclash.PortProfileStrategySelect,
		},
	}, []int{nodeID})
	if err != nil {
		t.Fatalf("CreatePortProfile failed: %v", err)
	}

	if err := svc.DeleteSourceConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("DeleteSourceConfig failed: %v", err)
	}

	if _, err := svc.GetSourceConfig(ctx, cfg.ID); err == nil {
		t.Fatal("GetSourceConfig after delete: want not found")
	}

	nodes, total, err = svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{SourceConfigID: cfg.ID})
	if err != nil {
		t.Fatalf("ListNodes after delete failed: %v", err)
	}
	if total != 0 || len(nodes) != 0 {
		t.Errorf("ListNodes after cascade delete: total=%d len=%d, want 0", total, len(nodes))
	}

	// Profile remains but binding should be gone.
	view, err := svc.GetPortProfile(ctx, profileView.Profile.ID)
	if err != nil {
		t.Fatalf("GetPortProfile after cascade: %v", err)
	}
	if len(view.Nodes) != 0 {
		t.Errorf("profile nodes after cascade = %d, want 0", len(view.Nodes))
	}
}

func TestConfirmSourceConfigFullReplace(t *testing.T) {
	svc, ctx := setupTestService(t)

	yamlV1 := `
proxies:
  - name: "Old-Node"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
`
	cfg, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "replace.yaml",
		RawContent: yamlV1,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig failed: %v", err)
	}
	imported, err := svc.ConfirmSourceConfig(ctx, cfg.ID)
	if err != nil || imported != 1 {
		t.Fatalf("first ConfirmSourceConfig: imported=%d err=%v", imported, err)
	}
	nodesV1, _, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{SourceConfigID: cfg.ID})
	if err != nil || len(nodesV1) != 1 {
		t.Fatalf("nodes after first confirm: len=%d err=%v", len(nodesV1), err)
	}
	oldID := nodesV1[0].ID

	yamlV2 := `
proxies:
  - name: "New-Node"
    type: ss
    server: 9.9.9.9
    port: 443
    cipher: aes-256-gcm
    password: "otherpassword"
`
	result, err := svc.ReuploadSourceConfig(ctx, cfg.ID, clash.ReuploadSourceConfigInput{
		Filename:   "replace-v2.yaml",
		RawContent: yamlV2,
	})
	if err != nil {
		t.Fatalf("ReuploadSourceConfig failed: %v", err)
	}
	if result.ImportedNodes != 1 {
		t.Errorf("ReuploadSourceConfig imported = %d, want 1", result.ImportedNodes)
	}

	// Second confirm (same content) must full-replace without self-collision.
	imported2, err := svc.ConfirmSourceConfig(ctx, cfg.ID)
	if err != nil {
		t.Fatalf("second ConfirmSourceConfig failed: %v", err)
	}
	if imported2 != 1 {
		t.Errorf("second ConfirmSourceConfig imported = %d, want 1 (no self-collision)", imported2)
	}

	nodesV2, total, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{SourceConfigID: cfg.ID})
	if err != nil {
		t.Fatalf("ListNodes after replace: %v", err)
	}
	if total != 1 || len(nodesV2) != 1 {
		t.Fatalf("after replace: total=%d len=%d, want 1", total, len(nodesV2))
	}
	if nodesV2[0].ID == oldID {
		t.Errorf("node ID still %d after full replace; expected new row", oldID)
	}
	if nodesV2[0].Name != "New-Node" {
		t.Errorf("node name = %q, want New-Node", nodesV2[0].Name)
	}
	if nodesV2[0].SourceConfigID != cfg.ID {
		t.Errorf("source_config_id = %d, want %d", nodesV2[0].SourceConfigID, cfg.ID)
	}
}

func TestSyncSkipsCrossSourceDuplicateFingerprint(t *testing.T) {
	svc, ctx := setupTestService(t)

	sharedYAML := `
proxies:
  - name: "Shared-Node"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
`
	cfgA, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "a.yaml",
		RawContent: sharedYAML,
	})
	if err != nil {
		t.Fatalf("upload A: %v", err)
	}
	if _, err := svc.ConfirmSourceConfig(ctx, cfgA.ID); err != nil {
		t.Fatalf("confirm A: %v", err)
	}

	cfgB, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "b.yaml",
		RawContent: sharedYAML,
	})
	if err != nil {
		t.Fatalf("upload B: %v", err)
	}
	importedB, err := svc.ConfirmSourceConfig(ctx, cfgB.ID)
	if err != nil {
		t.Fatalf("confirm B: %v", err)
	}
	if importedB != 0 {
		t.Errorf("confirm B imported = %d, want 0 (cross-source skip)", importedB)
	}

	nodesA, totalA, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{SourceConfigID: cfgA.ID})
	if err != nil || totalA != 1 || len(nodesA) != 1 {
		t.Fatalf("source A nodes: total=%d len=%d err=%v", totalA, len(nodesA), err)
	}
	nodesB, totalB, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{SourceConfigID: cfgB.ID})
	if err != nil {
		t.Fatalf("source B nodes: %v", err)
	}
	if totalB != 0 || len(nodesB) != 0 {
		t.Errorf("source B nodes total=%d, want 0", totalB)
	}
}

func TestRefreshAndReuploadTypeGuards(t *testing.T) {
	svc, ctx := setupTestService(t)

	uploadYAML := `
proxies:
  - name: "U-Node"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "secretpassword"
`
	cfg, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "upload.yaml",
		RawContent: uploadYAML,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig: %v", err)
	}

	if _, err := svc.RefreshSourceConfig(ctx, cfg.ID); err == nil {
		t.Fatal("RefreshSourceConfig on upload source: want error")
	} else if !errors.Is(err, clash.ErrInvalidInput) {
		t.Errorf("RefreshSourceConfig error = %v, want ErrInvalidInput", err)
	}

	// Subscription-shaped row via reupload guard: create upload then only reupload is allowed.
	// For reupload-on-subscription, we need a subscription source. Use Fetch is network-bound;
	// instead assert reupload works on upload and refresh fails (already done).
	if _, err := svc.ReuploadSourceConfig(ctx, cfg.ID, clash.ReuploadSourceConfigInput{
		RawContent: uploadYAML,
	}); err != nil {
		t.Fatalf("ReuploadSourceConfig on upload: %v", err)
	}
}

func TestGetKernelCapabilities(t *testing.T) {
	svc, ctx := setupTestService(t)

	caps, err := svc.GetKernelCapabilities(ctx)
	if err != nil {
		t.Fatalf("GetKernelCapabilities failed: %v", err)
	}

	if len(caps) == 0 {
		t.Fatal("expected at least one capability result")
	}
	if caps[0].KernelType != "mihomo" {
		t.Errorf("expected mihomo, got %s", caps[0].KernelType)
	}
}

func assertBatchMissingBinary(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("TestNodesBatch() error = nil, want ErrBinaryNotFound")
	}
	if !errors.Is(err, clash.ErrBinaryNotFound) {
		t.Fatalf("TestNodesBatch() error = %v, want ErrBinaryNotFound", err)
	}
	if err.Error() == "未找到 Mihomo 二进制文件: mihomo" {
		t.Fatalf("binary path not resolved (still bare mihomo): %s", err)
	}
}

func TestTestNodesBatch_ResolvesDefaultBinaryPath(t *testing.T) {
	svc, ctx := setupTestService(t)
	nodeID := importOneTestNode(t, svc, ctx)

	_, err := svc.TestNodesBatch(ctx, clash.TestNodesBatchInput{
		NodeIDs: []int{nodeID},
	})
	if err == nil {
		// Default binary exists on this machine and the node test ran.
		return
	}
	assertBatchMissingBinary(t, err)
	if !strings.Contains(err.Error(), "data") || !strings.Contains(err.Error(), "core") {
		t.Fatalf("expected resolved data/core path in error, got: %s", err)
	}
}

func TestTestNodesBatch_UsesSettingsBinaryPath(t *testing.T) {
	svc, ctx, settingsRepo := setupTestServiceWithSettings(t)
	nodeID := importOneTestNode(t, svc, ctx)

	customPath := filepath.Join(t.TempDir(), "custom-mihomo-from-settings")
	if _, _, err := settingsRepo.Save(ctx, settingsdomain.Config{
		Clash: settingsdomain.ClashConfig{MihomoBinaryPath: customPath},
	}, 0); err != nil {
		t.Fatalf("Save settings: %v", err)
	}

	_, err := svc.TestNodesBatch(ctx, clash.TestNodesBatchInput{
		NodeIDs: []int{nodeID},
	})
	assertBatchMissingBinary(t, err)
	if !strings.Contains(err.Error(), filepath.Base(customPath)) {
		t.Fatalf("TestNodesBatch error = %q, want settings path %q", err, customPath)
	}
}

func TestTestNodesBatch_ExplicitPathWinsOverSettings(t *testing.T) {
	svc, ctx, settingsRepo := setupTestServiceWithSettings(t)
	nodeID := importOneTestNode(t, svc, ctx)

	settingsPath := filepath.Join(t.TempDir(), "from-settings")
	explicitPath := filepath.Join(t.TempDir(), "from-request")
	if _, _, err := settingsRepo.Save(ctx, settingsdomain.Config{
		Clash: settingsdomain.ClashConfig{MihomoBinaryPath: settingsPath},
	}, 0); err != nil {
		t.Fatalf("Save settings: %v", err)
	}

	_, err := svc.TestNodesBatch(ctx, clash.TestNodesBatchInput{
		NodeIDs:    []int{nodeID},
		BinaryPath: explicitPath,
	})
	if err == nil {
		t.Fatal("TestNodesBatch() error = nil, want missing explicit binary")
	}
	if !strings.Contains(err.Error(), filepath.Base(explicitPath)) {
		t.Fatalf("TestNodesBatch() error = %q, want explicit path %q", err, explicitPath)
	}
	if strings.Contains(err.Error(), filepath.Base(settingsPath)) {
		t.Fatalf("TestNodesBatch() error = %q, settings path should not win over explicit", err)
	}
}

func TestStartKernel_MissingBinary(t *testing.T) {
	svc, ctx := setupTestService(t)
	missing := filepath.Join(t.TempDir(), "no-such-mihomo")
	_, err := svc.StartKernel(ctx, clash.StartKernelInput{BinaryPath: missing})
	if err == nil {
		t.Fatal("StartKernel() error = nil, want ErrBinaryNotFound")
	}
	if !errors.Is(err, clash.ErrBinaryNotFound) {
		t.Fatalf("StartKernel() error = %v, want ErrBinaryNotFound", err)
	}
	if !strings.Contains(err.Error(), filepath.Base(missing)) {
		t.Fatalf("StartKernel() error = %q, want path %q", err, missing)
	}
}

type recordingLogger struct {
	infos []string
	warns []string
}

func (l *recordingLogger) Info(msg string, _ ...any) {
	l.infos = append(l.infos, msg)
}

func (l *recordingLogger) Warn(msg string, _ ...any) {
	l.warns = append(l.warns, msg)
}

func TestAutoStartKernel_MissingBinaryLogsInfoNotWarn(t *testing.T) {
	svc, ctx, settingsRepo := setupTestServiceWithSettings(t)
	nodeID := importOneTestNode(t, svc, ctx)
	missing := filepath.Join(t.TempDir(), "no-auto-start-mihomo")
	if _, _, err := settingsRepo.Save(ctx, settingsdomain.Config{
		Clash: settingsdomain.ClashConfig{MihomoBinaryPath: missing},
	}, 0); err != nil {
		t.Fatalf("Save settings: %v", err)
	}
	if _, err := svc.CreatePortProfile(ctx, &domainclash.PortProfile{
		Name:             "auto-start",
		MixedPort:        7890,
		IncludeInRuntime: true,
	}, []int{nodeID}); err != nil {
		t.Fatalf("CreatePortProfile: %v", err)
	}

	log := &recordingLogger{}
	svc.AutoStartKernel(ctx, log)
	if len(log.warns) != 0 {
		t.Fatalf("AutoStartKernel warns = %v, want none when binary is missing", log.warns)
	}
	found := false
	for _, msg := range log.infos {
		if strings.Contains(msg, "内核二进制不存在") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("AutoStartKernel infos = %v, want missing-binary skip", log.infos)
	}
}

func TestInspectKernelBinary_UsesSettingsPath(t *testing.T) {
	svc, ctx, settingsRepo := setupTestServiceWithSettings(t)
	customPath := filepath.Join(t.TempDir(), "inspect-from-settings")
	if _, _, err := settingsRepo.Save(ctx, settingsdomain.Config{
		Clash: settingsdomain.ClashConfig{MihomoBinaryPath: customPath},
	}, 0); err != nil {
		t.Fatalf("Save settings: %v", err)
	}

	_, err := svc.InspectKernelBinary(ctx, "")
	if err == nil {
		t.Fatal("InspectKernelBinary() error = nil, want missing settings binary")
	}
	if !errors.Is(err, clash.ErrBinaryNotFound) {
		t.Fatalf("InspectKernelBinary() error = %v, want ErrBinaryNotFound", err)
	}
	if !strings.Contains(err.Error(), filepath.Base(customPath)) {
		t.Fatalf("InspectKernelBinary() error = %q, want settings path %q", err, customPath)
	}
}

func TestReuploadSourceConfigPreservesPortProfileNodes(t *testing.T) {
	svc, ctx := setupTestService(t)

	yaml1 := `
proxies:
  - name: "Node-A"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "pass1"
  - name: "Node-B"
    type: ss
    server: 2.2.2.2
    port: 8388
    cipher: aes-256-gcm
    password: "pass2"
`
	cfg, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "sub.yaml",
		RawContent: yaml1,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig failed: %v", err)
	}

	if _, err := svc.ConfirmSourceConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("ConfirmSourceConfig failed: %v", err)
	}

	nodes, _, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{SourceConfigID: cfg.ID})
	if err != nil || len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got len=%d err=%v", len(nodes), err)
	}

	nodeA := nodes[0]
	nodeB := nodes[1]
	if nodeA.Name != "Node-A" {
		nodeA, nodeB = nodeB, nodeA
	}

	// Create Port Profile with both Node-A and Node-B
	profile, err := svc.CreatePortProfile(ctx, &domainclash.PortProfile{
		Name:      "Workstation-1",
		MixedPort: 7890,
	}, []int{nodeA.ID, nodeB.ID})
	if err != nil {
		t.Fatalf("CreatePortProfile failed: %v", err)
	}
	if len(profile.Nodes) != 2 {
		t.Fatalf("expected 2 nodes in workstation, got %d", len(profile.Nodes))
	}

	// Update source config: Node-A updated, Node-B removed, Node-C added
	yaml2 := `
proxies:
  - name: "Node-A-Renamed"
    type: ss
    server: 1.1.1.1
    port: 8388
    cipher: aes-256-gcm
    password: "pass1"
  - name: "Node-C-New"
    type: ss
    server: 3.3.3.3
    port: 8388
    cipher: aes-256-gcm
    password: "pass3"
`
	if _, err := svc.ReuploadSourceConfig(ctx, cfg.ID, clash.ReuploadSourceConfigInput{
		RawContent: yaml2,
	}); err != nil {
		t.Fatalf("ReuploadSourceConfig failed: %v", err)
	}

	// Check Port Profile bindings: Node-A should still be bound! Node-B removed! Node-C not auto-added!
	updatedProfile, err := svc.GetPortProfile(ctx, profile.Profile.ID)
	if err != nil {
		t.Fatalf("GetPortProfile failed: %v", err)
	}

	if len(updatedProfile.Nodes) != 1 {
		t.Fatalf("expected 1 node bound to workstation after update, got %d", len(updatedProfile.Nodes))
	}
	if updatedProfile.Nodes[0].ID != nodeA.ID {
		t.Errorf("expected bound node ID to be %d, got %d", nodeA.ID, updatedProfile.Nodes[0].ID)
	}
	if updatedProfile.Nodes[0].Name != "Node-A-Renamed" {
		t.Errorf("expected bound node name to be Node-A-Renamed, got %s", updatedProfile.Nodes[0].Name)
	}
}

func TestManualDeleteAllNodesAndResyncPreservesPortProfile(t *testing.T) {
	svc, ctx := setupTestService(t)

	yamlContent := `
proxies:
  - name: "Node-1"
    type: ss
    server: 10.0.0.1
    port: 8388
    cipher: aes-256-gcm
    password: "pwd"
  - name: "Node-2"
    type: ss
    server: 10.0.0.2
    port: 8388
    cipher: aes-256-gcm
    password: "pwd"
`
	cfg, _, err := svc.UploadSourceConfig(ctx, clash.UploadSourceConfigInput{
		Filename:   "sub.yaml",
		RawContent: yamlContent,
	})
	if err != nil {
		t.Fatalf("UploadSourceConfig failed: %v", err)
	}
	if _, err := svc.ConfirmSourceConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("ConfirmSourceConfig failed: %v", err)
	}

	nodes, _, err := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{})
	if err != nil || len(nodes) != 2 {
		t.Fatalf("expected 2 nodes, got len=%d err=%v", len(nodes), err)
	}

	// Bind nodes to Workstation
	profile, err := svc.CreatePortProfile(ctx, &domainclash.PortProfile{
		Name:      "Workstation-ManualDeleteTest",
		MixedPort: 7891,
	}, []int{nodes[0].ID, nodes[1].ID})
	if err != nil {
		t.Fatalf("CreatePortProfile failed: %v", err)
	}

	// User manually deletes ALL nodes from node pool
	nodeIDs := []int{nodes[0].ID, nodes[1].ID}
	if err := svc.DeleteNodesBatch(ctx, nodeIDs); err != nil {
		t.Fatalf("DeleteNodesBatch failed: %v", err)
	}

	// Verify node pool is empty
	nodesAfterDel, _, _ := svc.ListNodes(ctx, 1, 10, domainclash.ProxyNodeFilter{})
	if len(nodesAfterDel) != 0 {
		t.Fatalf("expected 0 nodes in pool after manual delete, got %d", len(nodesAfterDel))
	}

	// User re-syncs / confirms source config again
	if _, err := svc.ConfirmSourceConfig(ctx, cfg.ID); err != nil {
		t.Fatalf("ConfirmSourceConfig re-sync failed: %v", err)
	}

	// Verify Workstation nodes: Node-1 and Node-2 must be automatically re-bound by Fingerprint!
	updatedProfile, err := svc.GetPortProfile(ctx, profile.Profile.ID)
	if err != nil {
		t.Fatalf("GetPortProfile failed: %v", err)
	}

	if len(updatedProfile.Nodes) != 2 {
		t.Fatalf("expected 2 nodes re-bound to workstation after re-sync, got %d", len(updatedProfile.Nodes))
	}
}

func TestUpdatePortProfileToggleIncludeInRuntime(t *testing.T) {
	svc, ctx := setupTestService(t)

	profileView, err := svc.CreatePortProfile(ctx, &domainclash.PortProfile{
		Name:             "Profile-ToggleTest",
		MixedPort:        7899,
		IncludeInRuntime: true,
	}, nil)
	if err != nil {
		t.Fatalf("CreatePortProfile failed: %v", err)
	}
	if !profileView.Profile.IncludeInRuntime {
		t.Fatalf("expected IncludeInRuntime to be true initially")
	}

	// Toggle to false
	p := profileView.Profile
	p.IncludeInRuntime = false
	updated, err := svc.UpdatePortProfile(ctx, p, nil)
	if err != nil {
		t.Fatalf("UpdatePortProfile to false failed: %v", err)
	}
	if updated.Profile.IncludeInRuntime != false {
		t.Fatalf("expected IncludeInRuntime to be false after update, got %v", updated.Profile.IncludeInRuntime)
	}

	// Re-fetch from repo
	fetched, err := svc.GetPortProfile(ctx, p.ID)
	if err != nil {
		t.Fatalf("GetPortProfile failed: %v", err)
	}
	if fetched.Profile.IncludeInRuntime != false {
		t.Fatalf("expected re-fetched IncludeInRuntime to be false, got %v", fetched.Profile.IncludeInRuntime)
	}
}
