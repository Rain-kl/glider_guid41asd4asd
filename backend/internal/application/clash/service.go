package clash

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Rain-kl/Foam/backend/internal/domain/clash"
	kernelctrl "github.com/Rain-kl/Foam/backend/internal/infra/clash/kernel"
	proxyparser "github.com/Rain-kl/Foam/backend/internal/infra/clash/proxy"
	renderpkg "github.com/Rain-kl/Foam/backend/internal/infra/clash/runtimeconfig"
	subfetch "github.com/Rain-kl/Foam/backend/internal/infra/clash/sourcefetch"
	"github.com/Rain-kl/Foam/backend/internal/repository"
)

var (
	ErrNotFound       = repository.ErrNotFound
	ErrInvalidInput   = errors.New("请求参数无效")
	ErrKernelRunning  = errors.New("内核已在运行中")
	ErrKernelStopped  = errors.New("内核未运行")
	ErrBinaryNotFound = kernelctrl.ErrBinaryNotFound
)

type Service struct {
	sourceConfigRepo        repository.SourceConfigRepository
	proxyNodeRepo           repository.ProxyNodeRepository
	nodeTestResultRepo      repository.NodeTestResultRepository
	portProfileRepo         repository.PortProfileRepository
	portProfileTemplateRepo repository.PortProfileTemplateRepository
	runtimeConfigRepo       repository.RuntimeConfigRepository
	kernelInstanceRepo      repository.KernelInstanceRepository
	settingsRepo            repository.RuntimeSettingsRepository

	fetcher *subfetch.Fetcher

	// In-memory active process tracking
	mu            sync.Mutex // guards activeCmd, kernelType
	logMu         sync.Mutex // guards processLogBuf only
	activeCmd     *exec.Cmd
	kernelType    string
	processLogBuf []string
}

func NewService(
	sourceConfigRepo repository.SourceConfigRepository,
	proxyNodeRepo repository.ProxyNodeRepository,
	nodeTestResultRepo repository.NodeTestResultRepository,
	portProfileRepo repository.PortProfileRepository,
	portProfileTemplateRepo repository.PortProfileTemplateRepository,
	runtimeConfigRepo repository.RuntimeConfigRepository,
	kernelInstanceRepo repository.KernelInstanceRepository,
	settingsRepo repository.RuntimeSettingsRepository,
) *Service {
	return &Service{
		sourceConfigRepo:        sourceConfigRepo,
		proxyNodeRepo:           proxyNodeRepo,
		nodeTestResultRepo:      nodeTestResultRepo,
		portProfileRepo:         portProfileRepo,
		portProfileTemplateRepo: portProfileTemplateRepo,
		runtimeConfigRepo:       runtimeConfigRepo,
		kernelInstanceRepo:      kernelInstanceRepo,
		settingsRepo:            settingsRepo,
		fetcher:                 subfetch.NewFetcher(),
		processLogBuf:           make([]string, 0, 500),
	}
}

func (s *Service) getDefaultTestURL(ctx context.Context) string {
	if s.settingsRepo != nil {
		if cfg, _, _, ok, err := s.settingsRepo.Get(ctx); err == nil && ok && strings.TrimSpace(cfg.Clash.NodeTestDefaultURL) != "" {
			return strings.TrimSpace(cfg.Clash.NodeTestDefaultURL)
		}
	}
	return "https://cp.cloudflare.com/generate_204"
}

// configuredMihomoPath picks the unresolved configured path:
// explicit request → persisted settings → env / ./data/core/mihomo.
// Kernel filesystem rules (project root, Windows .exe, existence) live in
// kernel.ResolveBinaryPath / kernel.LocateBinary — callers must not invent
// a fourth fallback such as the bare command name "mihomo".
func (s *Service) configuredMihomoPath(ctx context.Context, explicit string) string {
	if path := strings.TrimSpace(explicit); path != "" {
		return path
	}
	if s.settingsRepo != nil {
		if cfg, _, _, ok, err := s.settingsRepo.Get(ctx); err == nil && ok {
			if path := strings.TrimSpace(cfg.Clash.MihomoBinaryPath); path != "" {
				return path
			}
		}
	}
	return kernelctrl.DefaultMihomoBinaryPath()
}

// locateMihomoBinary is the application gate used before side effects
// (start kernel, batch node test). Kernel inspect/test still Locate on
// the path they receive; StartMihomoProcess does not.
func (s *Service) locateMihomoBinary(ctx context.Context, explicit string) (string, error) {
	return kernelctrl.LocateBinary(s.configuredMihomoPath(ctx, explicit))
}

func (s *Service) getDefaultNodeTestTimeout(ctx context.Context) time.Duration {
	if s.settingsRepo != nil {
		if cfg, _, _, ok, err := s.settingsRepo.Get(ctx); err == nil && ok && cfg.Clash.NodeTestDefaultTimeoutMS > 0 {
			return time.Duration(cfg.Clash.NodeTestDefaultTimeoutMS) * time.Millisecond
		}
	}
	return 8 * time.Second
}

// --- SourceConfig & Imports ---

type UploadSourceConfigInput struct {
	Filename     string
	RawContent   string
	UploadedBy   string
	UploadedByID int
}

