import dev.langchain4j.model.openai.OpenAiChatModel;
import java.util.List;

class Agent {
  void batch(OpenAiChatModel model, List<String> items) {
    for (String item : items) {
      model.generate(item);
    }
  }
}
