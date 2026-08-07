package clilink

import (
	"context"

	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// transformreceipt.go is the network half of `heros apply --link-receipt`.
//
// It is a separate file and a separate method for the reason `PushSource` and `ReportVerdict` are: four
// different things cross this boundary, they are agreed to separately, and a reviewer asking "what sends
// a transform's outcome?" must be able to open one file and read it.

// SendTransformReceipt transmits a receipt under the authenticated identity.
//
// The credential check is here rather than in `apply`, so the offline command never has to know how
// authentication works — and so the message a developer gets names the command that fixes it.
func (c Commands) SendTransformReceipt(cfg cli.Config, s cli.Streams, r runlink.TransformReceipt) error {
	cred, ok := cli.LoadCredential()
	if !ok {
		return &cli.ExitError{Code: cli.ExitOperational,
			Msg: "apply: --link-receipt needs an authenticated identity — run `heros login` first " +
				"(the diff was written; nothing was transmitted)"}
	}
	s.Narratef("apply: --link-receipt — transmitting %d node outcome(s) and a diffstat "+
		"(%d file(s), +%d/-%d). The diff itself has no field on this payload and is not sent.",
		len(r.NodeOutcomes), r.FilesChanged, r.LinesAdded, r.LinesRemoved)

	res, err := c.client(cred.Token).SendTransformReceipt(context.Background(), r)
	if err != nil {
		return err
	}
	s.Narratef("apply: receipt accepted · view it at %s", res.TransformURL)
	return nil
}