func (s *Service) UploadSourceConfig(ctx context.Context, input UploadSourceConfigInput) (*clash.SourceConfig, *proxyparser.ParseResult, error) {
	if input.Filename == "" {
		input.Filename = "upload.yaml"
	}
	if input.RawContent == "" {
		return nil, nil, fmt.Errorf("%w: 内容不能为空", ErrInvalidInput)
	}

	parseRes, err := proxyparser.ParseYAML([]byte(input.RawContent))
	if err != nil {
		return nil, nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	sum := sha256.Sum256([]byte(input.RawContent))
	hashStr := hex.EncodeToString(sum[:])

	fingerprints := make([]string, len(parseRes.Nodes))
	for i, n := range parseRes.Nodes {
		fingerprints[i] = n.Fingerprint
	}
	existingFps, err := s.proxyNodeRepo.FindExistingFingerprints(ctx, fingerprints)
	if err != nil {
		return nil, nil, err
	}

	duplicateCount := 0
	seenInFile := make(map[string]struct{})
	for _, n := range parseRes.Nodes {
		if _, exists := existingFps[n.Fingerprint]; exists {
			duplicateCount++
		} else if _, seen := seenInFile[n.Fingerprint]; seen {
			duplicateCount++
		} else {
			seenInFile[n.Fingerprint] = struct{}{}
		}
	}

	sourceItem := &clash.SourceConfig{
		SourceType:     clash.SourceConfigSourceTypeUpload,
		Filename:       input.Filename,
		ContentHash:    hashStr,
		RawContent:     input.RawContent,
		Status:         clash.SourceConfigStatusParsed,
		TotalNodes:     len(parseRes.Nodes) + len(parseRes.Issues),
		ValidNodes:     len(parseRes.Nodes),
		InvalidNodes:   len(parseRes.Issues),
		DuplicateNodes: duplicateCount,
		UploadedBy:     input.UploadedBy,
		UploadedByID:   input.UploadedByID,
	}

	if err := s.sourceConfigRepo.Create(ctx, sourceItem); err != nil {
		return nil, nil, err
	}

	return sourceItem, parseRes, nil
}

type FetchSubscriptionInput struct {
	SourceURL    string
	UploadedBy   string
	UploadedByID int
}

func (s *Service) FetchSubscription(ctx context.Context, input FetchSubscriptionInput) (*clash.SourceConfig, *proxyparser.ParseResult, error) {
	if input.SourceURL == "" {
		return nil, nil, fmt.Errorf("%w: 订阅地址不能为空", ErrInvalidInput)
	}

	fetchRes, err := s.fetcher.FetchYAML(ctx, input.SourceURL)
	if err != nil {
		return nil, nil, fmt.Errorf("拉取订阅失败: %w", err)
	}

	rawContent := string(fetchRes.Content)
	parseRes, err := proxyparser.ParseYAML(fetchRes.Content)
	if err != nil {
		return nil, nil, fmt.Errorf("解析订阅内容失败: %w", err)
	}

	sum := sha256.Sum256(fetchRes.Content)
	hashStr := hex.EncodeToString(sum[:])

	fingerprints := make([]string, len(parseRes.Nodes))
	for i, n := range parseRes.Nodes {
		fingerprints[i] = n.Fingerprint
	}
	existingFps, err := s.proxyNodeRepo.FindExistingFingerprints(ctx, fingerprints)
	if err != nil {
		return nil, nil, err
	}

	duplicateCount := 0
	seenSubInFile := make(map[string]struct{})
	for _, n := range parseRes.Nodes {
		if _, exists := existingFps[n.Fingerprint]; exists {
			duplicateCount++
		} else if _, seen := seenSubInFile[n.Fingerprint]; seen {
			duplicateCount++
		} else {
			seenSubInFile[n.Fingerprint] = struct{}{}
		}
	}

	sourceItem := &clash.SourceConfig{
		SourceType:     clash.SourceConfigSourceTypeSubscriptionURL,
		SourceURL:      input.SourceURL,
		ContentType:    fetchRes.ContentType,
		FetchedAt:      &fetchRes.FetchedAt,
		Filename:       fetchRes.DisplayName,
		ContentHash:    hashStr,
		RawContent:     rawContent,
		Status:         clash.SourceConfigStatusParsed,
		TotalNodes:     len(parseRes.Nodes) + len(parseRes.Issues),
		ValidNodes:     len(parseRes.Nodes),
		InvalidNodes:   len(parseRes.Issues),
		DuplicateNodes: duplicateCount,
		UploadedBy:     input.UploadedBy,
		UploadedByID:   input.UploadedByID,
	}

	if err := s.sourceConfigRepo.Create(ctx, sourceItem); err != nil {
		return nil, nil, err
	}

	return sourceItem, parseRes, nil
}

func (s *Service) GetSourceConfig(ctx context.Context, id int) (*clash.SourceConfig, error) {
	return s.sourceConfigRepo.GetByID(ctx, id)
}

func (s *Service) ListSourceConfigs(ctx context.Context, page, pageSize int) ([]*clash.SourceConfig, int64, error) {
	return s.sourceConfigRepo.List(ctx, page, pageSize)
}

func (s *Service) DeleteSourceConfig(ctx context.Context, id int) error {
	if _, err := s.sourceConfigRepo.GetByID(ctx, id); err != nil {
		return err
	}
	if err := s.proxyNodeRepo.DeleteBySourceConfigID(ctx, id); err != nil {
		return err
	}
	return s.sourceConfigRepo.Delete(ctx, id)
}

// SyncSourceResult is the outcome of full delete+insert sync for a source.
type SyncSourceResult struct {
	ID             int `json:"id"`
	TotalNodes     int `json:"total_nodes"`
	ValidNodes     int `json:"valid_nodes"`
	InvalidNodes   int `json:"invalid_nodes"`
	DuplicateNodes int `json:"duplicate_nodes"`
	ImportedNodes  int `json:"imported_nodes"`
}

// syncSourceNodes replaces all nodes owned by the source with a fresh parse of rawContent.
// Cross-source fingerprint collisions are skipped (global uniqueness, no rebinding).
func (s *Service) syncSourceNodes(ctx context.Context, cfg *clash.SourceConfig, rawContent string) (*SyncSourceResult, error) {
	parseRes, err := proxyparser.ParseYAML([]byte(rawContent))
	if err != nil {
		return nil, fmt.Errorf("重新解析配置失败: %w", err)
	}

	// 1. Get existing nodes for this source config
	existingNodes, err := s.proxyNodeRepo.ListBySourceConfigID(ctx, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("查询已有节点失败: %w", err)
	}

	existingMap := make(map[string]*clash.ProxyNode, len(existingNodes))
	for _, n := range existingNodes {
		existingMap[n.Fingerprint] = n
	}

	// 2. Collect all new fingerprints to check cross-source duplicates
	fingerprints := make([]string, len(parseRes.Nodes))
	for i, n := range parseRes.Nodes {
		fingerprints[i] = n.Fingerprint
	}
	existingFpsInOtherSources, err := s.proxyNodeRepo.FindExistingFingerprintsExcludingSource(ctx, fingerprints, cfg.ID)
	if err != nil {
		return nil, fmt.Errorf("检查重复节点失败: %w", err)
	}

	nodesToInsert := make([]*clash.ProxyNode, 0, len(parseRes.Nodes))
	nodesToUpdate := make([]*clash.ProxyNode, 0, len(parseRes.Nodes))
	keptNodeIDs := make(map[int]struct{})

	seenInFile := make(map[string]struct{})
	duplicateCount := 0

	for _, n := range parseRes.Nodes {
		// Duplicate inside the same file
		if _, seen := seenInFile[n.Fingerprint]; seen {
			duplicateCount++
			continue
		}
		seenInFile[n.Fingerprint] = struct{}{}

		// Check if node exists in THIS source config (fingerprint match -> preserve ID & bindings!)
		if oldNode, exists := existingMap[n.Fingerprint]; exists {
			oldNode.SourceConfigName = cfg.Filename
			oldNode.Name = n.Name
			oldNode.Type = n.Type
			oldNode.Server = n.Server
			oldNode.Port = n.Port
			oldNode.MetadataJSON = n.MetadataJSON

			nodesToUpdate = append(nodesToUpdate, oldNode)
			keptNodeIDs[oldNode.ID] = struct{}{}
			continue
		}

		// Check if fingerprint exists in OTHER source configs
		if _, inOther := existingFpsInOtherSources[n.Fingerprint]; inOther {
			duplicateCount++
			continue
		}

		// Completely new node
		nodesToInsert = append(nodesToInsert, &clash.ProxyNode{
			SourceConfigID:   cfg.ID,
			SourceConfigName: cfg.Filename,
			Name:             n.Name,
			Type:             n.Type,
			Server:           n.Server,
			Port:             n.Port,
			Fingerprint:      n.Fingerprint,
			MetadataJSON:     n.MetadataJSON,
			Enabled:          true,
			LastTestStatus:   clash.NodeTestStatusUnknown,
		})
	}

	// 3. Find old nodes that were deleted from subscription source
	idsToDelete := make([]int, 0)
	for _, oldNode := range existingNodes {
		if _, kept := keptNodeIDs[oldNode.ID]; !kept {
			idsToDelete = append(idsToDelete, oldNode.ID)
		}
	}

	// 4. Perform DB operations
	if len(idsToDelete) > 0 {
		if err := s.proxyNodeRepo.DeleteBatch(ctx, idsToDelete); err != nil {
			return nil, fmt.Errorf("删除已失效节点失败: %w", err)
		}
	}

	if len(nodesToUpdate) > 0 {
		if err := s.proxyNodeRepo.UpdateBatch(ctx, nodesToUpdate); err != nil {
			return nil, fmt.Errorf("更新现有节点失败: %w", err)
		}
	}

	if len(nodesToInsert) > 0 {
		if err := s.proxyNodeRepo.CreateBatch(ctx, nodesToInsert); err != nil {
			return nil, fmt.Errorf("批量插入新节点失败: %w", err)
		}
	}

	sum := sha256.Sum256([]byte(rawContent))
	cfg.RawContent = rawContent
	cfg.ContentHash = hex.EncodeToString(sum[:])
	cfg.Status = clash.SourceConfigStatusImported
	cfg.TotalNodes = len(parseRes.Nodes) + len(parseRes.Issues)
	cfg.ValidNodes = len(parseRes.Nodes)
	cfg.InvalidNodes = len(parseRes.Issues)
	cfg.DuplicateNodes = duplicateCount
	cfg.ImportedNodes = len(nodesToUpdate) + len(nodesToInsert)
	if err := s.sourceConfigRepo.Update(ctx, cfg); err != nil {
		return nil, err
	}

	return &SyncSourceResult{
		ID:             cfg.ID,
		TotalNodes:     cfg.TotalNodes,
		ValidNodes:     cfg.ValidNodes,
		InvalidNodes:   cfg.InvalidNodes,
		DuplicateNodes: cfg.DuplicateNodes,
		ImportedNodes:  cfg.ImportedNodes,
	}, nil
}

func (s *Service) ConfirmSourceConfig(ctx context.Context, id int) (int, error) {
	cfg, err := s.sourceConfigRepo.GetByID(ctx, id)
	if err != nil {
		return 0, err
	}
	result, err := s.syncSourceNodes(ctx, cfg, cfg.RawContent)
	if err != nil {
		return 0, err
	}
	return result.ImportedNodes, nil
}

func (s *Service) RefreshSourceConfig(ctx context.Context, id int) (*SyncSourceResult, error) {
	cfg, err := s.sourceConfigRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cfg.SourceType != clash.SourceConfigSourceTypeSubscriptionURL {
		return nil, fmt.Errorf("%w: 仅订阅类型配置源支持刷新", ErrInvalidInput)
	}
	if cfg.SourceURL == "" {
		return nil, fmt.Errorf("%w: 订阅地址为空", ErrInvalidInput)
	}

	fetchRes, err := s.fetcher.FetchYAML(ctx, cfg.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("拉取订阅失败: %w", err)
	}

	cfg.ContentType = fetchRes.ContentType
	cfg.FetchedAt = &fetchRes.FetchedAt
	if fetchRes.DisplayName != "" {
		cfg.Filename = fetchRes.DisplayName
	}

	return s.syncSourceNodes(ctx, cfg, string(fetchRes.Content))
}

type ReuploadSourceConfigInput struct {
	Filename   string
	RawContent string
}

func (s *Service) ReuploadSourceConfig(ctx context.Context, id int, input ReuploadSourceConfigInput) (*SyncSourceResult, error) {
	cfg, err := s.sourceConfigRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if cfg.SourceType != clash.SourceConfigSourceTypeUpload {
		return nil, fmt.Errorf("%w: 仅上传类型配置源支持重新上传", ErrInvalidInput)
	}
	if input.RawContent == "" {
		return nil, fmt.Errorf("%w: 内容不能为空", ErrInvalidInput)
	}
	if input.Filename != "" {
		cfg.Filename = input.Filename
	}

	return s.syncSourceNodes(ctx, cfg, input.RawContent)
}

// --- Proxy Nodes & Testing ---

func (s *Service) ListNodes(ctx context.Context, page, pageSize int, filter clash.ProxyNodeFilter) ([]*clash.ProxyNode, int64, error) {
	return s.proxyNodeRepo.List(ctx, page, pageSize, filter)
}

func (s *Service) GetNode(ctx context.Context, id int) (*clash.ProxyNode, error) {
	return s.proxyNodeRepo.GetByID(ctx, id)
}

func (s *Service) UpdateNode(ctx context.Context, node *clash.ProxyNode) error {
	return s.proxyNodeRepo.Update(ctx, node)
}

func (s *Service) DeleteNode(ctx context.Context, id int) error {
	return s.proxyNodeRepo.Delete(ctx, id)
}

func (s *Service) DeleteNodesBatch(ctx context.Context, ids []int) error {
	return s.proxyNodeRepo.DeleteBatch(ctx, ids)
}

func (s *Service) ToggleNodesBatch(ctx context.Context, ids []int, enabled bool) error {
	return s.proxyNodeRepo.ToggleBatch(ctx, ids, enabled)
}

type TestNodesBatchInput struct {
	NodeIDs    []int
	BinaryPath string
	TestURL    string
	TimeoutSec int
}

type TestNodeResultView struct {
	NodeID       int    `json:"node_id"`
	NodeName     string `json:"node_name"`
	Success      bool   `json:"success"`
	LatencyMS    int    `json:"latency_ms"`
	ErrorMessage string `json:"error_message"`
}

func (s *Service) TestNodesBatch(ctx context.Context, input TestNodesBatchInput) ([]TestNodeResultView, error) {
	if len(input.NodeIDs) == 0 {
		return nil, fmt.Errorf("%w: 未指定要测试的节点", ErrInvalidInput)
	}
	nodes, err := s.proxyNodeRepo.GetByIDs(ctx, input.NodeIDs)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return []TestNodeResultView{}, nil
	}

	located, locErr := s.locateMihomoBinary(ctx, input.BinaryPath)
	if locErr != nil {
		return nil, locErr
	}
	input.BinaryPath = located
	if input.TestURL == "" {
		input.TestURL = s.getDefaultTestURL(ctx)
	}
	timeout := time.Duration(input.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = s.getDefaultNodeTestTimeout(ctx)
	}

	results := make([]TestNodeResultView, len(nodes))
	var wg sync.WaitGroup
	semaphore := make(chan struct{}, 5) // Concurrent limit 5

	for i, node := range nodes {
		wg.Add(1)
		go func(idx int, n *clash.ProxyNode) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			resView := TestNodeResultView{
				NodeID:   n.ID,
				NodeName: n.Name,
			}

			testRes, testErr := kernelctrl.TestNodeWithMihomo(ctx, kernelctrl.MihomoNodeTestInput{
				BinaryPath:   input.BinaryPath,
				ProxyName:    n.Name,
				MetadataJSON: n.MetadataJSON,
				TestURL:      input.TestURL,
				Timeout:      timeout,
			})

			status := clash.NodeTestStatusFailed
			var latencyPtr *int
			errMsg := ""

			if testErr != nil {
				errMsg = testErr.Error()
				resView.Success = false
				resView.ErrorMessage = errMsg
			} else {
				status = clash.NodeTestStatusSuccess
				lat := testRes.LatencyMS
				latencyPtr = &lat
				resView.Success = true
				resView.LatencyMS = lat
			}

			_ = s.proxyNodeRepo.UpdateTestStatus(ctx, n.ID, status, latencyPtr, errMsg)
			_ = s.nodeTestResultRepo.Create(ctx, &clash.NodeTestResult{
				NodeID:       n.ID,
				TestType:     "delay",
				Success:      resView.Success,
				LatencyMS:    resView.LatencyMS,
				ErrorMessage: errMsg,
				TestedAt:     time.Now(),
			})

			results[idx] = resView
		}(i, node)
	}

	wg.Wait()
	return results, nil
}

