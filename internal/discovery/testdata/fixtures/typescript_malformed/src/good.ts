import Anthropic from "@anthropic-ai/sdk";

const anthropic = new Anthropic();

export async function good(text: string) {
  return anthropic.messages.create({ model: "claude-sonnet-4-5", messages: [] });
}
