package dedup

import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

func run(client *bedrockruntime.Client) {
	client.Converse(nil, &bedrockruntime.ConverseInput{})
}