// --- Port Profiles & Workspace ---

type PortProfileWithNodesView struct {
	Profile       *clash.PortProfile   `json:"profile"`
	NodeIDs       []int                `json:"node_ids"`
	Nodes         []*clash.ProxyNode   `json:"nodes,omitempty"`
	RuntimeConfig *clash.RuntimeConfig `json:"runtime_config,omitempty"`
}

func (s *Service) ListPortProfiles(ctx context.Context) ([]*PortProfileWithNodesView, error) {
	profiles, err := s.portProfileRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*PortProfileWithNodesView, len(profiles))
	for i, p := range profiles {
		nodeIDs, _ := s.portProfileRepo.GetProfileNodeIDs(ctx, p.ID)
		nodes, _ := s.portProfileRepo.GetProfileNodes(ctx, p.ID)
		rtCfg, _ := s.runtimeConfigRepo.GetByPortProfileID(ctx, p.ID)
		result[i] = &PortProfileWithNodesView{
			Profile:       p,
			NodeIDs:       nodeIDs,
			Nodes:         nodes,
			RuntimeConfig: rtCfg,
		}
	}
	return result, nil
}

func (s *Service) GetPortProfile(ctx context.Context, id int) (*PortProfileWithNodesView, error) {
	p, err := s.portProfileRepo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	nodeIDs, _ := s.portProfileRepo.GetProfileNodeIDs(ctx, p.ID)
	nodes, _ := s.portProfileRepo.GetProfileNodes(ctx, p.ID)
	rtCfg, _ := s.runtimeConfigRepo.GetByPortProfileID(ctx, p.ID)
	return &PortProfileWithNodesView{
		Profile:       p,
		NodeIDs:       nodeIDs,
		Nodes:         nodes,
		RuntimeConfig: rtCfg,
	}, nil
}

