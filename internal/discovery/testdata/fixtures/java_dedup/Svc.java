import dev.langchain4j.model.openai.OpenAiChatModel;

class Svc {
  String run(OpenAiChatModel model) {
    return model.generate("summarize this");
  }
}
