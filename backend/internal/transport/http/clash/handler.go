package clash

import (
	"errors"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"

	clashapp "github.com/Rain-kl/Foam/backend/internal/application/clash"
	domainclash "github.com/Rain-kl/Foam/backend/internal/domain/clash"
	"github.com/Rain-kl/Foam/backend/internal/shared/response"
	"github.com/gin-gonic/gin"
)

type Handler struct {
	service *clashapp.Service
}

func NewHandler(service *clashapp.Service) *Handler {
	return &Handler{service: service}
}

func (h *Handler) Register(router *gin.RouterGroup) {
	// Source Configs & Imports
	router.POST("/clash/source-configs/upload", h.uploadSourceConfig)
	router.POST("/clash/source-configs/subscription", h.fetchSubscription)
	router.GET("/clash/source-configs", h.listSourceConfigs)
	router.GET("/clash/source-configs/:id", h.getSourceConfig)
	router.DELETE("/clash/source-configs/:id", h.deleteSourceConfig)
	router.POST("/clash/source-configs/:id/confirm", h.confirmSourceConfig)
	router.POST("/clash/source-configs/:id/refresh", h.refreshSourceConfig)
	router.POST("/clash/source-configs/:id/reupload", h.reuploadSourceConfig)

	// Nodes Pool
	router.GET("/clash/nodes", h.listNodes)
	router.POST("/clash/nodes/test", h.testNodes)
	router.GET("/clash/nodes/:id", h.getNode)
	router.PUT("/clash/nodes/:id", h.updateNode)
	router.DELETE("/clash/nodes/:id", h.deleteNode)
	router.POST("/clash/nodes/batch-delete", h.deleteNodesBatch)
	router.POST("/clash/nodes/batch-toggle", h.toggleNodesBatch)

	// Port Profiles / Workspace
	router.GET("/clash/port-profiles", h.listPortProfiles)
	router.POST("/clash/port-profiles", h.createPortProfile)
	router.GET("/clash/port-profiles/:id", h.getPortProfile)
	router.PUT("/clash/port-profiles/:id", h.updatePortProfile)
	router.DELETE("/clash/port-profiles/:id", h.deletePortProfile)
	router.POST("/clash/port-profiles/:id/nodes", h.setPortProfileNodes)
	router.GET("/clash/port-profiles/:id/preview", h.previewPortProfile)

	// Templates
	router.GET("/clash/port-profile-templates", h.listTemplates)
	router.POST("/clash/port-profile-templates", h.createTemplate)
	router.DELETE("/clash/port-profile-templates/:id", h.deleteTemplate)

	// Runtime & Kernel Control
	router.GET("/clash/runtime/status", h.getKernelStatus)
	router.POST("/clash/runtime/start", h.startKernel)
	router.POST("/clash/runtime/stop", h.stopKernel)
	router.POST("/clash/runtime/reload", h.reloadKernel)
	router.GET("/clash/runtime/config", h.getActiveRuntimeConfig)
	router.GET("/clash/runtime/logs", h.getKernelLogs)
	router.GET("/clash/kernels/capabilities", h.getKernelCapabilities)
	router.POST("/clash/kernels/inspect", h.inspectKernel)
	router.POST("/clash/kernels/upload", h.uploadKernel)
	router.POST("/clash/kernels/download", h.downloadKernel)
}

// --- Source Config Handlers ---

type uploadConfigRequest struct {
	Filename   string `json:"filename"`
	RawContent string `json:"raw_content" binding:"required"`
}

func (h *Handler) uploadSourceConfig(c *gin.Context) {
	var req uploadConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "请求内容不能为空")
		return
	}
	cfg, parseRes, err := h.service.UploadSourceConfig(c.Request.Context(), clashapp.UploadSourceConfigInput{
		Filename:   req.Filename,
		RawContent: req.RawContent,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusCreated, gin.H{
		"config":       cfg,
		"parse_result": parseRes,
	})
}

type fetchSubscriptionRequest struct {
	SourceURL string `json:"source_url" binding:"required"`
}

func (h *Handler) fetchSubscription(c *gin.Context) {
	var req fetchSubscriptionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "订阅地址不能为空")
		return
	}
	cfg, parseRes, err := h.service.FetchSubscription(c.Request.Context(), clashapp.FetchSubscriptionInput{
		SourceURL: req.SourceURL,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{
		"config":       cfg,
		"parse_result": parseRes,
	})
}

func (h *Handler) listSourceConfigs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	items, total, err := h.service.ListSourceConfigs(c.Request.Context(), page, pageSize)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

