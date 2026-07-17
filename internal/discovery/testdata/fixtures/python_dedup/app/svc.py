from openai import OpenAI

client = OpenAI()


def run():
    return client.chat.completions.create(model="gpt-4o", messages=[])
