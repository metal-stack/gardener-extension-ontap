package webhook

import (
	"bytes"
	_ "embed"
	"text/template"
)

//go:embed mutating-webhook.yaml.tpl
var webhookTemplate string

type Webhook struct {
	WebhookNamespace string
	CABundle         string
}

func Parse(webhook Webhook) (string, error) {
	tmpl := template.Must(template.New("webhook").Parse(string(webhookTemplate)))
	var result bytes.Buffer

	err := tmpl.Execute(&result, webhook)
	if err != nil {
		return "", err
	}
	return result.String(), nil
}