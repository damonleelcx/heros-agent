use async_openai::Client;
async fn ask(client: &Client) {
    let _ = client.chat().create(req).await;
    for _ in 0..3 { client.chat().create(req2).await.unwrap(); }
}
