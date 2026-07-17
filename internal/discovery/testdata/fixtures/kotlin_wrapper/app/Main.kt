package com.example.app

import com.myco.llm.complete

fun run(): String {
    return complete(prompt = "summarize the ticket")
}
