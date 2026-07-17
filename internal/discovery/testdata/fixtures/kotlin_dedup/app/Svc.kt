package com.example.app

import dev.langchain4j.model.chat.ChatLanguageModel

class Svc(private val model: ChatLanguageModel) {
    fun run(): String {
        return model.generate("summarize this")
    }
}
