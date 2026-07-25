// Command heros is the P11 customer-installed CLI: the product's front door and its metering path.
//
//	heros discover|apply|eval|status|version   — local, no account, no network
//	heros login|link                           — explicit, authenticated; transmit only to
//	                                             https://heros-agent.space
//
// It is free on every plan, non-interactive by default, and needs no TTY. Machine output goes to
// stdout in a stable versioned format; human narration goes to stderr. See
// docs/decisions/p11-contracts.md for the exit-code, output-format, and allowlist contracts.
package main

import (
	"os"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/clilink"
)

func main() {
	streams := cli.Streams{Out: os.Stdout, Err: os.Stderr}
	// The offline surface (cli) never imports the network; the platform commands are injected here.
	code := cli.Main(os.Args[1:], streams, os.LookupEnv, clilink.Commands{})
	os.Exit(code)
}
