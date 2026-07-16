package malformed

import "github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

func good(client *bedrockruntime.Client) {
	client.Converse(nil, &bedrockruntime.ConverseInput{})
}
