# A REAL Ruby program that calls a model, in a language no frontend in this build handles.
#
# It exists so `RefusalLanguage` has a case that RENDERS. `copy_test.go` fails on a member of a closed
# vocabulary that nothing ever emits — a branch nobody has seen is a branch nobody has checked — and
# before this file, `refused / language` was exactly that: reachable in code, unreachable in any test.
#
# 🔴 It is a real tree parsed by real discovery, not an inline fixture. The property under test is
# "discovery recorded LANGUAGE_UNSUPPORTED for a language present in the tree", and an inline
# `DiscoveryReport` literal would carry whatever diagnostic the test author typed — proving the
# extractor agrees with the author about a repository nobody has.
require "anthropic"

class Triage
  def initialize
    @client = Anthropic::Client.new
  end

  def classify(text)
    @client.messages.create(model: "claude-sonnet-4-5", messages: [{ role: "user", content: text }])
  end
end
