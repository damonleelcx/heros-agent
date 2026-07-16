package loop

import "github.com/anthropics/anthropic-sdk-go"

func agent(client *anthropic.Client, steps []int) {
	for range steps {
		client.Messages.New(nil, anthropic.MessageNewParams{})
	}
}