func (s *Service) CreatePortProfile(ctx context.Context, profile *clash.PortProfile, nodeIDs []int) (*PortProfileWithNodesView, error) {
	if profile.Name == "" {
		return nil, fmt.Errorf("%w: 名称不能为空", ErrInvalidInput)
	}
	if profile.MixedPort <= 0 {
		return nil, fmt.Errorf("%w: 混合端口无效", ErrInvalidInput)
	}
	if profile.KernelType == "" {
		profile.KernelType = "mihomo"
	}

	if err := s.portProfileRepo.Create(ctx, profile); err != nil {
		return nil, err
	}

	if len(nodeIDs) > 0 {
		_ = s.portProfileRepo.SetProfileNodes(ctx, profile.ID, nodeIDs)
	}

	return s.GetPortProfile(ctx, profile.ID)
}

func (s *Service) UpdatePortProfile(ctx context.Context, profile *clash.PortProfile, nodeIDs []int) (*PortProfileWithNodesView, error) {
	if err := s.portProfileRepo.Update(ctx, profile); err != nil {
		return nil, err
	}
	if nodeIDs != nil {
		_ = s.portProfileRepo.SetProfileNodes(ctx, profile.ID, nodeIDs)
	}
	return s.GetPortProfile(ctx, profile.ID)
}

