// Command thinkcheck measures what thinking mode costs on a real extraction call.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/heros-foreal/heros/internal/config"
	"github.com/heros-foreal/heros/internal/discovery"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/provider/deepseek"
)

func main() {
	if err := config.LoadDotEnv(".env.local"); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	key, err := config.DeepSeekKey()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	repo := "."
	if len(os.Args) > 1 {
		repo = os.Args[1]
	}
	corpus, err := discovery.Walk(repo, discovery.Limits{})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	ix := discovery.NewIndex(corpus)
	excerpt, ok := ix.Excerpt("context")
	if !ok {
		fmt.Println("no context evidence")
		os.Exit(1)
	}

	c := deepseek.New(key)
	temp := 0.0
	msgs := []provider.Message{
		{Role: "system", Content: "You assess one axis of an AI agent for weaknesses. Reply with a JSON object only. Keep \"weakness\" to at most two sentences."},
		{Role: "user", Content: "Axis: context\n\nWhat the agent does here:\n" + excerpt +
			"\n\nReply as JSON: {\"axis\":string,\"weakness\":string,\"actionable\":boolean}"},
	}

	fmt.Printf("excerpt: %d chars\n\n", len(excerpt))
	fmt.Printf("%-10s %8s %8s %9s %8s %-10s %s\n",
		"REASONING", "IN", "OUT", "REASONING", "COST", "FINISH", "ANSWER")

	for _, mode := range []provider.Reasoning{provider.HighReasoning, provider.NoReasoning} {
		ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		resp, err := c.Complete(ctx, provider.Request{
			Model: deepseek.ModelFlash, MaxTokens: 1200, Temperature: &temp,
			JSONObject: true, Reasoning: mode, Messages: msgs,
		})
		cancel()
		if err != nil {
			fmt.Printf("%-10s FAILED: %v\n", mode, err)
			continue
		}
		answer := resp.Content
		if len(answer) > 60 {
			answer = answer[:60] + "…"
		}
		fmt.Printf("%-10s %8d %8d %9d %8s %-10s %s\n",
			mode, resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.ReasoningTokens,
			provider.FormatCents(resp.CostMicroCents), resp.FinishReason, answer)
	}
}