func (h *Handler) getSourceConfig(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	cfg, err := h.service.GetSourceConfig(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, cfg)
}

func (h *Handler) deleteSourceConfig(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.service.DeleteSourceConfig(c.Request.Context(), id); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"deleted": true})
}

func (h *Handler) confirmSourceConfig(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	importedCount, err := h.service.ConfirmSourceConfig(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"imported_nodes": importedCount})
}

func (h *Handler) refreshSourceConfig(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	result, err := h.service.RefreshSourceConfig(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, result)
}

func (h *Handler) reuploadSourceConfig(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req uploadConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "请求内容不能为空")
		return
	}
	result, err := h.service.ReuploadSourceConfig(c.Request.Context(), id, clashapp.ReuploadSourceConfigInput{
		Filename:   req.Filename,
		RawContent: req.RawContent,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, result)
}

// --- Node Pool Handlers ---

func (h *Handler) listNodes(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	sourceConfigID, _ := strconv.Atoi(c.Query("source_config_id"))

	var enabledPtr *bool
	if enabledStr := c.Query("enabled"); enabledStr != "" {
		val := enabledStr == "true" || enabledStr == "1"
		enabledPtr = &val
	}

	filter := domainclash.ProxyNodeFilter{
		Keyword:        c.Query("keyword"),
		SourceConfigID: sourceConfigID,
		Enabled:        enabledPtr,
	}

	items, total, err := h.service.ListNodes(c.Request.Context(), page, pageSize, filter)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{
		"items":     items,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

type testNodesRequest struct {
	NodeIDs    []int  `json:"node_ids" binding:"required"`
	BinaryPath string `json:"binary_path"`
	TestURL    string `json:"test_url"`
	TimeoutSec int    `json:"timeout_seconds"`
}

func (h *Handler) testNodes(c *gin.Context) {
	var req testNodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "未指定要测试的节点")
		return
	}
	results, err := h.service.TestNodesBatch(c.Request.Context(), clashapp.TestNodesBatchInput{
		NodeIDs:    req.NodeIDs,
		BinaryPath: req.BinaryPath,
		TestURL:    req.TestURL,
		TimeoutSec: req.TimeoutSec,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"results": results})
}

func (h *Handler) getNode(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	item, err := h.service.GetNode(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, item)
}

func (h *Handler) updateNode(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	node, err := h.service.GetNode(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if err := c.ShouldBindJSON(node); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "请求格式无效")
		return
	}
	node.ID = id
	if err := h.service.UpdateNode(c.Request.Context(), node); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, node)
}

func (h *Handler) deleteNode(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.service.DeleteNode(c.Request.Context(), id); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"deleted": true})
}

type batchIDsRequest struct {
	IDs     []int `json:"ids" binding:"required"`
	Enabled bool  `json:"enabled"`
}

func (h *Handler) deleteNodesBatch(c *gin.Context) {
	var req batchIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "未指定节点列表")
		return
	}
	if err := h.service.DeleteNodesBatch(c.Request.Context(), req.IDs); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"deleted_count": len(req.IDs)})
}

func (h *Handler) toggleNodesBatch(c *gin.Context) {
	var req batchIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "未指定节点列表")
		return
	}
	if err := h.service.ToggleNodesBatch(c.Request.Context(), req.IDs, req.Enabled); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"toggled_count": len(req.IDs), "enabled": req.Enabled})
}

// --- Port Profiles / Workspace Handlers ---

func (h *Handler) listPortProfiles(c *gin.Context) {
	profiles, err := h.service.ListPortProfiles(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"items": profiles})
}

type createPortProfileRequest struct {
	Name             string                               `json:"name" binding:"required"`
	ListenHost       string                               `json:"listen_host"`
	MixedPort        int                                  `json:"mixed_port" binding:"required"`
	SocksPort        int                                  `json:"socks_port"`
	HTTPPort         int                                  `json:"http_port"`
	ProxySettings    domainclash.PortProfileProxySettings `json:"proxy_settings"`
	IncludeInRuntime bool                                 `json:"include_in_runtime"`
	KernelType       string                               `json:"kernel_type"`
	NodeIDs          []int                                `json:"node_ids"`
}

func (h *Handler) createPortProfile(c *gin.Context) {
	var req createPortProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "请求格式无效或核心字段缺失")
		return
	}

	profile := &domainclash.PortProfile{
		Name:             req.Name,
		ListenHost:       req.ListenHost,
		MixedPort:        req.MixedPort,
		SocksPort:        req.SocksPort,
		HTTPPort:         req.HTTPPort,
		ProxySettings:    req.ProxySettings,
		IncludeInRuntime: req.IncludeInRuntime,
		KernelType:       req.KernelType,
	}

	view, err := h.service.CreatePortProfile(c.Request.Context(), profile, req.NodeIDs)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusCreated, view)
}

