import anthropic
from openai import OpenAI

client = anthropic.Anthropic()

def classify(text):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": text}])

def agent(items):
    oai = OpenAI()
    for it in items:
        oai.chat.completions.create(model="gpt-4o", messages=[])
