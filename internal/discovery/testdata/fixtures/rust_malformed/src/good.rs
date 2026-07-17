use async_openai::Client;

async fn good(client: &Client) {
    let _ = client.chat().create(req).await;
}
