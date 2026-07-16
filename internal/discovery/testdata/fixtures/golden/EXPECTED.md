# golden fixture — expected (documented node count)

**3 nodes:**
1. classify  — anthropic Messages.New, single, model=symbolic const, prompt unresolved (empty params).
2. agent     — anthropic Messages.New, loop, variable_at_runtime=true.
3. run       — declared wrapper internal/llm.Complete, prompt="summarize the ticket".

The committed golden IR is expected-ir.json. Regenerate with UPDATE_GOLDEN=1.
