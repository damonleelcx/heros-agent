# NEAR-MISS: two calls in ONE FILE with no data dependency between them.
#
# `translate` and `moderate` are called from the same module and share nothing: neither reads the
# other's output, and neither runs because of the other. Proximity in a file is not topology, and an
# agent that connects whatever is nearby has no discriminative power at all.
import anthropic

client = anthropic.Anthropic()


def translate(text):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": text}])


def moderate(text):
    return client.messages.create(model="claude-sonnet-4-5", messages=[{"role": "user", "content": text}])
