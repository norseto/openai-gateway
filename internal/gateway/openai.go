package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-logr/logr"
	"github.com/norseto/k8s-watchdogs/pkg/logger"
	"github.com/spf13/cobra"
)

var (
	port         int
	openWebUIURL string
)

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
	Status string      `json:"status"`
}

type OpenWebUIModel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

type handler struct {
}

func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Starts the OpenAI compatible gateway server",
		RunE: func(cmd *cobra.Command, args []string) error {
			baseLog := logger.FromContext(cmd.Context())

			if openWebUIURL == "" {
				baseLog.Error(fmt.Errorf("--open-webui-url is required"), "Startup error")
				return fmt.Errorf("--open-webui-url is required")
			}

			// --- Server Setup ---
			addr := fmt.Sprintf(":%d", port)
			quitAddr := "127.0.0.1:8081" // Internal port for quit signal

			// Create handler instance (no logger needed here)
			h := &handler{}

			// Main API Server Mux
			mainMux := http.NewServeMux()
			// Wrap handlers to inject logger from request context
			mainMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
				ctx := logr.NewContext(r.Context(), baseLog)
				h.handleRoot(w, r.WithContext(ctx))
			})
			mainMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
				ctx := logr.NewContext(r.Context(), baseLog)
				h.handleHealth(w, r.WithContext(ctx))
			})

			mainSrv := &http.Server{
				Addr:    addr,
				Handler: mainMux,
			}

			// Channel to signal shutdown
			stopChan := make(chan struct{})

			// Quit Server Mux (Internal)
			quitMux := http.NewServeMux()
			quitMux.HandleFunc("/quitquitquit", func(w http.ResponseWriter, r *http.Request) {
				baseLog.Info("Received shutdown signal via /quitquitquit")
				w.WriteHeader(http.StatusOK)
				w.Write([]byte("Initiating shutdown..."))
				close(stopChan) // Signal shutdown
			})

			quitSrv := &http.Server{
				Addr:    quitAddr,
				Handler: quitMux,
			}

			// Goroutine to run the main server
			go func() {
				baseLog.Info("Gateway server starting", "address", addr, "forwarding_url", openWebUIURL)
				if err := mainSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					baseLog.Error(err, "Main server ListenAndServe error")
					close(stopChan) // Signal shutdown on server error too
				}
			}()

			// Goroutine to run the quit server
			go func() {
				baseLog.Info("Internal quit server starting", "address", quitAddr)
				if err := quitSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					baseLog.Error(err, "Quit server ListenAndServe error")
					// Don't necessarily stop the main server if quit server fails, maybe log it
				}
			}()

			// --- Wait for shutdown signal ---
			sigChan := make(chan os.Signal, 1)
			signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

			select {
			case sig := <-sigChan:
				baseLog.Info("Received OS signal, initiating shutdown", "signal", sig.String())
			case <-stopChan:
				baseLog.Info("Received internal signal, initiating shutdown")
			}

			// --- Graceful shutdown ---
			baseLog.Info("Starting graceful shutdown...")

			// Create a context with timeout for shutdown
			shutdownTimeout := 15 * time.Second
			ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
			defer cancel()

			// Shutdown the main server
			if err := mainSrv.Shutdown(ctx); err != nil {
				baseLog.Error(err, "Main server shutdown error")
			} else {
				baseLog.Info("Main server gracefully stopped")
			}

			// Shutdown the quit server
			if err := quitSrv.Shutdown(ctx); err != nil {
				baseLog.Error(err, "Quit server shutdown error")
			} else {
				baseLog.Info("Quit server gracefully stopped")
			}

			baseLog.Info("Graceful shutdown complete")
			return nil // Graceful shutdown is not an error for the command
		},
	}

	cmd.Flags().IntVar(&port, "port", 8080, "Port number to listen on")
	cmd.Flags().StringVar(&openWebUIURL, "open-webui-url", os.Getenv("OPEN_WEBUI_URL"), "Open-WebUI API endpoint URL (can also be set via OPEN_WEBUI_URL env var)")
	_ = cmd.MarkFlagRequired("open-webui-url")

	return cmd
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

	targetURL := openWebUIURL + "/chat"
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
	targetURL := openWebUIURL + targetPath
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
	log.Info("Health check request received")
	// Simple health check: check if Open-WebUI is reachable
	req, err := http.NewRequest("GET", openWebUIURL+"/health", nil) // Assuming /health exists
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

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	for i := range result {
		result[i] = letters[rand.Intn(len(letters))]
	}
	return string(result)
}
