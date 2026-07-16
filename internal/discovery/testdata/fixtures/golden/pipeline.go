package golden

import "github.com/anthropics/anthropic-sdk-go"

// classify is a single static LLM call with a symbolic model constant.
func classify(client *anthropic.Client) {
	client.Messages.New(nil, anthropic.MessageNewParams{Model: anthropic.ModelClaudeOpus4_6})
}

// agent loops the LLM call — one static definition, variable at runtime.
func agent(client *anthropic.Client, steps []int) {
	for range steps {
		client.Messages.New(nil, anthropic.MessageNewParams{})
	}
}
