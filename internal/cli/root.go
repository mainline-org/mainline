package cli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"mainline/internal/domain"
	"mainline/internal/engine"
)

var (
	jsonOutput bool
	quietMode  bool
	cwdPath    string
)

var rootCmd = &cobra.Command{
	Use:   "mainline",
	Short: "Distributed intent ledger for coding agents",
	Long:  "Mainline coordinates multiple AI coding agents by recording, checking, and merging their work intents.",
	PersistentPreRun: func(cmd *cobra.Command, args []string) {
		if cwdPath != "" {
			os.Chdir(cwdPath)
		}
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "output in JSON format")
	rootCmd.PersistentFlags().BoolVar(&quietMode, "quiet", false, "suppress non-error output")
	rootCmd.PersistentFlags().StringVar(&cwdPath, "cwd", "", "set working directory")

	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(appendCmd)
	rootCmd.AddCommand(sealCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(publishCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(mergeCmd)
	rootCmd.AddCommand(logCmd)
	rootCmd.AddCommand(showCmd)
	rootCmd.AddCommand(threadCmd)
	rootCmd.AddCommand(prTrailerCmd)
	rootCmd.AddCommand(prDescriptionCmd)
	rootCmd.AddCommand(reconcileCmd)
	rootCmd.AddCommand(contextCmd)
	rootCmd.AddCommand(listProposalsCmd)
	rootCmd.AddCommand(canonicalHashCmd)
}

func getService() (*engine.Service, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}
	return engine.NewService(cwd)
}

func outputJSON(data interface{}) {
	resp := domain.JSONResponse{OK: true, Data: data}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(resp)
}

func outputError(err error) {
	if mlErr, ok := err.(*domain.MainlineError); ok {
		if jsonOutput {
			resp := domain.JSONErrorResponse{OK: false, Error: mlErr}
			enc := json.NewEncoder(os.Stderr)
			enc.SetIndent("", "  ")
			enc.Encode(resp)
		} else {
			fmt.Fprintf(os.Stderr, "error [%s]: %s\n", mlErr.Code, mlErr.Message)
			for _, a := range mlErr.SuggestedActions {
				fmt.Fprintf(os.Stderr, "  suggestion: %s\n", a)
			}
		}
	} else {
		if jsonOutput {
			resp := domain.JSONErrorResponse{OK: false, Error: &domain.MainlineError{
				Code: domain.ErrIOError, Message: err.Error(),
			}}
			enc := json.NewEncoder(os.Stderr)
			enc.SetIndent("", "  ")
			enc.Encode(resp)
		} else {
			fmt.Fprintf(os.Stderr, "error: %s\n", err)
		}
	}
	os.Exit(1)
}
