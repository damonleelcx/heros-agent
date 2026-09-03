// Command streamcheck proves the streaming client against the live provider: that deltas arrive
// incrementally, that the assembled Response equals what the deltas said, and that usage and cost
// survive streaming — they arrive in a final frame that is very easy to drop.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/heros-foreal/heros/internal/config"
	"github.com/heros-foreal/heros/internal/provider"
	"github.com/heros-foreal/heros/internal/provider/qwen"
)

func main() {
	if err := config.LoadDotEnv(".env.local"); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	key, err := config.QwenKey()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	c := qwen.New(key)
	temp := 0.0
	req := provider.Request{
		Model: qwen.ModelFlash, MaxTokens: 300, Temperature: &temp, Reasoning: provider.NoReasoning,
		Messages: []provider.Message{
			{Role: "user", Content: "In two sentences, say what a retry ladder is."},
		},
	}

	var chunks int
	var firstAt time.Duration
	var acc strings.Builder
	start := time.Now()
	streamed, err := c.CompleteStream(context.Background(), req, func(d provider.Delta) {
		if chunks == 0 {
			firstAt = time.Since(start)
		}
		chunks++
		acc.WriteString(d.Text)
	})
	total := time.Since(start)
	if err != nil {
		fmt.Println("stream FAILED:", err)
		os.Exit(1)
	}

	fmt.Printf("deltas            %d\n", chunks)
	fmt.Printf("first delta at    %v   (total %v)\n", firstAt.Round(time.Millisecond), total.Round(time.Millisecond))
	fmt.Printf("usage             in=%d cached=%d out=%d reasoning=%d\n",
		streamed.Usage.InputTokens, streamed.Usage.CachedInputTokens,
		streamed.Usage.OutputTokens, streamed.Usage.ReasoningTokens)
	fmt.Printf("cost              %d micro-cents (%s)\n", streamed.CostMicroCents,
		provider.FormatCents(streamed.CostMicroCents))
	fmt.Printf("finish            %q   model %q\n", streamed.FinishReason, streamed.Model)
	fmt.Printf("accumulated == Response.Content? %v\n", acc.String() == streamed.Content)
	fmt.Printf("answer            %.90s\n\n", strings.ReplaceAll(streamed.Content, "\n", " "))

	if chunks < 2 {
		fmt.Println("only one delta - that is not streaming, it is a single frame")
		os.Exit(2)
	}
	if streamed.Usage.OutputTokens == 0 || streamed.CostMicroCents == 0 {
		fmt.Println("usage/cost lost in streaming - the final usage frame was dropped")
		os.Exit(2)
	}
	fmt.Println("streaming client OK")
}
