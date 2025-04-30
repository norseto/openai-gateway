package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	"sync"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	"github.com/norseto/k8s-watchdogs/pkg/logger"
	"github.com/spf13/cobra"
)

var (
	// defaultPort is the default port number for the main server.
	defaultPort int = 8080
	// defaultQuitPort is the default port number for the quit server.
	defaultQuitPort int = 8081
	// defaultShutdownTimeoutSec is the default timeout for graceful shutdown.
	defaultShutdownTimeoutSec int = 15
)

// Config holds the application configuration, excluding the logger.
type Config struct {
	Port int
	OpenWebUIURL string
	QuitPort int
	ShutdownTimeoutSec int
}

// OpenAI Compatible Request Structure
type OpenAIChatRequest struct {
	Model    string        `json:"model"`
	Messages []MessageItem `json:"messages"`
}

// OpenAI Compatible Response Structure
type OpenAIChatResponse struct {
	ID      string     `json:"id"`
	Object  string     `json:"object"`
	Created int64      `json:"created"`
	Model   string     `json:"model"`
	Choices []Choice   `json:"choices"`
	Usage   TokenUsage `json:"usage"`
}

type MessageItem struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Choice struct {
	Index        int         `json:"index"`
	Message      MessageItem `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type TokenUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Open-WebUI Response Structure
type OpenWebUIChatResponse struct {
	Message MessageItem `json:"message"`
	Status  string      `json:"status"`
}

type OpenWebUIModel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type handler struct {
	// Config holds the application configuration.
	Config *Config
}

func NewServeCommand() *cobra.Command {
	var port int
	var openWebUIURL string
	var quitPort int
	var shutdownTimeoutSec int

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Starts the OpenAI compatible gateway server",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := &Config{
				Port:               port,
				OpenWebUIURL:       openWebUIURL,
				QuitPort:           quitPort,
				ShutdownTimeoutSec: shutdownTimeoutSec,
			}
			return processServe(cmd.Context(), cfg)
		},
	}

	cmd.Flags().IntVar(&port, "port", defaultPort, "Port number to listen on")
	cmd.Flags().StringVar(&openWebUIURL, "open-webui-url", os.Getenv("OPEN_WEBUI_URL"), "Open-WebUI API endpoint URL (can also be set via OPEN_WEBUI_URL env var)")
	cmd.Flags().IntVar(&quitPort, "quit-port", defaultQuitPort, "Internal port for the quit signal server")
	cmd.Flags().IntVar(&shutdownTimeoutSec, "shutdown-timeout", defaultShutdownTimeoutSec, "Timeout for graceful shutdown in seconds")
	_ = cmd.MarkFlagRequired("open-webui-url")


	return cmd
}

// wrapLogger is a middleware that injects the base logger into the request context.
func wrapLogger(log logr.Logger, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := logger.WithContext(r.Context(), log)
		next.ServeHTTP(w, r.WithContext(ctx))
	}
}

// handleQuitSignal handles the request to the internal quit endpoint.
// It gets the logger from the request context.
func handleQuitSignal(stopChan chan<- struct{}, closeOnce *sync.Once) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log := logger.FromContext(r.Context())
		log.Info("Received shutdown signal via /quitquitquit")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("Initiating shutdown..."))
		closeOnce.Do(func() { close(stopChan) })
	}
}

// runMainServer runs the main API server in a goroutine.
func runMainServer(ctx context.Context, cfg *Config, srv *http.Server, stopChan chan<- struct{}, closeOnce *sync.Once) {
	log := logger.FromContext(ctx)
	log.Info("Gateway server starting", "address", srv.Addr, "forwarding_url", cfg.OpenWebUIURL)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error(err, "Main server ListenAndServe error")
		closeOnce.Do(func() { close(stopChan) })
	}
}

// runQuitServer runs the internal quit server in a goroutine.
func runQuitServer(ctx context.Context, srv *http.Server) {
	log := logger.FromContext(ctx)
	log.Info("Internal quit server starting", "address", srv.Addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error(err, "Quit server ListenAndServe error")
	}
}

// setupServers initializes the main API server and the internal quit server.
func setupServers(ctx context.Context, cfg *Config, h *handler, stopChan chan struct{}, closeOnce *sync.Once) (*http.Server, *http.Server) {
	log := logger.FromContext(ctx)

	addr := fmt.Sprintf(":%d", cfg.Port)
	mainMux := http.NewServeMux()
	mainMux.HandleFunc("/", wrapLogger(log, h.handleRoot))
	mainMux.HandleFunc("/healthz", wrapLogger(log, h.handleHealth))
	mainSrv := &http.Server{
		Addr:    addr,
		Handler: mainMux,
	}

	quitAddrStr := fmt.Sprintf("127.0.0.1:%d", cfg.QuitPort)
	quitMux := http.NewServeMux()
	quitMux.HandleFunc("/quitquitquit", handleQuitSignal(stopChan, closeOnce))
	quitSrv := &http.Server{
		Addr:    quitAddrStr,
		Handler: quitMux,
	}

	return mainSrv, quitSrv
}

// startServers starts the main and quit servers in separate goroutines.
func startServers(ctx context.Context, cfg *Config, mainSrv, quitSrv *http.Server, stopChan chan struct{}, closeOnce *sync.Once) {
	go runMainServer(ctx, cfg, mainSrv, stopChan, closeOnce)
	go runQuitServer(ctx, quitSrv)
}

// waitForShutdownSignal blocks until a shutdown signal (OS or internal) is received.
func waitForShutdownSignal(ctx context.Context, stopChan <-chan struct{}) {
	log := logger.FromContext(ctx)
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.Info("Received OS signal, initiating shutdown", "signal", sig.String())
	case <-stopChan:
		log.Info("Received internal signal, initiating shutdown")
	}
}

// shutdownServers performs graceful shutdown of the main and quit servers.
func shutdownServers(ctx context.Context, cfg *Config, mainSrv, quitSrv *http.Server) {
	log := logger.FromContext(ctx)
	log.Info("Starting graceful shutdown...")
	shutdownTimeout := time.Duration(cfg.ShutdownTimeoutSec) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := mainSrv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "Main server shutdown error")
	} else {
		log.Info("Main server gracefully stopped")
	}

	if err := quitSrv.Shutdown(shutdownCtx); err != nil {
		log.Error(err, "Quit server shutdown error")
	} else {
		log.Info("Quit server gracefully stopped")
	}

	log.Info("Graceful shutdown complete")
}

// processServe is the main execution function for the serve command.
func processServe(ctx context.Context, cfg *Config) error {
	log := logger.FromContext(ctx)

	if cfg.OpenWebUIURL == "" {
		log.Error(fmt.Errorf("--open-webui-url is required"), "Startup error")
		return fmt.Errorf("--open-webui-url is required")
	}

	stopChan := make(chan struct{})
	var closeOnce sync.Once

	h := &handler{Config: cfg}

	mainSrv, quitSrv := setupServers(ctx, cfg, h, stopChan, &closeOnce)
	startServers(ctx, cfg, mainSrv, quitSrv, stopChan, &closeOnce)
	waitForShutdownSignal(ctx, stopChan)
	shutdownServers(ctx, cfg, mainSrv, quitSrv)

	return nil
}

func (h *handler) handleRoot(w http.ResponseWriter, r *http.Request) {
	log := logger.FromContext(r.Context()).WithValues("request_id", randomString(8))
	log.Info("Received request", "method", r.Method, "path", r.URL.Path)
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		log.Info("Method not allowed", "method", r.Method)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if r.URL.Path == "/v1/chat/completions" {
		h.handleChatCompletions(w, r)
		return
	}

	h.forwardAndTransform(w, r)
}

func (h *handler) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	log := logger.FromContext(r.Context()).WithValues("request_id", randomString(8))
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Error(err, "Failed to read request body")
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var openaiReq OpenAIChatRequest
	if err := json.Unmarshal(body, &openaiReq); err != nil {
		log.Error(err, "Invalid JSON format", "body", string(body))
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}
	log.Info("Handling chat completion request", "model", openaiReq.Model, "messages_count", len(openaiReq.Messages))

	webuiReqBody, err := json.Marshal(openaiReq)
	if err != nil {
		log.Error(err, "Failed to marshal WebUI request")
		http.Error(w, "Failed to marshal WebUI request", http.StatusInternalServerError)
		return
	}

	targetURL := h.Config.OpenWebUIURL + "/chat"
	log.Info("Forwarding request to Open-WebUI", "url", targetURL)
	req, err := http.NewRequest("POST", targetURL, bytes.NewReader(webuiReqBody))
	if err != nil {
		log.Error(err, "Failed to create request to WebUI")
		http.Error(w, "Failed to create request to WebUI", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	client := &http.Client{}
	startTime := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(startTime)
	if err != nil {
		log.Error(err, "Failed to contact Open-WebUI", "duration_ms", duration.Milliseconds())
		http.Error(w, "Failed to contact Open-WebUI", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Info("Received response from Open-WebUI", "status_code", resp.StatusCode, "duration_ms", duration.Milliseconds())

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		log.Error(fmt.Errorf("Open-WebUI returned non-OK status"), "Upstream error", "status_code", resp.StatusCode, "response_body", string(bodyBytes))
		http.Error(w, fmt.Sprintf("Open-WebUI Error (%d): %s", resp.StatusCode, string(bodyBytes)), http.StatusBadGateway)
		return
	}

	webuiRespBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Error(err, "Failed to read WebUI response body")
		http.Error(w, "Failed to read WebUI response", http.StatusInternalServerError)
		return
	}

	var webuiResp OpenWebUIChatResponse
	if err := json.Unmarshal(webuiRespBody, &webuiResp); err != nil {
		log.Error(err, "Invalid WebUI response format", "response_body", string(webuiRespBody))
		http.Error(w, "Invalid WebUI response format", http.StatusInternalServerError)
		return
	}

	openaiResp := OpenAIChatResponse{
		ID:      "chatcmpl-" + randomString(10),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   openaiReq.Model,
		Choices: []Choice{
			{
				Index:        0,
				Message:      webuiResp.Message,
				FinishReason: "stop",
			},
		},
		Usage: TokenUsage{
			PromptTokens:     0,
			CompletionTokens: 0,
			TotalTokens:      0,
		},
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(openaiResp); err != nil {
		log.Error(err, "Failed to encode/write OpenAI response")
	}
	log.Info("Successfully handled chat completion request", "response_id", openaiResp.ID)
}

func (h *handler) forwardAndTransform(w http.ResponseWriter, r *http.Request) {
	log := logger.FromContext(r.Context()).WithValues("request_id", randomString(8))
	targetPath := strings.TrimPrefix(r.URL.Path, "/v1")
	targetURL := h.Config.OpenWebUIURL + targetPath
	log.Info("Forwarding request", "target_url", targetURL)

	var req *http.Request
	var err error

	if r.Method == http.MethodPost {
		body, readErr := io.ReadAll(r.Body)
		if readErr != nil {
			log.Error(readErr, "Failed to read request body for forwarding")
			http.Error(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()
		req, err = http.NewRequest("POST", targetURL, bytes.NewReader(body))
	} else {
		req, err = http.NewRequest(r.Method, targetURL, nil)
	}

	if err != nil {
		log.Error(err, "Failed to create forward request", "method", r.Method, "url", targetURL)
		http.Error(w, "Failed to create forward request", http.StatusInternalServerError)
		return
	}

	for k, vv := range r.Header {
		if k != "Host" && k != "Content-Length" {
			for _, v := range vv {
				req.Header.Add(k, v)
			}
		}
	}

	client := &http.Client{}
	startTime := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(startTime)
	if err != nil {
		log.Error(err, "Failed to forward request to upstream", "url", targetURL, "duration_ms", duration.Milliseconds())
		http.Error(w, "Failed to contact upstream service", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	log.Info("Received response from upstream", "url", targetURL, "status_code", resp.StatusCode, "duration_ms", duration.Milliseconds())

	for k, vv := range resp.Header {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}

	w.WriteHeader(resp.StatusCode)

	if _, copyErr := io.Copy(w, resp.Body); copyErr != nil {
		log.Error(copyErr, "Failed to copy upstream response body")
	}

	log.Info("Forwarded request processed", "original_path", r.URL.Path, "target_path", targetPath, "status_code", resp.StatusCode)
}

func (h *handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	log := logger.FromContext(r.Context()).WithValues("request_id", randomString(8))
	log.V(1).Info("Health check request received")
	req, err := http.NewRequest("GET", h.Config.OpenWebUIURL+"/health", nil)
	if err != nil {
		log.Error(err, "Failed to create health check request")
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Error(err, "Health check failed: could not reach Open-WebUI")
		http.Error(w, "Upstream service unavailable", http.StatusServiceUnavailable)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Info("Health check warning: Open-WebUI returned non-OK status", "status_code", resp.StatusCode)
		http.Error(w, fmt.Sprintf("Upstream service unhealthy (status: %d)", resp.StatusCode), http.StatusServiceUnavailable)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
	log.Info("Health check successful")
}

func randomString(_ int) string {
	return uuid.NewString()
}
