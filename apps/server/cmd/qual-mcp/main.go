// qual-mcp runs a local MCP stdio client adapter that connects only to the
// protected Qual HTTP API. It NEVER receives PostgreSQL credentials.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"qual/internal/integration/mcpserver"
)

func main() {
	if err := run(); err != nil {
		// Stdout belongs to JSON-RPC only.
		fmt.Fprintln(os.Stderr, "qual-mcp:", err)
		os.Exit(1)
	}
}
func run() error {
	bearer := os.Getenv("QUAL_TOKEN")
	if bearer == "" {
		return errors.New("QUAL_TOKEN scoped PAT required; do not pass secrets on the command line")
	}
	apiURL, err := mcpserver.ValidateAPIURL(os.Getenv("QUAL_API_URL"))
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	bridge := mcpserver.Config{APIURL: apiURL, Bearer: bearer}
	return mcpserver.New(bridge).Run(ctx, &mcp.StdioTransport{})
}
