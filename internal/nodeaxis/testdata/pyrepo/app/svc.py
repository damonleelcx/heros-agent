"""Two OpenAI call sites whose LANGUAGE and FORM are identically covered, and which the engine answers
differently — which is the whole reason a per-node verdict cannot be a coverage lookup."""
from openai import OpenAI

client = OpenAI()


def plain(text):
    # Named arguments, written at the call site. The model is locatable and rewritable.
    return client.chat.completions.create(model="gpt-4o", messages=[{"role": "user", "content": text}])


def unpacked(text, opts):
    # The SAME provider, the SAME method, the SAME language, the SAME registry row — and the arguments
    # are assembled somewhere else and unpacked here. There is no `model=` to rewrite, and no
    # materializer will ever create one: the value does not exist in this file.
    return client.chat.completions.create(**opts)
