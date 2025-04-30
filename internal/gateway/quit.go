package gateway

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/norseto/k8s-watchdogs/pkg/logger"
	"github.com/spf13/cobra"
)

const (
	quitTimeout = 5 * time.Second
)

// NewQuitCommand creates a new cobra command for sending the quit signal.
func NewQuitCommand() *cobra.Command {
	var quitPort int

	cmd := &cobra.Command{
		Use:   "quit",
		Short: "Sends a shutdown signal to a running gateway server",
		RunE: func(cmd *cobra.Command, args []string) error {
			log := logger.FromContext(cmd.Context())

			quitURL := fmt.Sprintf("http://127.0.0.1:%d/quitquitquit", quitPort)
			log.Info("Sending shutdown signal", "url", quitURL)

			client := &http.Client{
				Timeout: quitTimeout,
			}

			req, err := http.NewRequest("POST", quitURL, nil)
			if err != nil {
				log.Error(err, "Failed to create quit request")
				return fmt.Errorf("failed to create quit request: %w", err)
			}

			resp, err := client.Do(req)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					log.Error(err, "Quit request timed out", "timeout", quitTimeout)
					return fmt.Errorf("quit request timed out: %w", err)
				}
				if opErr, ok := err.(*net.OpError); ok {
					if sysErr, ok := opErr.Err.(*os.SyscallError); ok && sysErr.Err == syscall.ECONNREFUSED {
						log.Info("Gateway server not found or not running at target address", "target_url", quitURL)
						return nil
					}
				}
				log.Error(err, "Failed to send quit request")
				return fmt.Errorf("failed to send quit request: %w", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				log.Info("Shutdown signal sent successfully")
			} else {
				log.Error(fmt.Errorf("unexpected status code: %d", resp.StatusCode), "Failed to send quit signal", "status_code", resp.StatusCode)
				return fmt.Errorf("quit endpoint returned non-OK status: %d", resp.StatusCode)
			}

			return nil
		},
	}

	cmd.Flags().IntVar(&quitPort, "quit-port", 8081, "Internal port where the target gateway's quit server listens")

	return cmd
}