func (s *Service) DeletePortProfile(ctx context.Context, id int) error {
	return s.portProfileRepo.Delete(ctx, id)
}

func (s *Service) SetProfileNodes(ctx context.Context, profileID int, nodeIDs []int) error {
	return s.portProfileRepo.SetProfileNodes(ctx, profileID, nodeIDs)
}

func (s *Service) RenderProfilePreview(ctx context.Context, profileID int) (*renderpkg.RenderResult, error) {
	p, err := s.portProfileRepo.GetByID(ctx, profileID)
	if err != nil {
		return nil, err
	}
	nodes, err := s.portProfileRepo.GetProfileNodes(ctx, profileID)
	if err != nil {
		return nil, err
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("当前端口配置未绑定任何可用节点")
	}
	profileCopy := *p
	if strings.TrimSpace(profileCopy.ProxySettings.TestURL) == "" {
		profileCopy.ProxySettings.TestURL = s.getDefaultTestURL(ctx)
	}
	res, err := renderpkg.RenderMihomoConfig(renderpkg.MihomoRenderInput{
		Profile: profileCopy,
		Nodes:   nodes,
	})
	if err != nil {
		return nil, err
	}

	_ = s.runtimeConfigRepo.Upsert(ctx, &clash.RuntimeConfig{
		PortProfileID:  p.ID,
		KernelType:     "mihomo",
		Checksum:       res.Checksum,
		RenderedConfig: res.Content,
	})

	return res, nil
}

// --- Templates ---

func (s *Service) ListTemplates(ctx context.Context) ([]*clash.PortProfileTemplate, error) {
	return s.portProfileTemplateRepo.List(ctx)
}

func (s *Service) CreateTemplate(ctx context.Context, template *clash.PortProfileTemplate) (*clash.PortProfileTemplate, error) {
	if template.Name == "" {
		return nil, fmt.Errorf("%w: 模板名称不能为空", ErrInvalidInput)
	}
	if err := s.portProfileTemplateRepo.Create(ctx, template); err != nil {
		return nil, err
	}
	return template, nil
}

func (s *Service) DeleteTemplate(ctx context.Context, id int) error {
	return s.portProfileTemplateRepo.Delete(ctx, id)
}

// --- Runtime & Kernel Lifecycle Control ---

type RuntimeStatusView struct {
	KernelInstance *clash.KernelInstance       `json:"kernel_instance"`
	IsProcessAlive bool                        `json:"is_process_alive"`
	ControllerOk   bool                        `json:"controller_ok"`
	MihomoVersion  string                      `json:"mihomo_version"`
	ActiveProfiles []*PortProfileWithNodesView `json:"active_profiles"`
}

func (s *Service) GetKernelStatus(ctx context.Context, kernelType string) (*RuntimeStatusView, error) {
	if kernelType == "" {
		kernelType = "mihomo"
	}
	inst, err := s.kernelInstanceRepo.GetByType(ctx, kernelType)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	if inst == nil {
		inst = &clash.KernelInstance{
			KernelType: kernelType,
			Status:     clash.KernelInstanceStatusStopped,
		}
	}

	isAlive := false
	s.mu.Lock()
	if s.activeCmd != nil && s.activeCmd.Process != nil && s.kernelType == kernelType {
		if s.activeCmd.ProcessState == nil {
			isAlive = true
		}
	}
	s.mu.Unlock()

	ctrlAddr := inst.ControllerAddress
	if ctrlAddr == "" {
		ctrlAddr = "127.0.0.1:9090"
	}
	ctrlOk := false
	version := ""
	if err := kernelctrl.ProbeController(ctx, ctrlAddr, inst.ControllerSecret); err == nil {
		ctrlOk = true
		if ver, err := kernelctrl.GetMihomoVersion(ctx, ctrlAddr, inst.ControllerSecret); err == nil {
			version = ver
		}
	}

	profiles, _ := s.ListPortProfiles(ctx)
	activeProfiles := make([]*PortProfileWithNodesView, 0)
	for _, p := range profiles {
		if p.Profile.IncludeInRuntime {
			activeProfiles = append(activeProfiles, p)
		}
	}

	return &RuntimeStatusView{
		KernelInstance: inst,
		IsProcessAlive: isAlive,
		ControllerOk:   ctrlOk,
		MihomoVersion:  version,
		ActiveProfiles: activeProfiles,
	}, nil
}

