import anthropic

client = anthropic.Anthropic()


def good():
    return client.messages.create(model="claude-sonnet-4-5", messages=[])
