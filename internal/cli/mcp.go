package cli

import (
	"io"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/appetizers-io/outrider/internal/mcpserver"
)

type noCloseWriter struct{ io.Writer }

func (noCloseWriter) Close() error { return nil }

func mcpCmd() *cobra.Command {
	var o mcpserver.Options
	cmd := &cobra.Command{Use: "mcp", Short: "Serve MCP tools for an explicitly selected Outrider instance", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		o.Version = Version
		server, err := mcpserver.New(o)
		if err != nil {
			return err
		}
		return server.Run(cmd.Context(), &mcp.IOTransport{Reader: io.NopCloser(cmd.InOrStdin()), Writer: noCloseWriter{cmd.OutOrStdout()}, MaxLineLength: 131072})
	}}
	cmd.Flags().StringVar(&o.ConfigPath, "config", "", "explicit CLI YAML or Crew settings JSON path (required)")
	cmd.Flags().StringVar(&o.StateDir, "state-dir", "", "explicit queue state root; no host fallback (required)")
	cmd.Flags().StringVar(&o.Format, "config-format", "cli", "configuration format: cli or crew")
	cmd.Flags().BoolVar(&o.CrewControl, "crew-control", false, "enable setup tools through the running Crew app backend")
	return cmd
}
