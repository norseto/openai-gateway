package main

import (
	"context"
	"os"

	gw "github.com/norseto/openai-gateway"
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
	}
	rootCmd.SetContext(ctx)
	logger.InitCmdLogger(rootCmd, func(cmd *cobra.Command, args []string) {
		logger.FromContext(cmd.Context()).Info("Starting OpenAI Gateway", "version", gw.RELEASE_VERSION, "git_version", gw.GitVersion)
	})

	rootCmd.AddCommand(gateway.NewServeCommand())
	rootCmd.AddCommand(gateway.NewQuitCommand())

	if err := rootCmd.Execute(); err != nil {
		log := logger.FromContext(rootCmd.Context())
		log.Error(err, "Failed to execute command")
		os.Exit(1)
	}
}