func (h *Handler) getPortProfile(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	view, err := h.service.GetPortProfile(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, view)
}

func (h *Handler) updatePortProfile(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req createPortProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "请求格式无效")
		return
	}

	profile := &domainclash.PortProfile{
		ID:               id,
		Name:             req.Name,
		ListenHost:       req.ListenHost,
		MixedPort:        req.MixedPort,
		SocksPort:        req.SocksPort,
		HTTPPort:         req.HTTPPort,
		ProxySettings:    req.ProxySettings,
		IncludeInRuntime: req.IncludeInRuntime,
		KernelType:       req.KernelType,
	}

	view, err := h.service.UpdatePortProfile(c.Request.Context(), profile, req.NodeIDs)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, view)
}

func (h *Handler) deletePortProfile(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.service.DeletePortProfile(c.Request.Context(), id); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"deleted": true})
}

type setProfileNodesRequest struct {
	NodeIDs []int `json:"node_ids"`
}

func (h *Handler) setPortProfileNodes(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req setProfileNodesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "请求格式无效")
		return
	}
	if err := h.service.SetProfileNodes(c.Request.Context(), id, req.NodeIDs); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"updated": true})
}

func (h *Handler) previewPortProfile(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	preview, err := h.service.RenderProfilePreview(c.Request.Context(), id)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, preview)
}

// --- Templates ---

func (h *Handler) listTemplates(c *gin.Context) {
	templates, err := h.service.ListTemplates(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"items": templates})
}

func (h *Handler) createTemplate(c *gin.Context) {
	var tpl domainclash.PortProfileTemplate
	if err := c.ShouldBindJSON(&tpl); err != nil {
		response.Error(c, http.StatusBadRequest, "invalidRequest", "模板名称不能为空")
		return
	}
	created, err := h.service.CreateTemplate(c.Request.Context(), &tpl)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusCreated, created)
}

func (h *Handler) deleteTemplate(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := h.service.DeleteTemplate(c.Request.Context(), id); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"deleted": true})
}

// --- Runtime & Kernel Control ---

func (h *Handler) getKernelStatus(c *gin.Context) {
	kernelType := c.DefaultQuery("kernel_type", "mihomo")
	statusView, err := h.service.GetKernelStatus(c.Request.Context(), kernelType)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, statusView)
}

type startKernelRequest struct {
	KernelType        string `json:"kernel_type"`
	BinaryPath        string `json:"binary_path"`
	WorkDir           string `json:"work_dir"`
	AllowLAN          bool   `json:"allow_lan"`
	Mode              string `json:"mode"`
	ControllerAddress string `json:"controller_address"`
	ControllerSecret  string `json:"controller_secret"`
}

func (h *Handler) startKernel(c *gin.Context) {
	var req startKernelRequest
	_ = c.ShouldBindJSON(&req)
	inst, err := h.service.StartKernel(c.Request.Context(), clashapp.StartKernelInput{
		KernelType:        req.KernelType,
		BinaryPath:        req.BinaryPath,
		WorkDir:           req.WorkDir,
		AllowLAN:          req.AllowLAN,
		Mode:              req.Mode,
		ControllerAddress: req.ControllerAddress,
		ControllerSecret:  req.ControllerSecret,
	})
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, inst)
}

type stopKernelRequest struct {
	KernelType string `json:"kernel_type"`
}

func (h *Handler) stopKernel(c *gin.Context) {
	var req stopKernelRequest
	_ = c.ShouldBindJSON(&req)
	if err := h.service.StopKernel(c.Request.Context(), req.KernelType); err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"stopped": true})
}

type reloadKernelRequest struct {
	KernelType        string `json:"kernel_type"`
	AllowLAN          bool   `json:"allow_lan"`
	Mode              string `json:"mode"`
	ControllerAddress string `json:"controller_address"`
	ControllerSecret  string `json:"controller_secret"`
}

func (h *Handler) reloadKernel(c *gin.Context) {
	var req reloadKernelRequest
	_ = c.ShouldBindJSON(&req)
	inst, err := h.service.ReloadKernel(c.Request.Context(), req.KernelType, req.AllowLAN, req.Mode, req.ControllerAddress, req.ControllerSecret)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, inst)
}

