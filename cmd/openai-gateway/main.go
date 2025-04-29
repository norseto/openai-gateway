package main

import (
	"context"
	"os"

	"github.com/go-logr/logr"
	"github.com/norseto/openai-gateway/internal/gateway"
	"github.com/norseto/k8s-watchdogs/pkg/logger"
	"github.com/spf13/cobra"
)

func main() {
	ctx := context.Background()

	var rootCmd = &cobra.Command{
		Use:   "openai-gateway",
		Short: "An OpenAI API gateway",
		Long:  `An OpenAI API gateway that provides additional features like request/response logging and token usage tracking.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			logger.InitCmdLogger(cmd) // This function should handle necessary flag bindings
		},
	}
	rootCmd.SetContext(ctx)

	// Add the gateway server command
	rootCmd.AddCommand(gateway.NewCommand())

	// Remove explicit logger flag binding - InitCmdLogger handles this.
	/*
	// Bind logger flags to the root command's persistent flags
	loggerOpts := logger.NewOptions()
	loggerOpts.BindFlags(rootCmd.PersistentFlags())
	*/

	if err := rootCmd.Execute(); err != nil {
		// Use the logger if initialized, otherwise print to stderr
		log := logger.FromContext(rootCmd.Context()) // Use logger from context
		if log != (logr.Logger{}) {
			log.Error(err, "Failed to execute command")
		} else {
			_, _ = os.Stderr.WriteString("Failed to execute command: " + err.Error() + "\n")
		}
		os.Exit(1)
	}
}