func (s *Service) RenderAggregatedConfig(ctx context.Context, _ string, allowLAN bool, mode string, controllerAddress string, controllerSecret string) (*renderpkg.FinalRenderResult, error) {
	profiles, err := s.ListPortProfiles(ctx)
	if err != nil {
		return nil, err
	}
	activeProfiles := make([]*renderpkg.PortProfileWithNodes, 0)
	defaultTestURL := s.getDefaultTestURL(ctx)
	for _, p := range profiles {
		if p.Profile.IncludeInRuntime && len(p.Nodes) > 0 {
			profCopy := *p.Profile
			if strings.TrimSpace(profCopy.ProxySettings.TestURL) == "" {
				profCopy.ProxySettings.TestURL = defaultTestURL
			}
			activeProfiles = append(activeProfiles, &renderpkg.PortProfileWithNodes{
				Profile: profCopy,
				Nodes:   p.Nodes,
			})
		}
	}
	if len(activeProfiles) == 0 {
		return nil, fmt.Errorf("没有已绑定节点的活动端口配置")
	}

	if controllerAddress == "" {
		controllerAddress = "127.0.0.1:9090"
	}

	return renderpkg.RenderFinalMihomoConfig(renderpkg.AggregatedMihomoInput{
		Profiles:          activeProfiles,
		AllowLAN:          allowLAN,
		Mode:              mode,
		ControllerAddress: controllerAddress,
		ControllerSecret:  controllerSecret,
	})
}

type StartKernelInput struct {
	KernelType        string
	BinaryPath        string
	WorkDir           string
	AllowLAN          bool
	Mode              string
	ControllerAddress string
	ControllerSecret  string
}

func normalizeStartKernelInput(input *StartKernelInput) {
	if input.KernelType == "" {
		input.KernelType = "mihomo"
	}
	if input.WorkDir == "" {
		input.WorkDir = "./data/runtime"
	}
	input.WorkDir = kernelctrl.ResolveProjectDataPath(input.WorkDir)
	if input.ControllerAddress == "" {
		input.ControllerAddress = "127.0.0.1:9090"
	}
}

func (s *Service) StartKernel(ctx context.Context, input StartKernelInput) (*clash.KernelInstance, error) {
	// NOTE: Do NOT hold s.mu across any blocking I/O or cmd.Wait().
	// stringLogWriter.Write acquires logMu; s.mu is only held for short pointer swaps.

	normalizeStartKernelInput(&input)
	located, locErr := s.locateMihomoBinary(ctx, input.BinaryPath)
	if locErr != nil {
		return nil, locErr
	}
	input.BinaryPath = located

	if err := os.MkdirAll(input.WorkDir, 0o755); err != nil { //nolint:gosec,mnd
		return nil, fmt.Errorf("创建工作目录失败: %w", err)
	}

	// Render aggregated config
	aggregated, err := s.RenderAggregatedConfig(ctx, input.KernelType, input.AllowLAN, input.Mode, input.ControllerAddress, input.ControllerSecret)
	if err != nil {
		return nil, fmt.Errorf("渲染内核配置失败: %w", err)
	}

	configPath := filepath.Join(input.WorkDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(aggregated.Content), 0o600); err != nil {
		return nil, fmt.Errorf("写入内核配置文件失败: %w", err)
	}

	// Kill existing process before starting a new one
	s.mu.Lock()
	if s.activeCmd != nil && s.activeCmd.Process != nil && s.activeCmd.ProcessState == nil {
		_ = s.activeCmd.Process.Kill()
	}
	s.mu.Unlock()

	// Clear log buffer before start
	s.logMu.Lock()
	s.processLogBuf = nil
	s.logMu.Unlock()

	logWriter := &stringLogWriter{service: s}
	cmd, err := kernelctrl.StartMihomoProcess(ctx, input.BinaryPath, input.WorkDir, configPath, logWriter, logWriter)
	if err != nil {
		return nil, fmt.Errorf("启动 Mihomo 进程失败: %w", err)
	}

	// Register the new command under the process lock
	s.mu.Lock()
	s.activeCmd = cmd
	s.kernelType = input.KernelType
	s.mu.Unlock()

	pid := cmd.Process.Pid
	now := time.Now()

	inst := &clash.KernelInstance{
		KernelType:           input.KernelType,
		Status:               clash.KernelInstanceStatusRunning,
		PID:                  &pid,
		WorkDir:              input.WorkDir,
		ConfigPath:           configPath,
		ControllerAddress:    input.ControllerAddress,
		ControllerSecret:     input.ControllerSecret,
		ActiveConfigChecksum: aggregated.Checksum,
		ActiveProfileCount:   aggregated.ProfileCount,
		ActiveListenerCount:  aggregated.ListenerCount,
		LastAction:           "start",
		LastError:            "",
		LastStartedAt:        &now,
	}
	// Preserve identity on re-start so Upsert updates the same row cleanly.
	if existing, getErr := s.kernelInstanceRepo.GetByType(ctx, input.KernelType); getErr == nil && existing != nil {
		inst.ID = existing.ID
		inst.CreatedAt = existing.CreatedAt
	}

	if err := s.kernelInstanceRepo.Upsert(ctx, inst); err != nil {
		_ = cmd.Process.Kill()
		s.mu.Lock()
		if s.activeCmd == cmd {
			s.activeCmd = nil
		}
		s.mu.Unlock()
		return nil, fmt.Errorf("保存内核实例状态失败: %w", err)
	}

	// done is buffered so the goroutine never blocks even if nobody reads it
	done := make(chan error, 1)
	go func() {
		// cmd.Wait() drains stdout/stderr pipes then returns.
		// This must NOT hold any mutex — stringLogWriter.Write() runs concurrently.
		done <- cmd.Wait()
	}()

	// Liveness probe: wait up to 600ms for a premature exit (port conflict, bad config, etc.)
	select {
	case waitErr := <-done:
		// Process died before 600ms — collect errors and surface them
		s.mu.Lock()
		if s.activeCmd == cmd {
			s.activeCmd = nil
		}
		s.mu.Unlock()

		s.logMu.Lock()
		logSnapshot := make([]string, len(s.processLogBuf))
		copy(logSnapshot, s.processLogBuf)
		s.logMu.Unlock()

		errMsg := extractKernelError(logSnapshot)
		if errMsg == "" && waitErr != nil {
			errMsg = waitErr.Error()
		}

		stopTime := time.Now()
		inst.Status = clash.KernelInstanceStatusStopped
		inst.PID = nil
		inst.LastError = errMsg
		inst.LastStoppedAt = &stopTime
		if upsertErr := s.kernelInstanceRepo.Upsert(ctx, inst); upsertErr != nil {
			return nil, fmt.Errorf("内核启动后异常退出: %s (且状态写回失败: %v)", errMsg, upsertErr)
		}

		if errMsg != "" {
			return nil, fmt.Errorf("内核启动后异常退出: %s", errMsg)
		}
		return nil, errors.New("内核进程启动后立即退出")
	case <-time.After(600 * time.Millisecond):
		// Process is still alive — good
	}

	// Async: continue watching for later runtime exit
	go func() { //nolint:gosec,contextcheck
		waitErr := <-done
		s.mu.Lock()
		if s.activeCmd == cmd {
			s.activeCmd = nil
		}
		s.mu.Unlock()

		s.logMu.Lock()
		logSnapshot := make([]string, len(s.processLogBuf))
		copy(logSnapshot, s.processLogBuf)
		s.logMu.Unlock()

		errMsg := extractKernelError(logSnapshot)
		if errMsg == "" && waitErr != nil {
			errMsg = waitErr.Error()
		}

		stopTime := time.Now()
		inst.Status = clash.KernelInstanceStatusStopped
		inst.PID = nil
		inst.LastError = errMsg
		inst.LastStoppedAt = &stopTime
		_ = s.kernelInstanceRepo.Upsert(context.Background(), inst)
	}()

	return inst, nil
}