func (h *Handler) getActiveRuntimeConfig(c *gin.Context) {
	kernelType := c.DefaultQuery("kernel_type", "mihomo")
	res, err := h.service.GetActiveRuntimeConfig(c.Request.Context(), kernelType)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, res)
}

func (h *Handler) getKernelLogs(c *gin.Context) {
	kernelType := c.DefaultQuery("kernel_type", "mihomo")
	logs, err := h.service.GetKernelLogs(c.Request.Context(), kernelType)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"logs": logs})
}

func (h *Handler) getKernelCapabilities(c *gin.Context) {
	caps, err := h.service.GetKernelCapabilities(c.Request.Context())
	if err != nil {
		writeServiceError(c, err)
		return
	}
	response.Success(c, http.StatusOK, gin.H{"capabilities": caps})
}

type kernelPathRequest struct {
	InstallPath string `json:"install_path"`
}

func (h *Handler) inspectKernel(c *gin.Context) {
	var req kernelPathRequest
	_ = c.ShouldBindJSON(&req)
	res, err := h.service.InspectKernelBinary(c.Request.Context(), req.InstallPath)
	if err != nil {
		if errors.Is(err, clashapp.ErrBinaryNotFound) {
			writeServiceError(c, err)
			return
		}
		response.Error(c, http.StatusBadRequest, "inspectFailed", err.Error())
		return
	}
	response.Success(c, http.StatusOK, res)
}

func (h *Handler) uploadKernel(c *gin.Context) {
	installPath := c.PostForm("install_path")
	fileHeader, err := c.FormFile("binary")
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalidFile", "请选择 Mihomo 二进制文件上传")
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		response.Error(c, http.StatusBadRequest, "openFileFailed", err.Error())
		return
	}
	defer func() { _ = f.Close() }()

	res, err := h.service.UploadKernelBinary(c.Request.Context(), fileHeader.Filename, installPath, f)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "uploadFailed", err.Error())
		return
	}
	response.Success(c, http.StatusOK, res)
}

func (h *Handler) downloadKernel(c *gin.Context) {
	var req kernelPathRequest
	_ = c.ShouldBindJSON(&req)
	res, err := h.service.DownloadKernelBinary(c.Request.Context(), req.InstallPath)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "downloadFailed", err.Error())
		return
	}
	response.Success(c, http.StatusOK, res)
}

func (h *Handler) ProxyZashboardClash(c *gin.Context) {
	inst, err := h.service.GetKernelInstance(c.Request.Context(), "mihomo")
	controllerAddress := "127.0.0.1:9090"
	secret := ""
	if err == nil && inst != nil {
		if addr := strings.TrimSpace(inst.ControllerAddress); addr != "" {
			controllerAddress = addr
		}
		secret = strings.TrimSpace(inst.ControllerSecret)
	}

	target, err := url.Parse("http://" + controllerAddress)
	if err != nil {
		response.Error(c, http.StatusBadRequest, "invalidControllerAddress", "Clash 控制地址无效")
		return
	}

	path := c.Param("path")
	if path == "" {
		path = "/"
	}

	proxy := httputil.NewSingleHostReverseProxy(target)
	originalDirector := proxy.Director         //nolint:staticcheck
	proxy.Director = func(req *http.Request) { //nolint:staticcheck
		originalDirector(req)
		req.URL.Path = path
		req.URL.RawPath = path
		req.Host = target.Host
		if secret != "" {
			req.Header.Set("Authorization", "Bearer "+secret)
			query := req.URL.Query()
			query.Set("token", secret)
			req.URL.RawQuery = query.Encode()
		}
	}
	proxy.ErrorHandler = func(_ http.ResponseWriter, _ *http.Request, proxyErr error) {
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "连接 Clash 控制接口失败: " + proxyErr.Error(),
		})
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

func writeServiceError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, clashapp.ErrNotFound):
		response.Error(c, http.StatusNotFound, "notFound", "资源不存在")
	case errors.Is(err, clashapp.ErrInvalidInput):
		response.Error(c, http.StatusBadRequest, "invalidRequest", err.Error())
	case errors.Is(err, clashapp.ErrKernelRunning):
		response.Error(c, http.StatusConflict, "kernelRunning", "内核已在运行中")
	case errors.Is(err, clashapp.ErrKernelStopped):
		response.Error(c, http.StatusBadRequest, "kernelStopped", "内核未运行")
	case errors.Is(err, clashapp.ErrBinaryNotFound):
		response.Error(c, http.StatusBadRequest, "binaryNotFound", err.Error())
	default:
		response.Error(c, http.StatusInternalServerError, "internalError", err.Error())
	}
}
