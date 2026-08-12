// The MEASURED calibration fixture (task 5.2).
//
// Its true graph is not written down anywhere: it is whatever the Go frontend RESOLVES, which is the
// only ground truth this platform already owns. The shape is chosen so the frontend's intra-procedural
// data-edge rule actually fires — `x := <call A>` followed by `<call B>(… x …)` in the same function —
// because a fixture whose measured truth is EMPTY cannot tell an agent that finds edges from one that
// finds none, and the calibration set needs at least one that can.
package main

import "github.com/anthropics/anthropic-sdk-go"

func triage(c *anthropic.Client, ticket string) {
	// classify → draft: the draft call consumes the classification's result, which is a data edge the
	// typed frontend can follow and a syntactic one cannot.
	classified := c.Messages.New(nil, anthropic.MessageNewParams{})
	drafted := c.Messages.New(nil, anthropic.MessageNewParams{Metadata: classified})
	// drafted → review: a second hop, so the fixture carries a CHAIN rather than a single edge. A
	// one-edge truth makes recall a coin flip.
	c.Messages.New(nil, anthropic.MessageNewParams{Metadata: drafted})
}

// unrelated is deliberately in the same package and shares nothing. It is the discrimination half:
// an agent that connects whatever is nearby links it to the chain above, and the measured truth
// says it must not.
func unrelated(c *anthropic.Client) {
	c.Messages.New(nil, anthropic.MessageNewParams{})
}