func (s *Service) StopKernel(ctx context.Context, kernelType string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.activeCmd != nil && s.activeCmd.Process != nil {
		_ = s.activeCmd.Process.Kill()
		s.activeCmd = nil
	}

	if kernelType == "" {
		kernelType = "mihomo"
	}
	inst, _ := s.kernelInstanceRepo.GetByType(ctx, kernelType)
	if inst != nil {
		now := time.Now()
		inst.Status = clash.KernelInstanceStatusStopped
		inst.PID = nil
		inst.LastAction = "stop"
		inst.LastStoppedAt = &now
		_ = s.kernelInstanceRepo.Upsert(ctx, inst)
	}
	return nil
}

func (s *Service) ReloadKernel(ctx context.Context, kernelType string, allowLAN bool, mode string, controllerAddress string, controllerSecret string) (*clash.KernelInstance, error) {
	if kernelType == "" {
		kernelType = "mihomo"
	}

	s.mu.Lock()
	isProcessAlive := s.activeCmd != nil && s.activeCmd.Process != nil && s.activeCmd.ProcessState == nil
	s.mu.Unlock()

	inst, err := s.kernelInstanceRepo.GetByType(ctx, kernelType)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// If kernel process is not alive or instance is stopped, automatically start the kernel
	if !isProcessAlive || inst == nil || inst.ConfigPath == "" || inst.Status != clash.KernelInstanceStatusRunning {
		return s.StartKernel(ctx, StartKernelInput{
			KernelType:        kernelType,
			AllowLAN:          allowLAN,
			Mode:              mode,
			ControllerAddress: controllerAddress,
			ControllerSecret:  controllerSecret,
		})
	}

	aggregated, err := s.RenderAggregatedConfig(ctx, kernelType, allowLAN, mode, controllerAddress, controllerSecret)
	if err != nil {
		return nil, fmt.Errorf("重载前渲染配置失败: %w", err)
	}

	if err := os.WriteFile(inst.ConfigPath, []byte(aggregated.Content), 0o600); err != nil {
		return nil, fmt.Errorf("覆盖配置文件失败: %w", err)
	}

	if err := kernelctrl.ReloadMihomoConfig(ctx, inst.ControllerAddress, inst.ControllerSecret, inst.ConfigPath); err != nil {
		// If controller HTTP reload fails, fallback to restarting kernel cleanly
		if startInst, startErr := s.StartKernel(ctx, StartKernelInput{
			KernelType:        kernelType,
			AllowLAN:          allowLAN,
			Mode:              mode,
			ControllerAddress: controllerAddress,
			ControllerSecret:  controllerSecret,
		}); startErr == nil {
			return startInst, nil
		}
		return nil, fmt.Errorf("调用内核 API 重载失败: %w", err)
	}

	now := time.Now()
	inst.ActiveConfigChecksum = aggregated.Checksum
	inst.ActiveProfileCount = aggregated.ProfileCount
	inst.ActiveListenerCount = aggregated.ListenerCount
	inst.LastAction = "reload"
	inst.LastReloadedAt = &now
	_ = s.kernelInstanceRepo.Upsert(ctx, inst)

	return inst, nil
}

