import dev.langchain4j.model.openai.OpenAiChatModel;

class Good {
  void good(OpenAiChatModel model) {
    model.generate("ok");
  }
}
