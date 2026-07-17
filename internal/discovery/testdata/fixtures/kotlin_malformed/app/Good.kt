package com.example.app

import dev.langchain4j.model.chat.ChatLanguageModel

class Good(private val model: ChatLanguageModel) {
    fun good(): String {
        return model.generate("ok")
    }
}