func (s *Service) GetActiveRuntimeConfig(ctx context.Context, kernelType string) (*renderpkg.FinalRenderResult, error) {
	if kernelType == "" {
		kernelType = "mihomo"
	}

	inst, err := s.kernelInstanceRepo.GetByType(ctx, kernelType)
	if err == nil && inst != nil && inst.ConfigPath != "" {
		if contentBytes, readErr := os.ReadFile(inst.ConfigPath); readErr == nil && len(contentBytes) > 0 {
			contentStr := string(contentBytes)
			sum := sha256.Sum256(contentBytes)
			return &renderpkg.FinalRenderResult{
				KernelType:    kernelType,
				Checksum:      hex.EncodeToString(sum[:]),
				Content:       contentStr,
				ProfileCount:  inst.ActiveProfileCount,
				ListenerCount: inst.ActiveListenerCount,
			}, nil
		}
	}

	return s.RenderAggregatedConfig(ctx, kernelType, false, "rule", "127.0.0.1:9090", "")
}

func (s *Service) GetKernelLogs(_ context.Context, _ string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	logs := make([]string, len(s.processLogBuf))
	copy(logs, s.processLogBuf)
	return logs, nil
}

func (s *Service) GetKernelCapabilities(_ context.Context) ([]*clash.KernelCapability, error) {
	return []*clash.KernelCapability{
		{
			KernelType:          "mihomo",
			SupportsReload:      true,
			SupportsExternalAPI: true,
			SupportsHealthCheck: true,
			SupportedStrategies: []string{
				clash.PortProfileStrategySelect,
				clash.PortProfileStrategyURLTest,
				clash.PortProfileStrategyFallback,
				clash.PortProfileStrategyLoadBalance,
			},
		},
	}, nil
}

type stringLogWriter struct {
	service *Service
}

func (w *stringLogWriter) Write(p []byte) (n int, err error) {
	// Use logMu (not s.mu) to avoid deadlock with StartKernel which holds s.mu
	// while cmd.Wait() blocks waiting for pipes to drain via this very Write method.
	w.service.logMu.Lock()
	defer w.service.logMu.Unlock()

	line := string(p)
	w.service.processLogBuf = append(w.service.processLogBuf, line)
	if len(w.service.processLogBuf) > 500 {
		w.service.processLogBuf = w.service.processLogBuf[len(w.service.processLogBuf)-500:]
	}
	return len(p), nil
}

func isKernelErrorLine(l string) bool {
	return strings.Contains(l, "level=error") ||
		strings.Contains(l, "level=fatal") ||
		strings.Contains(l, "bind: address already in use") ||
		strings.Contains(l, "listen error") ||
		strings.Contains(l, "listen err")
}

func extractKernelError(logs []string) string {
	var errs []string
	for _, l := range logs {
		if !isKernelErrorLine(l) {
			continue
		}
		trimmed := strings.TrimSpace(l)
		if idx := strings.Index(trimmed, "msg=\""); idx != -1 {
			msg := trimmed[idx+5:]
			if endIdx := strings.Index(msg, "\""); endIdx != -1 {
				msg = msg[:endIdx]
			}
			if msg != "" {
				errs = append(errs, msg)
				continue
			}
		}
		errs = append(errs, trimmed)
	}
	if len(errs) == 0 {
		return ""
	}
	if len(errs) > 3 {
		errs = errs[len(errs)-3:]
	}
	return strings.Join(errs, "; ")
}

func (s *Service) InspectKernelBinary(ctx context.Context, installPath string) (*kernelctrl.InstalledKernelBinary, error) {
	return kernelctrl.InspectMihomoBinary(ctx, s.configuredMihomoPath(ctx, installPath))
}

func (s *Service) UploadKernelBinary(ctx context.Context, fileName string, installPath string, reader io.Reader) (*kernelctrl.InstalledKernelBinary, error) {
	return kernelctrl.InstallUploadedMihomoBinary(ctx, fileName, s.configuredMihomoPath(ctx, installPath), reader)
}

func (s *Service) DownloadKernelBinary(ctx context.Context, installPath string) (*kernelctrl.InstalledKernelBinary, error) {
	return kernelctrl.DownloadAndInstallMihomoBinary(ctx, s.configuredMihomoPath(ctx, installPath))
}

func (s *Service) GetKernelInstance(ctx context.Context, kernelType string) (*clash.KernelInstance, error) {
	if kernelType == "" {
		kernelType = "mihomo"
	}
	return s.kernelInstanceRepo.GetByType(ctx, kernelType)
}

// AutoStartKernel 检查当前配置是否满足自动启动条件，满足则启动内核。
// 应在应用启动阶段（组合根完成后）以 goroutine 形式调用，不阻塞主流程。
// 条件：① 至少一个 include_in_runtime=true 且已绑定节点的端口配置；② 内核二进制存在可执行。
func (s *Service) AutoStartKernel(ctx context.Context, logger interface {
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
}) {
	const defaultWorkDir = "./data/runtime"

	// 检查活动端口配置
	profiles, err := s.ListPortProfiles(ctx)
	if err != nil {
		logger.Warn("auto_start_kernel: 读取端口配置失败，跳过自动启动", "error", err)
		return
	}
	hasActive := false
	for _, p := range profiles {
		if p.Profile.IncludeInRuntime && len(p.Nodes) > 0 {
			hasActive = true
			break
		}
	}
	if !hasActive {
		logger.Info("auto_start_kernel: 没有可用的活动端口配置，跳过自动启动")
		return
	}

	logger.Info("auto_start_kernel: 检测到可用的活动端口配置，正在自动启动内核…")

	inst, startErr := s.StartKernel(ctx, StartKernelInput{
		KernelType: "mihomo",
		WorkDir:    defaultWorkDir,
	})
	if startErr != nil {
		if errors.Is(startErr, ErrBinaryNotFound) {
			logger.Info("auto_start_kernel: 内核二进制不存在，跳过自动启动", "error", startErr)
			return
		}
		logger.Warn("auto_start_kernel: 自动启动内核失败", "error", startErr)
		return
	}
	logger.Info("auto_start_kernel: 内核已成功自动启动",
		"pid", func() int {
			if inst.PID != nil {
				return *inst.PID
			}
			return 0
		}(),
		"controller", inst.ControllerAddress,
	)
}
