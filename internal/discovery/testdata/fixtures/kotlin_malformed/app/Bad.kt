package com.example.app

import dev.langchain4j.model.chat.ChatLanguageModel

class Bad(private val model: ChatLanguageModel {
    fun oops( : String {
        return model.generate(
    }
