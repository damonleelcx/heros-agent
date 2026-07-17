use async_openai::Client;

async fn run(client: &Client) {
    let _ = client.chat().create(req).await;
}
