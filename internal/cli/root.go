package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version string populated at build time
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:   "coxec",
	Short: "A swiss army knife for concurrent execution",
	Long:  `coxec is a CLI tool and server for concurrent execution, providing templates, built-in clients, timing control, and structured output.`,
	Run: func(cmd *cobra.Command, args []string) {
		versionFlag, _ := cmd.Flags().GetBool("version")
		if versionFlag {
			fmt.Printf("coxec version %s\n", Version)
			return
		}
		_ = cmd.Help()
	},
}

func init() {
	rootCmd.Flags().BoolP("version", "v", false, "Print the version number")
}

// Execute runs the root command
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
