package cli

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/0funct0ry/coxec/internal/engine"
	"github.com/spf13/cobra"
)

// Version string populated at build time
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "coxec",
	Short: "A swiss army knife for concurrent execution",
	Long:  `coxec is a CLI tool and server for concurrent execution, providing templates, built-in clients, timing control, and structured output.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		versionFlag, _ := cmd.Flags().GetBool("version")
		if versionFlag {
			fmt.Printf("coxec version %s\n", Version)
			return nil
		}

		executeCmd, _ := cmd.Flags().GetString("execute")
		fileFlag, _ := cmd.Flags().GetString("file")
		templateFlag, _ := cmd.Flags().GetString("template")
		concurrency, _ := cmd.Flags().GetInt("concurrency")

		if executeCmd == "" && fileFlag == "" && templateFlag == "" {
			return fmt.Errorf("must provide one of -e, -f, or -t")
		}

		if concurrency <= 0 {
			return fmt.Errorf("concurrency (-c) must be greater than 0")
		}

		if executeCmd != "" {
			// Disable printing usage to avoid cluttering stderr on command failure
			cmd.SilenceUsage = true
			// We handle the error exiting locally to propagate exit code
			cmd.SilenceErrors = true

			tasks := make(chan string, concurrency)
			for i := 0; i < concurrency; i++ {
				tasks <- executeCmd
			}
			close(tasks)

			return engine.RunJobPool(concurrency, tasks)
		}

		return nil
	},
}

func init() {
	rootCmd.Flags().BoolP("version", "v", false, "Print the version number")
	rootCmd.Flags().StringP("execute", "e", "", "Command to execute")
	rootCmd.Flags().StringP("file", "f", "", "File containing commands (placeholder)")
	rootCmd.Flags().StringP("template", "t", "", "Input template (placeholder)")
	rootCmd.Flags().IntP("concurrency", "c", 1, "Number of concurrent executions")
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			os.Exit(exitErr.ExitCode())
		}
		// Fallback for other errors (like flag validation errors)
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
