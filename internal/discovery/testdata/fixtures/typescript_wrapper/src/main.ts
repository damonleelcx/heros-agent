import { complete } from "@myco/llm";

export async function run() {
  return complete({ prompt: "summarize the ticket" });
}
