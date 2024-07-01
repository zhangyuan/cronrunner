package cmd

import (
	"cronrunner/pkg/serve"
	"log"

	"github.com/spf13/cobra"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the server",
	Run: func(cmd *cobra.Command, args []string) {
		if err := serve.Invoke(confPath, bindAddress); err != nil {
			log.Fatalln(err)
		}
	},
}

var confPath string
var bindAddress string

func init() {
	rootCmd.AddCommand(serveCmd)
	serveCmd.Flags().StringVarP(&confPath, "config", "c", "config.yaml", "Path to the configuration file")
	serveCmd.Flags().StringVarP(&bindAddress, "bind", "b", ":8080", "Listening address. default: :8080")
}
