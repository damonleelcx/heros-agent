import com.myco.llm.LlmService;

class Main {
  String run(LlmService svc) {
    return svc.complete("summarize the ticket");
  }
}
