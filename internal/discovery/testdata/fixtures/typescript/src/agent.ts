import Anthropic from "@anthropic-ai/sdk";
import OpenAI from "openai";
import { generateText } from "ai";

const anthropic = new Anthropic();
const openai = new OpenAI();

async function classify(text: string) {
  return anthropic.messages.create({ model: "claude-sonnet-4-5", messages: [{ role: "user", content: text }] });
}
async function loopAgent(items: string[]) {
  for (const it of items) {
    await openai.chat.completions.create({ model: "gpt-4o", messages: [] });
  }
}
async function vercel() {
  return generateText({ model: openai("gpt-4o"), prompt: "summarize" });
}
