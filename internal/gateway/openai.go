package gateway

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

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
}

type OpenWebUIModel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

var rootCmd = &cobra.Command{
	Use:   "openai-gateway",
	Short: "Open-WebUI Gateway compatible with OpenAI API",
	Run: func(cmd *cobra.Command, args []string) {
		if openWebUIURL == "" {
			fmt.Println("Error: --open-webui-url is required")
			os.Exit(1)
		}

		http.HandleFunc("/", handler)
		http.HandleFunc("/healthz", healthHandler)

		addr := fmt.Sprintf(":%d", port)
		log.Printf("Gateway server started on port %d", port)
		log.Printf("Forwarding to Open-WebUI at %s", openWebUIURL)
		log.Fatal(http.ListenAndServe(addr, nil))
	},
}

func Execute() {
	rootCmd.Flags().IntVar(&port, "port", 8080, "Port number to listen on")
	rootCmd.Flags().StringVar(&openWebUIURL, "open-webui-url", "", "Open-WebUI API endpoint URL")
	cobra.CheckErr(rootCmd.Execute())
}

func handler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Special handling: /v1/chat/completions
	if r.URL.Path == "/v1/chat/completions" {
		handleChatCompletions(w, r)
		return
	}

	// Other: Pass through request and transform response
	forwardAndTransform(w, r)
}

func handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	var openaiReq OpenAIChatRequest
	if err := json.Unmarshal(body, &openaiReq); err != nil {
		http.Error(w, "Invalid JSON format", http.StatusBadRequest)
		return
	}

	webuiReqBody, err := json.Marshal(openaiReq)
	if err != nil {
		http.Error(w, "Failed to marshal WebUI request", http.StatusInternalServerError)
		return
	}

	req, err := http.NewRequest("POST", openWebUIURL+"/chat", bytes.NewReader(webuiReqBody))
	if err != nil {
		http.Error(w, "Failed to create request to WebUI", http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to contact Open-WebUI", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Check if the response status code is not OK, return 502 Bad Gateway
	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		http.Error(w, string(bodyBytes), http.StatusBadGateway)
		return
	}

	webuiRespBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read WebUI response", http.StatusInternalServerError)
		return
	}

	var webuiResp OpenWebUIChatResponse
	if err := json.Unmarshal(webuiRespBody, &webuiResp); err != nil {
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
	json.NewEncoder(w).Encode(openaiResp)
}

func forwardAndTransform(w http.ResponseWriter, r *http.Request) {
	targetURL := openWebUIURL + strings.TrimPrefix(r.URL.Path, "/v1")

	var req *http.Request
	var err error

	if r.Method == "POST" {
		body, _ := io.ReadAll(r.Body)
		defer r.Body.Close()
		req, err = http.NewRequest("POST", targetURL, bytes.NewReader(body))
	} else { // GET
		req, err = http.NewRequest("GET", targetURL, nil)
	}

	if err != nil {
		http.Error(w, "Failed to create forward request", http.StatusInternalServerError)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if auth := r.Header.Get("Authorization"); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "Failed to contact Open-WebUI", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	webuiRespBody, _ := io.ReadAll(resp.Body)

	// Response transformation: /models only
	if r.URL.Path == "/v1/models" {
		var models []OpenWebUIModel
		if err := json.Unmarshal(webuiRespBody, &models); err != nil {
			http.Error(w, "Invalid WebUI models response format", http.StatusInternalServerError)
			return
		}

		data := make([]map[string]interface{}, 0)
		for _, m := range models {
			data = append(data, map[string]interface{}{
				"id":       m.ID,
				"object":   "model",
				"owned_by": "open-webui",
			})
		}

		result := map[string]interface{}{
			"object": "list",
			"data":   data,
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result)
		return
	}

	// Other responses are passed through as is
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(webuiRespBody)
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	resp := map[string]string{
		"status": "ok",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(resp)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, n)
	for i := range result {
		result[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(result)
}
