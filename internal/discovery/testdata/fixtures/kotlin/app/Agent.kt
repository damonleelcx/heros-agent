package com.example.app

import dev.langchain4j.model.chat.ChatLanguageModel

class Agent(private val model: ChatLanguageModel) {
    fun classify(text: String): String {
        return model.generate(text)
    }

    fun batch(items: List<String>) {
        for (item in items) {
            model.generate(item)
        }
    }
}
