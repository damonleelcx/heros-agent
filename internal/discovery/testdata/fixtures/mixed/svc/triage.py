import anthropic

client = anthropic.Anthropic()

def classify(text):
    return client.messages.create(model="claude-sonnet-4-5", messages=[])
