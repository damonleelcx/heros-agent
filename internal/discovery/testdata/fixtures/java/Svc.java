import dev.langchain4j.model.openai.OpenAiChatModel;
class Svc {
  void run(OpenAiChatModel model) {
    String r = model.generate("summarize this");
  }
}
