package main

import "github.com/anthropics/anthropic-sdk-go"

func main() {
	var c *anthropic.Client
	c.Messages.New(nil, anthropic.MessageNewParams{})
}
