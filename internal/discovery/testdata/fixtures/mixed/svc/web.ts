import OpenAI from "openai";
const openai = new OpenAI();
export async function ask() {
  return openai.chat.completions.create({ model: "gpt-4o", messages: [] });
}
